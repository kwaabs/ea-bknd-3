-- Second-round correction for app.meters.udis_id, superseding
-- fix_udis_id_dtx_duplicates_202609.sql (which corrected 16 rows in
-- isolation and, because it patched one side of each collision without
-- re-deriving the other, exposed a second layer of the same corruption
-- underneath -- each corrected meter_number turned out to collide with a
-- DIFFERENT meter_number that already held its target udis_id).
--
-- All of it -- both the original 16 pairs and the 16 newly-exposed ones
-- -- sits inside one contiguous ASSET_NO block, 234402280-234402366, so
-- this pulls that whole neighborhood from UDIS_CH.M_METER (the master:
-- one ASSET_NO per METER_ID, confirmed no fan-out) and corrects every
-- meter_number in it in a single pass instead of chasing collisions one
-- ring at a time.
UPDATE app.meters m
SET udis_id = v.udis_id,
    updated_at = now()
FROM (VALUES
    ('234402280', '9309585'),  ('234402281', '7965862'),  ('234402282', '8473762'),
    ('234402283', '9370391'),  ('234402284', '8035824'),  ('234402285', '8035870'),
    ('234402286', '9428037'),  ('234402287', '8036022'),  ('234402288', '8035634'),
    ('234402289', '8035958'),  ('234402290', '9309587'),  ('234402291', '9370393'),
    ('234402292', '7999464'),  ('234402293', '9305029'),  ('234402294', '7999298'),
    ('234402295', '7999280'),  ('234402296', '8475066'),  ('234402297', '8035946'),
    ('234402298', '8035864'),  ('234402299', '9370395'),  ('234402300', '8035954'),
    ('234402301', '9428039'),  ('234402303', '7999302'),  ('234402304', '9305031'),
    ('234402307', '10076391'), ('234402309', '9914071'),  ('234402312', '9913365'),
    ('234402315', '10076357'), ('234402320', '10076359'), ('234402323', '10076361'),
    ('234402325', '9913417'),  ('234402326', '10238479'), ('234402329', '10076393'),
    ('234402331', '10076363'), ('234402333', '10076219'), ('234402334', '9913419'),
    ('234402335', '10075845'), ('234402336', '10068491'), ('234402337', '10075847'),
    ('234402338', '10076575'), ('234402340', '10238481'), ('234402341', '9913421'),
    ('234402346', '10076221'), ('234402347', '9913423'),  ('234402348', '9914093'),
    ('234402349', '10076223'), ('234402351', '10068493'), ('234402353', '9913425'),
    ('234402355', '9913427'),  ('234402357', '9913429'),  ('234402359', '9913367'),
    ('234402360', '9913185'),  ('234402362', '10006755'), ('234402364', '10078637'),
    ('234402366', '9914095')
) AS v(meter_number, udis_id)
WHERE m.meter_number = v.meter_number
  AND m.udis_id IS DISTINCT FROM v.udis_id;

-- Re-check GLOBALLY (not just this range) -- corruption elsewhere in
-- app.meters, outside 234402280-234402366, hasn't been ruled out yet.
-- SELECT udis_id, count(*), array_agg(meter_number) AS meter_numbers
-- FROM app.meters
-- WHERE udis_id IS NOT NULL
-- GROUP BY udis_id
-- HAVING count(DISTINCT meter_number) > 1
-- ORDER BY count(*) DESC;
