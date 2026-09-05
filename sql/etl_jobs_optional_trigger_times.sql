-- Allows a job to have an empty trigger_times ({}) — a deliberate
-- "manual-only, no automatic schedule" job, for a one-off/ad-hoc pull
-- that should never fire on its own, only ever via the admin UI's
-- "Run now" (Engine.TriggerNow, which doesn't consult trigger_times at
-- all — see internal/etl/engine.go's scheduleJob for how an empty list
-- is now treated as "nothing to schedule," logged once at Info, rather
-- than a configuration error).
--
-- Previously trigger_times required at least one entry
-- (etl_jobs_trigger_times_nonempty), forcing every job — including a
-- true one-time pull someone only ever intended to trigger by hand — to
-- carry a fake recurring daily time it would otherwise fire at forever.
--
-- Run this once against a database that already has sql/etl_engine.sql
-- applied with the old constraint. A fresh install already skips it.

ALTER TABLE app.etl_jobs DROP CONSTRAINT IF EXISTS etl_jobs_trigger_times_nonempty;
