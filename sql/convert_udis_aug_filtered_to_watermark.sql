-- Abandon the full_refresh + meter-ID {{FILTER}} approach for
-- load-udis-aug-filtered -- RD_METER_READS.ID has no index, so which
-- 800-id chunk landed first was pure luck: run 20 finished its whole job
-- in 42s, run 21 sat on chunk 1 alone for an hour producing nothing
-- (confirmed via query_text: never advanced past chunk 1), even after
-- dropping the pointless ORDER BY. TV_UPDATE, by contrast, IS indexed on
-- both RD_METER_READS202608 and 202609 (confirmed via ALL_INDEXES) --
-- exactly what made load-udis-aug's incremental pulls fast and steady
-- once watermark_type/PREFETCH_ROWS were fixed. Switch to that same
-- proven shape instead: pull everything past the watermark, no meter
-- filter at all.
--
-- Correctness note: dropping the meter filter means this job now pulls
-- readings for every device UDIS has, not just the ~2680 in app.meters.
-- That's fine -- migrate_raw_meter_readings() already drops any reading
-- whose udis_id doesn't resolve to exactly one meter_number (see
-- raw_meter_readings_migration.sql), so an unrecognized/irrelevant
-- device's rows land in the temp table and get dropped there, same as
-- before. The meter filter was a volume optimization, never a
-- correctness requirement.
UPDATE app.etl_jobs
SET
    mode              = 'incremental',
    filter_query       = NULL,
    filter_batch_size  = NULL,
    watermark_column   = 'tv_update',
    watermark_type     = 'integer',
    source_query = $q$SELECT rmr.ID, rmr.TV, rmr.DATA_ITEM_ID, rmr.VAL, rmr.TV_UPDATE, '202608' AS SHEET_NAME
FROM UDIS_CH.RD_METER_READS202608 rmr
WHERE rmr.TV_UPDATE > {{WATERMARK}}
  AND rmr.DATA_ITEM_ID IN ('00100000','00300000','01500001','01500002','05001211','05001212')
ORDER BY rmr.TV_UPDATE$q$
WHERE name = 'load-udis-aug-filtered';

-- First run starts from watermark_type integer's default ("0", see
-- query.go's defaultWatermark) since this job has never run in
-- incremental mode before -- no app.etl_job_state row exists for it yet.
-- That's fine: RD_METER_READS202608 only contains August's rows by
-- construction, so "TV_UPDATE > 0" just means "pull the whole table",
-- which is exactly what a first backfill of this month should do.
