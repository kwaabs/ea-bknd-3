-- Creates the ETL job for refresh_meters_udis_id.sql (source_query/
-- filter_query documented there). Run once, after that file's
-- CREATE TABLE/CREATE FUNCTION have been applied.
--
-- This re-derives EVERY meter_number's udis_id independently from
-- UDIS_CH.M_METER (keyed on its own ASSET_NO), not by chasing whichever
-- meter_number currently collides with which -- the one-off patch
-- (fix_udis_id_dtx_duplicates_202609.sql) only fixed 16 rows and
-- exposed a further layer of the same corruption on the meter_numbers
-- they'd been colliding with, because it corrected each row in
-- isolation instead of all of them from the same source of truth in
-- one shot.
INSERT INTO app.etl_jobs (
    name, source_id, source_query,
    dest_schema, dest_table, dest_columns,
    mode, filter_query, filter_batch_size,
    trigger_times, batch_size
) VALUES (
    'refresh-meters-udis-id',
    '06eaf548-bac3-4b22-95a0-f5af2882bf0c',
    'SELECT ASSET_NO, TO_CHAR(METER_ID) FROM UDIS_CH.M_METER WHERE ASSET_NO IN ({{FILTER}})',
    'app', 'udis_meter_id_raw', ARRAY['meter_number', 'udis_id'],
    'full_refresh',
    $q$SELECT DISTINCT meter_number FROM app.meters
       WHERE TRIM(meter_number) <> ''
         AND lower(TRIM(meter_number)) NOT IN ('no_meter','no meter','non access')$q$,
    800,
    '{}', 5000
);

-- Run it (admin UI "Run now", or Engine.TriggerNow), then:
--   SELECT app.refresh_meters_udis_id();
--
-- Verify afterward -- should return far fewer rows than the 21 seen
-- 2026-09-11, and any that remain are genuine duplicate app.meters ROWS
-- (same meter_number twice), not udis_id-mapping errors:
--   SELECT udis_id, count(*), array_agg(meter_number) AS meter_numbers
--   FROM app.meters
--   WHERE udis_id IS NOT NULL
--   GROUP BY udis_id
--   HAVING count(*) > 1
--   ORDER BY count(*) DESC;
