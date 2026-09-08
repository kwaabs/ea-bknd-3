-- UDIS smart-meter interval-read pipeline (UDIS_CH.RD_METER_READS<month>,
-- confirmed at 18.8B+ rows and growing ~600M rows/day — see the ETL job
-- design discussion). No index on ID exists on the Oracle side and none
-- can be added (source is externally owned), so the ETL job cannot
-- filter to specific meters at the Oracle end — it must land every
-- meter's rows for the tracked DATA_ITEM_IDs, incrementally, and this
-- pipeline is what narrows that down on the Postgres side.
--
-- meter_type is NOT hardcoded anywhere here — which type(s) actually get
-- merged into the real table is a per-call argument to
-- udis_meter_reads_merge, not something fixed at migration time. BSP is
-- just today's use case, not the only one this pipeline should ever
-- support.
--
-- Ingestion sequence (same "landing table -> separate merge procedure"
-- contract as sql/populate_mms_customer_sales_from_raw.sql and the MMS
-- resync procedures — see internal/etl/models.go's package doc comment):
--   ETL job "udis_meter_reads" (mode=incremental, watermark on TV_UPDATE,
--   see this file's bottom comment for the exact job config) appends new
--   rows to app.udis_meter_reads_raw on its own schedule
--     -> SELECT app.udis_meter_reads_merge(ARRAY['BSP']);
--        (or whichever meter type(s) you actually want merged this run —
--        see the function's own comment)
--        run right after each ETL run of that job, same pairing as the
--        MMS resync functions already run after each MMS load.

-- Pure landing buffer — every meter's rows for the tracked
-- DATA_ITEM_IDs, before any meter-type filtering. id is text (not
-- numeric) so it joins directly against app.meters.udis_id (also text)
-- with no cast, and so a malformed/unexpected ID value can never fail
-- the whole batch load the way a numeric column with a CHECK constraint
-- could.
CREATE TABLE IF NOT EXISTS app.udis_meter_reads_raw (
    id            text NOT NULL,   -- Oracle RD_METER_READS.ID -> app.meters.udis_id
    tv            bigint NOT NULL, -- reading interval timestamp, unix epoch seconds
    data_item_id  text NOT NULL,   -- which measurement (e.g. '00100000')
    val           text,            -- raw VARCHAR2 value as Oracle returns it; cast at merge time
    tv_update     bigint NOT NULL, -- source's own load-batch marker; this job's watermark column
    loaded_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_udis_meter_reads_raw_id ON app.udis_meter_reads_raw (id);

-- app.meters.udis_id had no index — needed for the merge join below to
-- stay fast regardless of how large app.meters grows.
CREATE INDEX IF NOT EXISTS idx_meters_udis_id ON app.meters (udis_id) WHERE udis_id IS NOT NULL;

-- Real reading table, across whichever meter type(s) have actually been
-- merged in so far — meter_type is carried on every row precisely
-- because it isn't a fixed, single-value concern; a query here can
-- filter/group by it directly without joining back to app.meters. One
-- row per (meter, interval, measurement) — a re-synced reading (the
-- source is known to periodically re-write rows, per the same pattern
-- already documented for MMS) updates in place via the ON CONFLICT in
-- udis_meter_reads_merge rather than accumulating duplicates.
CREATE TABLE IF NOT EXISTS app.udis_meter_reads (
    meter_number  text NOT NULL,
    meter_type    text NOT NULL,
    udis_id       text NOT NULL,
    tv            bigint NOT NULL,
    data_item_id  text NOT NULL,
    val           numeric,
    tv_update     bigint NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (udis_id, tv, data_item_id)
);

CREATE INDEX IF NOT EXISTS idx_udis_meter_reads_meter_number ON app.udis_meter_reads (meter_number);
CREATE INDEX IF NOT EXISTS idx_udis_meter_reads_meter_type ON app.udis_meter_reads (meter_type);

-- safe_numeric: casts text to numeric, returning NULL instead of raising
-- on a malformed value. RD_METER_READS.VAL is a free-text VARCHAR2(64)
-- on a source this app doesn't own — one unexpected non-numeric value
-- (blank, a stray unit suffix, whatever) must not fail an entire merge
-- batch the way a bare ::numeric cast would. Deliberately a plain
-- (unscaled) numeric, both here and on the table column above — a
-- declared scale like numeric(p,2) would round the source's real
-- decimal digits; this preserves them exactly, however many there are.
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

-- udis_meter_reads_merge: joins the raw landing buffer against
-- app.meters, upserts into the real table, then clears staging.
--
-- p_meter_types controls which meter(s) actually get kept, per call —
-- NOT fixed by this function or its tables:
--   SELECT app.udis_meter_reads_merge(ARRAY['BSP']);              -- BSP only
--   SELECT app.udis_meter_reads_merge(ARRAY['BSP','Residential']); -- several types
--   SELECT app.udis_meter_reads_merge();                          -- every meter, no filter
--
-- TRUNCATE (not per-row DELETE) is safe here because staging is a pure
-- transient buffer with no other reader and every run's rows get
-- resolved (merged as a wanted type, or correctly excluded as one this
-- call didn't ask for) in this same pass — there's nothing left worth
-- keeping after it runs, regardless of match/no-match. Note that means a
-- row for a meter type excluded on one call is gone, not deferred — if
-- you need several types, pass them all in one call (or add a second
-- meter_type array and merge before the first call's TRUNCATE, not
-- after).
CREATE OR REPLACE FUNCTION app.udis_meter_reads_merge(p_meter_types text[] DEFAULT NULL)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_matched int;
BEGIN
    INSERT INTO app.udis_meter_reads (meter_number, meter_type, udis_id, tv, data_item_id, val, tv_update)
    SELECT m.meter_number, m.meter_type, raw.id, raw.tv, raw.data_item_id,
           app.safe_numeric(raw.val), raw.tv_update
    FROM app.udis_meter_reads_raw raw
    JOIN app.meters m ON m.udis_id = raw.id
    WHERE p_meter_types IS NULL OR m.meter_type = ANY(p_meter_types)
    ON CONFLICT (udis_id, tv, data_item_id) DO UPDATE
        SET val          = EXCLUDED.val,
            tv_update    = EXCLUDED.tv_update,
            meter_number = EXCLUDED.meter_number,
            meter_type   = EXCLUDED.meter_type,
            updated_at   = now();

    GET DIAGNOSTICS v_matched = ROW_COUNT;
    RAISE NOTICE 'udis_meter_reads_merge(%): % row(s) merged into app.udis_meter_reads', p_meter_types, v_matched;

    TRUNCATE app.udis_meter_reads_raw;
END;
$$;

-- ---------------------------------------------------------------------------
-- ETL job configuration (create via the admin UI's Add Job wizard — this
-- engine has no cross-database credentials to create it directly). The
-- job itself is meter-type-agnostic — it always lands every meter's
-- rows; meter_type only enters the picture at merge time, above.
--
--   Source: the existing Oracle "udis" source (UDIS_CH schema)
--   Mode: Incremental
--   Watermark column: tv_update   Watermark type: Integer
--   Source query (note DATA_ITEM_ID values quoted as strings — the
--   column is VARCHAR2, and bare numeric literals force an implicit
--   conversion that also happens to defeat any future index on it):
--
--     SELECT rmr.ID, rmr.TV, rmr.DATA_ITEM_ID, rmr.VAL, rmr.TV_UPDATE
--     FROM UDIS_CH.RD_METER_READS202608 rmr
--     WHERE rmr.DATA_ITEM_ID IN ('00100000','00300000','01500001','01500002','05001211','05001212')
--       AND rmr.TV_UPDATE > {{WATERMARK}}
--     ORDER BY rmr.TV_UPDATE
--
--   Destination: app.udis_meter_reads_raw
--   Column mapping (source -> dest, in this order):
--     ID -> id, TV -> tv, DATA_ITEM_ID -> data_item_id, VAL -> val, TV_UPDATE -> tv_update
--   Conflict columns: none — plain append, TRUNCATEd by the merge above
--
-- After each run of this job (same pairing as the MMS resync functions):
--   SELECT app.udis_meter_reads_merge(ARRAY['BSP']); -- or whichever type(s) you need
-- ---------------------------------------------------------------------------
