-- Wires up keyset pagination (see etl_jobs_cursor_columns.sql) for
-- load-udis-aug-filtered, replacing the plain FETCH FIRST 10000 ROWS
-- ONLY cap from rebuild_udis_aug_filtered_from_fme.sql -- that cap
-- would have silently DROPPED any rows past the first 10,000 for a
-- given 800-meter batch. 10,000 is now a page size: the engine keeps
-- re-running this query for the same 800-meter batch, each time past
-- the previous page's last (id, tv), until a page comes back with zero
-- rows -- so a batch with more than 10,000 matching rows still gets all
-- of them, just across several bounded round-trips instead of one
-- unbounded one, with each page written to app.raw_meter_readings_temp
-- as it arrives.
--
-- dest_columns order (bd.Meter_number, rmr.id, rmr.TV, ...) is
-- unchanged, so cursor_columns = ['id','tv'] correctly points at
-- source_query's 2nd and 3rd SELECT columns.
UPDATE app.etl_jobs
SET
    cursor_columns = ARRAY['id', 'tv'],
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
    AND (rmr.id > {{CURSOR_COL1}} OR (rmr.id = {{CURSOR_COL1}} AND rmr.TV > {{CURSOR_COL2}}))
ORDER BY rmr.id, rmr.TV
FETCH FIRST 10000 ROWS ONLY$q$
WHERE name = 'load-udis-aug-filtered';
