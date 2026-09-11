-- Records the actual query (or, for an http_api source, request URL) sent
-- for a run -- with {{WATERMARK}}/{{FILTER}} already substituted with the
-- real values used, not the raw template from app.etl_jobs.source_query.
-- Written as soon as the query is built, before it's executed, so it's
-- visible for a run that's still in flight (or stuck) -- exactly the
-- moment this is most useful for diagnosing what Oracle is actually
-- chewing on right now, not just after the fact.
ALTER TABLE app.etl_job_runs ADD COLUMN IF NOT EXISTS query_text text;
