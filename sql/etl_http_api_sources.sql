-- Adds a new ETL source kind, 'http_api': pulls paginated JSON out of an
-- HTTP API (instead of a SQL database) on the same schedule/engine as the
-- existing Oracle/MSSQL/Postgres sources. See internal/etl/httpsource.go
-- for the extraction path and internal/etl/models.go's KindHTTPAPI.
--
-- Field reuse on app.etl_sources for this kind (no new columns needed
-- there — same generic "connection + one secret" shape as the DB kinds):
--   host                -> base URL, e.g. "https://api.example.com"
--                          (source_query on the job supplies the path)
--   username             -> the API's "api-id" (a plain identifier, sent
--                          as-is in the api-id request header)
--   password_encrypted   -> the API's secret key (pgcrypto-encrypted at
--                          rest exactly like a DB password already is —
--                          see crypto.go; never sent on the wire itself,
--                          only used locally to compute the HMAC
--                          signature sent as the api-signature header)
--   port, database_name  -> unused for this kind; NOT NULL is dropped
--                          below so a source row doesn't need meaningless
--                          placeholder values
ALTER TABLE app.etl_sources ALTER COLUMN port DROP NOT NULL;
ALTER TABLE app.etl_sources ALTER COLUMN database_name DROP NOT NULL;

ALTER TABLE app.etl_sources DROP CONSTRAINT IF EXISTS etl_sources_kind_check;
ALTER TABLE app.etl_sources ADD CONSTRAINT etl_sources_kind_check
    CHECK (kind IN ('oracle', 'mssql', 'postgres', 'http_api'));

-- New job columns, only meaningful when the job's source is kind
-- 'http_api' (enforced in Go's JobInput.validate, not a DB CHECK — that
-- would need a cross-table lookup into etl_sources, which a CHECK
-- constraint can't do). For every other source kind these stay their
-- defaults and are ignored.
ALTER TABLE app.etl_jobs ADD COLUMN IF NOT EXISTS source_fields text[] NULL;
ALTER TABLE app.etl_jobs ADD COLUMN IF NOT EXISTS records_path text NOT NULL DEFAULT 'data';
ALTER TABLE app.etl_jobs ADD COLUMN IF NOT EXISTS page_size integer NOT NULL DEFAULT 500;

ALTER TABLE app.etl_jobs DROP CONSTRAINT IF EXISTS etl_jobs_page_size_positive;
ALTER TABLE app.etl_jobs ADD CONSTRAINT etl_jobs_page_size_positive
    CHECK (page_size > 0);
