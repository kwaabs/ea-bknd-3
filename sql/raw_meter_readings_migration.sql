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
--      script too. Fixed with a LEFT JOIN to app.meters on udis_id --
--      LEFT, not INNER, so a reading from a device app.meters doesn't
--      recognize still lands (meter_number NULL) rather than being
--      silently dropped, same "don't lose data" principle the original
--      script already followed everywhere else.
--
-- ASSUMPTION: app.meters.udis_id is text (confirmed against
-- internal/meters/model.go's `UdisID *string`), hence the `t.id::text`
-- cast below rather than casting udis_id to bigint.

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
    v_min_tv   bigint;
    v_max_tv   bigint;
    v_min_date date;
    v_max_date date;
    v_deleted  int;
    v_inserted int;
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

    -- 3. Insert temp data into production. meter_number resolved via
    -- app.meters (LEFT JOIN, see comment above); val cast guarded via
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
    LEFT JOIN app.meters m ON m.udis_id = t.id::text;
    GET DIAGNOSTICS v_inserted = ROW_COUNT;

    -- 4. Clear existing daily aggregates for the affected date range.
    DELETE FROM app.meter_consumption_daily
    WHERE consumption_date BETWEEN v_min_date AND v_max_date;

    -- 5. Recalculate daily consumption for the migrated date range.
    PERFORM app.calculate_daily_consumption(v_min_date, v_max_date, true);

    RAISE NOTICE 'migrate_raw_meter_readings: % row(s) replaced, % row(s) inserted, dates % to %',
        v_deleted, v_inserted, v_min_date, v_max_date;

    -- 6. Clear the temp table now that it's been migrated.
    TRUNCATE app.raw_meter_readings_temp;
END;
$$;

-- Run after each ETL run of the udis_meter_reads job, same pairing as
-- every other landing-table -> merge-procedure pipeline in this repo:
--   SELECT app.migrate_raw_meter_readings();
