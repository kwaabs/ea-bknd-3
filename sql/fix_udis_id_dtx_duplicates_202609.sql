-- One-off correction for 16 app.meters rows that carried the wrong
-- udis_id, found 2026-09-11 while investigating why
-- migrate_raw_meter_readings()'s join could fan out (see
-- raw_meter_readings_migration.sql). All 16 were DTX meters whose
-- udis_id matched a different, unrelated meter_number's udis_id --
-- confirmed against UDIS_CH.M_METER (the master record: one ASSET_NO
-- per METER_ID, no fan-out on the source side) that this was app.meters
-- data corruption, not a real shared-device relationship. All 16 wrong
-- rows share the exact same created_at/updated_at as the rest of the
-- table, consistent with one bad mapping step during a bulk import
-- rather than 16 independent mistakes.
--
-- Correct udis_id per meter_number sourced directly from
-- UDIS_CH.M_METER (METER_ID for each ASSET_NO):
--
--   234402329 -> 10076393      234402338 -> 10076575
--   234402331 -> 10076363      234402341 -> 9913421
--   234402333 -> 10076219      234402346 -> 10076221
--   234402334 -> 9913419       234402347 -> 9913423
--   234402335 -> 10075845      234402348 -> 9914093
--   234402336 -> 10068491      234402349 -> 10076223
--   234402337 -> 10075847      234402351 -> 10068493
--                               234402353 -> 9913425
--                               234402355 -> 9913427

UPDATE app.meters m
SET udis_id = v.udis_id,
    updated_at = now()
FROM (VALUES
    ('234402329', '10076393'),
    ('234402331', '10076363'),
    ('234402333', '10076219'),
    ('234402334', '9913419'),
    ('234402335', '10075845'),
    ('234402336', '10068491'),
    ('234402337', '10075847'),
    ('234402338', '10076575'),
    ('234402341', '9913421'),
    ('234402346', '10076221'),
    ('234402347', '9913423'),
    ('234402348', '9914093'),
    ('234402349', '10076223'),
    ('234402351', '10068493'),
    ('234402353', '9913425'),
    ('234402355', '9913427')
) AS v(meter_number, udis_id)
WHERE m.meter_number = v.meter_number;

-- Verify: should return zero rows once this and app.meters are in sync.
-- SELECT udis_id, count(*), array_agg(meter_number) AS meter_numbers
-- FROM app.meters
-- WHERE udis_id IS NOT NULL
-- GROUP BY udis_id
-- HAVING count(*) > 1;
