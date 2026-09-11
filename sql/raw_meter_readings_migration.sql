-- Fixed version of the hand-run "migrate production data" step that moves
-- app.raw_meter_readings_temp (landed by the udis_meter_reads ETL job) into
-- app.raw_meter_readings and recalculates daily consumption. Replaces
-- sql/udis_meter_reads_pipeline.sql, which designed a different table
-- (staging.udis_meter_reads_raw) that was never what's actually deployed --
-- this targets the real tables.
--
-- Three bugs fixed from the original DO block:
--
--   1. The DELETE was scoped to `tv BETWEEN v_min_tv AND v_max_tv` --
--      every meter's rows in that timestamp range, not just this batch's.
--      The ETL job pulls incrementally on tv_update (when Oracle last
--      touched a row), a different dimension from tv (the reading's own
--      timestamp) -- a batch filtered by tv_update is not a complete
--      resupply of every meter whose readings fall in that tv window. A
--      production row for a meter this batch never touched, but whose tv
--      happened to land in range, was silently deleted and never
--      reinserted -- permanent, silent data loss. Fixed by scoping the
--      DELETE to exactly the (source_id, tv, data_item_id) keys present
--      in the incoming batch.
--
--   2. `val::double precision` had no error guard. RD_METER_READS.VAL is
--      free-text VARCHAR2 on a source this app doesn't own (already
--      confirmed unreliable) -- one malformed value raised an exception
--      and rolled back the whole migration (DELETE + INSERT + daily
--      recalc, one implicit transaction), not just that one row. Fixed by
--      routing the cast through safe_numeric(), which returns NULL
--      instead of raising.
--
--   3. meter_number was a straight passthrough from the temp table. The
--      ETL job can never populate it (an Oracle-only query has no access
--      to app.meters), so it landed NULL and stayed NULL through this
--      script too. Fixed with a join to app.meters on udis_id -- only
--      readings from a device app.meters actually recognizes get kept.
--      This is also the ETL job's stand-in for the meter filter the old
--      FME process applied at the Oracle end (via a live M_METER lookup):
--      the ETL job itself pulls every device's rows (Oracle has no index
--      on RD_METER_READS.ID to filter by there without risking the
--      query-duration failure documented below), and this join is where
--      unrecognized-device rows actually get dropped instead -- same end
--      result in app.raw_meter_readings, no Oracle-side cost.
--
-- ASSUMPTION: app.meters.udis_id is text (confirmed against
-- internal/meters/model.go's `UdisID *string`), hence the `t.id::text`
-- cast below rather than casting udis_id to bigint.
--
-- NOTE on non-unique udis_id (found 2026-09-11): app.meters.udis_id is
-- NOT guaranteed unique. Confirmed against UDIS_CH.M_METER (the master --
-- one ASSET_NO per METER_ID, no fan-out there) that the collisions found
-- so far are app.meters data corruption, not a real shared-device
-- relationship -- see fix_udis_id_dtx_duplicates_202609.sql and
-- refresh_meters_udis_id.sql. A plain INNER JOIN on udis_id would fan
-- out: one incoming reading for a shared udis_id produces one output row
-- per distinct meter_number sharing it. The join below refuses to guess:
-- it only resolves udis_id values that currently map to exactly one
-- DISTINCT meter_number (two app.meters ROWS agreeing on the same
-- meter_number, a duplicate-row bug rather than a mapping ambiguity,
-- still counts as resolved), and counts the rest as "ambiguous"
-- (reported separately from "unrecognized device") instead of inserting
-- for any candidate.
--
-- NOTE on query duration: a run of the old FME process against this same
-- source (meter-filtered AND date-windowed) took 4h12m and still failed
-- with ORA-01555 (snapshot too old) -- a long-running-query failure, not
-- specific to filtering. RD_METER_READS.ID has no index, so filtering by
-- it doesn't reduce Oracle's scan time, only the row count returned; the
-- actual defense against this failure is keeping the ETL job's tv_update
-- watermark window narrow (run it often), not filtering by meter at the
-- Oracle end -- which is why that filtering happens here instead, in
-- Postgres, where it's nearly free.

CREATE OR REPLACE FUNCTION app.safe_numeric(v text)
RETURNS numeric
LANGUAGE plpgsql
IMMUTABLE
AS $$
BEGIN
    RETURN v::numeric;
EXCEPTION WHEN OTHERS THEN
    RETURN NULL;
END;
$$;

-- Lets the DELETE below find matching production rows by key instead of a
-- table scan -- this migration runs every ETL cycle, not once, so it has
-- to stay cheap regardless of how large app.raw_meter_readings grows.
CREATE INDEX IF NOT EXISTS idx_raw_meter_readings_key
    ON app.raw_meter_readings (source_id, tv, data_item_id);

-- app.meters.udis_id had no index -- needed for the JOIN below to stay
-- fast regardless of how large app.meters grows.
CREATE INDEX IF NOT EXISTS idx_meters_udis_id ON app.meters (udis_id) WHERE udis_id IS NOT NULL;

CREATE OR REPLACE FUNCTION app.migrate_raw_meter_readings()
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_min_tv      bigint;
    v_max_tv      bigint;
    v_min_date    date;
    v_max_date    date;
    v_deleted     int;
    v_inserted    int;
    v_extracted   int;
    v_ambiguous   int;
BEGIN
    -- 1. Range of data in the temp table -- still needed for the daily-
    -- aggregate recalc below, which legitimately does span every meter
    -- for the date range (that part of the original script wasn't buggy,
    -- only the raw-table DELETE was).
    SELECT MIN(tv), MAX(tv), MIN(to_timestamp(tv)::date), MAX(to_timestamp(tv)::date)
    INTO v_min_tv, v_max_tv, v_min_date, v_max_date
    FROM app.raw_meter_readings_temp;

    IF v_min_tv IS NULL THEN
        RAISE NOTICE 'migrate_raw_meter_readings: temp table empty, nothing to do';
        RETURN;
    END IF;

    SELECT count(*) INTO v_extracted FROM app.raw_meter_readings_temp;

    -- 2. Delete only the production rows this batch is about to replace --
    -- keyed on the same (source_id, tv, data_item_id) triple the insert
    -- below is keyed on, not a blanket tv range that would also catch
    -- meters this batch never touched.
    DELETE FROM app.raw_meter_readings prod
    USING app.raw_meter_readings_temp t
    WHERE prod.source_id = t.id
      AND prod.tv = t.tv
      AND prod.data_item_id = t.data_item_id;
    GET DIAGNOSTICS v_deleted = ROW_COUNT;

    -- Count temp rows whose udis_id maps to more than one DISTINCT
    -- meter_number -- these get dropped below rather than fanned out to
    -- every candidate (see the non-unique udis_id note above). Grouped on
    -- DISTINCT meter_number, not row count: a udis_id with two app.meters
    -- ROWS that happen to share the same meter_number (a duplicate-row
    -- bug, not a mapping ambiguity -- both rows agree on the answer)
    -- isn't actually ambiguous and shouldn't be dropped here. Reported
    -- separately from "unrecognized device" so the two failure modes stay
    -- distinguishable.
    SELECT count(*) INTO v_ambiguous
    FROM app.raw_meter_readings_temp t
    WHERE t.id::text IN (
        SELECT udis_id FROM app.meters
        WHERE udis_id IS NOT NULL
        GROUP BY udis_id
        HAVING count(DISTINCT meter_number) > 1
    );

    -- 3. Insert temp data into production. meter_number resolved via
    -- app.meters, restricted to udis_id values that currently resolve to
    -- exactly one distinct meter_number (see note above) -- unrecognized
    -- and ambiguous devices are both dropped here; val cast guarded via
    -- safe_numeric() instead of a bare ::double precision.
    INSERT INTO app.raw_meter_readings (meter_number, source_id, tv, data_item_id, val, tv_update)
    SELECT
        m.meter_number,
        t.id,
        t.tv,
        t.data_item_id,
        app.safe_numeric(t.val)::double precision,
        t.tv_update
    FROM app.raw_meter_readings_temp t
    INNER JOIN (
        SELECT udis_id, min(meter_number) AS meter_number
        FROM app.meters
        WHERE udis_id IS NOT NULL
        GROUP BY udis_id
        HAVING count(DISTINCT meter_number) = 1
    ) m ON m.udis_id = t.id::text;
    GET DIAGNOSTICS v_inserted = ROW_COUNT;

    -- 4. Clear existing daily aggregates for the affected date range.
    DELETE FROM app.meter_consumption_daily
    WHERE consumption_date BETWEEN v_min_date AND v_max_date;

    -- 5. Recalculate daily consumption for the migrated date range.
    PERFORM app.calculate_daily_consumption(v_min_date, v_max_date, true);

    -- v_extracted - v_inserted is how many rows this run dropped, split
    -- into ambiguous (udis_id shared by >1 meter_number, see note above)
    -- and unrecognized (udis_id not in app.meters at all) -- either count
    -- usually sits near zero; a sudden jump is a signal app.meters needs
    -- attention (drifted udis_id -- see refresh_meters_udis_id.sql -- or
    -- genuinely new/duplicated meter rows).
    RAISE NOTICE 'migrate_raw_meter_readings: % row(s) extracted, % row(s) replaced, % row(s) inserted (% dropped: % ambiguous udis_id, % unrecognized device), dates % to %',
        v_extracted, v_deleted, v_inserted, v_extracted - v_inserted, v_ambiguous, (v_extracted - v_inserted - v_ambiguous), v_min_date, v_max_date;

    -- 6. Clear the temp table now that it's been migrated.
    TRUNCATE app.raw_meter_readings_temp;
END;
$$;

-- Run after each ETL run of the udis_meter_reads job, same pairing as
-- every other landing-table -> merge-procedure pipeline in this repo:
--   SELECT app.migrate_raw_meter_readings();
