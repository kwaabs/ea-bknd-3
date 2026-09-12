-- Adds the regular/special distinction to app.announcements. Regular
-- announcements keep rolling on the dashboard marquee exactly as before;
-- special ones are pulled out of the marquee entirely and shown only via
-- the new announcements dialog on the frontend (a separate,
-- higher-visibility channel, since the marquee's constant rotation risks
-- burying something urgent among ordinary notices).
--
-- No CREATE TABLE for app.announcements is tracked in this repo (it
-- predates sql/ migrations here) -- this only adds the new column to
-- whatever already exists in prod.
ALTER TABLE app.announcements ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'regular';

ALTER TABLE app.announcements DROP CONSTRAINT IF EXISTS announcements_kind_check;
ALTER TABLE app.announcements ADD CONSTRAINT announcements_kind_check
    CHECK (kind IN ('regular', 'special'));
