-- UDIS smart-meter interval-read pipeline (UDIS_CH.RD_METER_READS<month>,
-- confirmed at 18.8B+ rows and growing ~600M rows/day — see the ETL job
-- design discussion). No index on ID exists on the Oracle side and none
-- can be added (source is externally owned), so the ETL job cannot
-- filter to specific meters at the Oracle end — it must land every
-- meter's rows for the tracked DATA_ITEM_IDs, incrementally, and this
-- pipeline is what narrows that down to BSP meters on the Postgres side.
--
-- Ingestion sequence (same "landing table -> separate merge procedure"
-- contract as sql/populate_mms_customer_sales_from_raw.sql and the MMS
-- resync procedures — see internal/etl/models.go's package doc comment):
--   ETL job "udis_meter_reads" (mode=incremental, watermark on TV_UPDATE,
--   see this file's bottom comment for the exact job config) appends new
--   rows to app.udis_meter_reads_raw on its own schedule
--     -> SELECT app.udis_meter_reads_merge_to_bsp();
--        run right after each ETL run of that job, same pairing as the
--        MMS resync functions already run after each MMS load.

-- Pure landing buffer — every meter's rows for the tracked
-- DATA_ITEM_IDs, before any BSP filtering. id is text (not numeric) so
-- it joins directly against app.meters.udis_id (also text) with no
-- cast, and so a malformed/unexpected ID value can never fail the whole
-- batch load the way a numeric column with a CHECK constraint could.
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

-- Real, BSP-only reading table — what the app actually reports off of.
-- One row per (meter, interval, measurement) — a later re-sync of the
-- same reading (the source is known to periodically re-write rows, per
-- the same pattern already documented for MMS) updates in place via the
-- ON CONFLICT below rather than accumulating duplicates.
CREATE TABLE IF NOT EXISTS app.udis_bsp_meter_reads (
    meter_number  text NOT NULL,
    udis_id       text NOT NULL,
    tv            bigint NOT NULL,
    data_item_id  text NOT NULL,
    val           numeric,
    tv_update     bigint NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (udis_id, tv, data_item_id)
);

CREATE INDEX IF NOT EXISTS idx_udis_bsp_meter_reads_meter_number
    ON app.udis_bsp_meter_reads (meter_number);

-- safe_numeric: casts text to numeric, returning NULL instead of raising
-- on a malformed value. RD_METER_READS.VAL is a free-text VARCHAR2(64)
-- on a source this app doesn't own — one unexpected non-numeric value
-- (blank, a stray unit suffix, whatever) must not fail an entire merge
-- batch the way a bare ::numeric cast would.
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

-- udis_meter_reads_merge_to_bsp: joins the raw landing buffer against
-- app.meters (keeping only meter_type = 'BSP'), upserts into the real
-- table, then clears staging. TRUNCATE (not per-row DELETE) is safe here
-- because staging is a pure transient buffer with no other reader and
-- every run's rows get resolved (merged as BSP, or correctly dropped as
-- non-BSP) in this same pass — there's nothing left worth keeping after
-- it runs, regardless of match/no-match.
CREATE OR REPLACE FUNCTION app.udis_meter_reads_merge_to_bsp()
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_matched int;
BEGIN
    INSERT INTO app.udis_bsp_meter_reads (meter_number, udis_id, tv, data_item_id, val, tv_update)
    SELECT m.meter_number, raw.id, raw.tv, raw.data_item_id,
           app.safe_numeric(raw.val), raw.tv_update
    FROM app.udis_meter_reads_raw raw
    JOIN app.meters m ON m.udis_id = raw.id
    WHERE m.meter_type = 'BSP'
    ON CONFLICT (udis_id, tv, data_item_id) DO UPDATE
        SET val          = EXCLUDED.val,
            tv_update    = EXCLUDED.tv_update,
            meter_number = EXCLUDED.meter_number,
            updated_at   = now();

    GET DIAGNOSTICS v_matched = ROW_COUNT;
    RAISE NOTICE 'udis_meter_reads_merge_to_bsp: % row(s) merged into app.udis_bsp_meter_reads', v_matched;

    TRUNCATE app.udis_meter_reads_raw;
END;
$$;

-- ---------------------------------------------------------------------------
-- ETL job configuration (create via the admin UI's Add Job wizard — this
-- engine has no cross-database credentials to create it directly):
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
--   SELECT app.udis_meter_reads_merge_to_bsp();
-- ---------------------------------------------------------------------------
