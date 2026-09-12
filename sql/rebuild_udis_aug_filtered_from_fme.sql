-- Rebuilds load-udis-aug-filtered to match the real, proven production
-- FME workspace (udis-energy-readings-for-ea-prod-20-02-2026.fmw)
-- exactly, instead of the from-scratch design that turned out to stall
-- unpredictably. Key corrections learned from that workspace:
--
--   1. Filter key is meter_number (m.asset_no), not udis_id -- Oracle
--      resolves METER_ID fresh via a live M_METER join every run. This
--      job now has NO dependency on app.meters.udis_id at all, sidestepping
--      the whole stale/duplicate-udis_id problem rather than needing it
--      fixed.
--   2. ORDER BY rmr.id, rmr.TV is exactly what production uses -- the
--      earlier theory that this ORDER BY alone caused the stall doesn't
--      hold given it's proven in the real FME process; the M_METER join
--      narrowing the row set first is likely what keeps the sort cheap
--      there, missing from the from-scratch version.
--   3. Row cap FETCH FIRST 10000 ROWS ONLY per 800-meter batch, per
--      request -- bounds any single batch's query size/duration
--      regardless of how much a given batch of devices has accumulated.
--
-- mode back to full_refresh (required for filter_query/{{FILTER}}) --
-- watermark_column/type cleared since incremental doesn't apply here.
-- TV lower bound stays a fixed literal for now, same as before; the
-- FME workspace instead recomputes it live from its own destination
-- table's MAX(tv) each run (no persisted state, self-healing) -- worth
-- building as a real engine feature once this shape is proven reliable,
-- not before.
UPDATE app.etl_jobs
SET
    mode              = 'full_refresh',
    watermark_column  = NULL,
    watermark_type    = NULL,
    filter_query      = $q$SELECT DISTINCT meter_number FROM app.meters
       WHERE TRIM(meter_number) <> ''
         AND lower(TRIM(meter_number)) NOT IN ('no_meter','no meter','non access')$q$,
    filter_batch_size = 800,
    dest_columns      = ARRAY['meter_number','id','tv','data_item_id','val','tv_update','sheet_name'],
    source_query = $q$WITH base_data AS (
    SELECT m.METER_ID, m.ASSET_NO AS Meter_number
    FROM "UDIS_CH"."M_METER" m
    WHERE m.asset_no IN ({{FILTER}})
)
SELECT
    bd.Meter_number,
    rmr.id,
    rmr.TV,
    rmr.DATA_ITEM_ID,
    rmr.VAL,
    rmr.TV_UPDATE,
    '202608' AS SHEET_NAME
FROM "UDIS_CH"."RD_METER_READS202608" rmr
JOIN base_data bd ON rmr.id = bd.METER_ID
WHERE
    rmr.tv >= 1786838400
    AND rmr.tv < (SELECT (TRUNC(SYSDATE) - TO_DATE('1970-01-01', 'YYYY-MM-DD')) * 86400 FROM DUAL)
    AND rmr.DATA_ITEM_ID IN ('00100000','00300000','01500001','01500002','05001211','05001212')
ORDER BY rmr.id, rmr.TV
FETCH FIRST 10000 ROWS ONLY$q$
WHERE name = 'load-udis-aug-filtered';
