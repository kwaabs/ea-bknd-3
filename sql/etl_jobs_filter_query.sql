-- Adds a way for a job to pull only the source rows matching a list of
-- keys that live in THIS app database (e.g. "only meters in app.meters"),
-- without a live cross-database join — a job's source_query only ever
-- talks to its one registered external source, so there's no other way
-- for it to reach app.meters (or any other table in this database) in the
-- same query.
--
-- filter_query is a plain SELECT run against this app database before
-- every run; its single result column is chunked into
-- filter_batch_size-sized groups, and source_query is run once per chunk
-- with the literal token {{FILTER}} substituted for that chunk's values
-- as a SQL IN (...) list. See internal/etl/run.go's
-- extractAndLoadFiltered for the implementation and why it's restricted
-- to mode = 'full_refresh' (chunked pulls and incremental watermark
-- advancement don't have a sound combined semantics).
--
-- Run this once against a database that already has sql/etl_engine.sql
-- applied without these columns. A fresh install already gets them
-- straight from etl_engine.sql.

ALTER TABLE app.etl_jobs ADD COLUMN IF NOT EXISTS filter_query text NULL;
ALTER TABLE app.etl_jobs ADD COLUMN IF NOT EXISTS filter_batch_size integer NULL;

-- Postgres has no `ADD CONSTRAINT IF NOT EXISTS` — this DO block gets the
-- same idempotency by catching the "already exists" error and ignoring
-- it, so this file is safe to re-run.
DO $$
BEGIN
    ALTER TABLE app.etl_jobs ADD CONSTRAINT etl_jobs_filter_requires_full_refresh
        CHECK (filter_query IS NULL OR mode = 'full_refresh');
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE app.etl_jobs ADD CONSTRAINT etl_jobs_filter_batch_size_positive
        CHECK (filter_batch_size IS NULL OR filter_batch_size > 0);
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

-- ---------------------------------------------------------------------
-- Example: restrict an existing job's next run to a batch of meter
-- numbers from app.meters (the app's own Edit Job form does this the
-- same way):
-- ---------------------------------------------------------------------
-- UPDATE app.etl_jobs
-- SET source_query = 'SELECT meter_number, reading_value, reading_date FROM meter_readings WHERE meter_number IN ({{FILTER}})',
--     filter_query = 'SELECT meter_number FROM app.meters WHERE status = ''active''',
--     filter_batch_size = 1000,
--     updated_at = now()
-- WHERE name = 'oracle_finance_meter_readings';
