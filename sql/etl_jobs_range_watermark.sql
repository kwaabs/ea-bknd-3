-- Adds a persisted date-range checkpoint, independent of the existing
-- mode=incremental/{{WATERMARK}} mechanism (which explicitly cannot
-- combine with filter_query -- see extractAndLoadFiltered's comment in
-- internal/etl/run.go). See Job.RangeStepSeconds' comment for the full
-- design: when set, each run only scans a fixed-width date slice
-- ({{RANGE_START}}/{{RANGE_END}}, e.g. one day) instead of one unbounded
-- full_refresh scan since RangeStart -- built because a full ~month-wide
-- backfill window against an 18B-row Oracle table proved too expensive
-- to complete even with per-batch keyset pagination (see
-- etl_jobs_cursor_columns.sql): pagination doesn't help if Oracle can't
-- cheaply find even the *first* page within that wide a window.
--
-- The checkpoint only advances once an entire run (every filter chunk,
-- every page within each chunk) succeeds -- a failed/partial run leaves
-- it untouched, so the next trigger retries the same slice rather than
-- silently skipping it.
ALTER TABLE app.etl_jobs ADD COLUMN IF NOT EXISTS range_step_seconds integer NULL;
ALTER TABLE app.etl_jobs ADD COLUMN IF NOT EXISTS range_start text NULL;
