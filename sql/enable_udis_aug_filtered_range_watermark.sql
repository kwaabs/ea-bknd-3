-- Enables the new range-watermark slicing (etl_jobs_range_watermark.sql)
-- for load-udis-aug-filtered: one day per run instead of the fixed
-- month-wide "1786838400 through today" window that was taking 10+
-- hours and getting killed by Oracle before returning a single row.
--
-- Clears any leftover app.etl_job_state row first -- this job briefly
-- ran in mode=incremental earlier (tv_update-based), and may still have
-- a state row from that experiment. app.etl_job_state is being reused
-- here for a completely different checkpoint semantic (a date-range
-- slice boundary, not a tv_update watermark), so a stale leftover value
-- must not be picked up as this feature's starting point.
DELETE FROM app.etl_job_state
WHERE job_id = (SELECT id FROM app.etl_jobs WHERE name = 'load-udis-aug-filtered');

UPDATE app.etl_jobs
SET
    range_step_seconds = 86400,   -- one day per run
    range_start         = '1786838400',
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
    rmr.tv >= {{RANGE_START}}
    AND rmr.tv < {{RANGE_END}}
    AND rmr.DATA_ITEM_ID IN ('00100000','00300000','01500001','01500002','05001211','05001212')
    AND (rmr.id > {{CURSOR_COL1}} OR (rmr.id = {{CURSOR_COL1}} AND rmr.TV > {{CURSOR_COL2}}))
ORDER BY rmr.id, rmr.TV
FETCH FIRST 10000 ROWS ONLY$q$
WHERE name = 'load-udis-aug-filtered';

-- After each run, check where the checkpoint landed and how much data
-- came back for that one-day slice:
--   SELECT last_watermark, to_timestamp(last_watermark::bigint) AS as_of, updated_at
--   FROM app.etl_job_state
--   WHERE job_id = (SELECT id FROM app.etl_jobs WHERE name = 'load-udis-aug-filtered');
