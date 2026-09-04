-- Generic ETL engine schema: lets new source systems (Oracle, MSSQL,
-- Postgres today — anything else database/sql-compatible later) be wired
-- up as data (rows in these tables) rather than code. Adding a nightly
-- load = INSERT a source + a job; no redeploy needed for the common case
-- (a straight SELECT landed into a Postgres table on a schedule).
--
-- This engine only does E + L (extract from the source, land it into a
-- Postgres table in the `app` schema, on a schedule). It deliberately does
-- NOT do transform-on-the-way-in — that stays a separate concern, handled
-- the same way it already is elsewhere in this codebase: a landing table
-- populated here, then a PL/pgSQL procedure (e.g.
-- populate_mms_customer_sales, see populate_mms_customer_sales_from_raw.sql)
-- reads from it and merges/transforms into the real app-facing table. Keep
-- that split — it's what makes each half independently re-runnable and
-- testable.
--
-- Run this once against the app database before starting the server with
-- ETL jobs configured; internal/etl reads these tables at startup and every
-- few minutes after (see engine.go's reload loop) so new/edited rows here
-- take effect without a restart.

CREATE EXTENSION IF NOT EXISTS pgcrypto; -- gen_random_uuid()

-- ---------------------------------------------------------------------
-- app.etl_sources — one row per external database this engine can pull
-- from. The password is deliberately NOT a column here: it's read from
-- an environment variable at connect time (password_env_var names which
-- one), the same convention this codebase already uses for
-- config.LDAPBindPass (env var LDAP_BIND_PASS) — a Postgres row is not
-- where a credential for a DIFFERENT system's database should live.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS app.etl_sources (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name             text NOT NULL UNIQUE,
    kind             text NOT NULL CHECK (kind IN ('oracle', 'mssql', 'postgres')),
    host             text NOT NULL,
    port             integer NOT NULL,
    -- Oracle: service name. MSSQL/Postgres: database name.
    database_name    text NOT NULL,
    username         text NOT NULL,
    password_env_var text NOT NULL,
    -- Driver-specific extras as a flat string map, e.g. {"encrypt":"disable"}
    -- for an MSSQL box without a trusted cert, or {"sslmode":"require"} for
    -- Postgres. Optional; each connector applies only the keys it knows.
    extra_params     jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled          boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------
-- app.etl_jobs — one row per (source query -> destination table) pull,
-- on its own schedule.
--
-- source_query is a plain SELECT against the source database. For an
-- incremental job it may reference the {{WATERMARK}} token anywhere a
-- literal would go (e.g. "... WHERE updated_at > {{WATERMARK}} ORDER BY
-- updated_at"); the engine substitutes it with the last-seen watermark,
-- formatted per watermark_type, before running the query. This is a
-- literal text substitution, not a bind parameter — safe here ONLY
-- because the substituted value always originates from THIS engine's own
-- etl_job_state (never end-user input), and the engine formats/escapes it
-- per watermark_type before substituting (see internal/etl/query.go).
--
-- dest_columns must list the destination columns in the exact order
-- source_query's SELECT list returns them — the engine does not attempt
-- to match by name, only by position (mirrors how the periodic-fact join
-- procedures elsewhere in this repo are hand-verified once, not
-- dynamically introspected).
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS app.etl_jobs (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name             text NOT NULL UNIQUE,
    source_id        uuid NOT NULL REFERENCES app.etl_sources(id) ON DELETE RESTRICT,
    source_query     text NOT NULL,
    dest_schema      text NOT NULL DEFAULT 'app',
    dest_table       text NOT NULL,
    dest_columns     text[] NOT NULL,
    mode             text NOT NULL CHECK (mode IN ('full_refresh', 'incremental')) DEFAULT 'full_refresh',
    -- Only meaningful when mode = 'incremental'. watermark_column must also
    -- appear in dest_columns (the engine reads the watermark back off the
    -- loaded row by dest column name, not the source query's naming).
    watermark_column text NULL,
    watermark_type   text NULL CHECK (watermark_type IS NULL OR watermark_type IN ('timestamp', 'integer', 'string')),
    -- Optional natural key for the destination table. When set, each batch
    -- loads via INSERT ... ON CONFLICT (conflict_columns) DO UPDATE — same
    -- upsert shape as populate_mms_customer_sales_from_raw.sql's
    -- ON CONFLICT (meter_number, date_time) DO NOTHING, just DO UPDATE
    -- here since a re-extracted row should overwrite a stale one, not be
    -- silently skipped. When NULL, every run plain-appends (right for a
    -- pure fact/log landing table that's meant to accumulate every
    -- extract, the same shape as the raw MMS_SALES table this whole
    -- pattern was modeled on).
    conflict_columns text[] NULL,
    -- Times of night to trigger this job, as "HH:MM" 24h strings,
    -- server-clock (UTC — see scheduler.nextUTCMidnight's comment: Ghana
    -- has no DST, so UTC == Ghana local time year-round, same assumption
    -- this reuses). A job with several entries fires once at each. Stored
    -- as text (not a native time[]) so the Go side parses/validates it
    -- directly with time.Parse("15:04", ...) rather than depending on how
    -- the driver maps Postgres's TIME type.
    trigger_times    text[] NOT NULL,
    batch_size       integer NOT NULL DEFAULT 5000,
    -- Per-run timeout; guards against a stuck source connection wedging a
    -- worker forever.
    timeout_seconds  integer NOT NULL DEFAULT 3600,
    enabled          boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT etl_jobs_watermark_required_for_incremental
        CHECK (mode = 'full_refresh' OR (watermark_column IS NOT NULL AND watermark_type IS NOT NULL)),
    CONSTRAINT etl_jobs_trigger_times_nonempty CHECK (array_length(trigger_times, 1) > 0),
    CONSTRAINT etl_jobs_trigger_times_format
        CHECK (NOT EXISTS (SELECT 1 FROM unnest(trigger_times) t WHERE t !~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'))
);

CREATE INDEX IF NOT EXISTS idx_etl_jobs_source_id ON app.etl_jobs (source_id);

-- ---------------------------------------------------------------------
-- app.etl_job_state — one row per job, the incremental cursor. Same
-- "persist the checkpoint in the same transaction as the data it
-- describes, right before commit" contract as app.migration_checkpoints
-- elsewhere in this repo, just keyed by job id instead of a hardcoded
-- procedure name (there can be arbitrarily many ETL jobs).
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS app.etl_job_state (
    job_id         uuid PRIMARY KEY REFERENCES app.etl_jobs(id) ON DELETE CASCADE,
    last_watermark text NULL,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------
-- app.etl_job_runs — run history / observability. One row per attempt,
-- 'running' until the worker updates it to 'success' or 'failed'. A run
-- left stuck on 'running' (server crashed mid-job) is a visible signal on
-- its own — nothing auto-reconciles it, same as this repo's existing
-- migration_checkpoints has no auto-recovery for a crashed run either.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS app.etl_job_runs (
    id             bigserial PRIMARY KEY,
    job_id         uuid NOT NULL REFERENCES app.etl_jobs(id) ON DELETE CASCADE,
    started_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz NULL,
    status         text NOT NULL CHECK (status IN ('running', 'success', 'failed')) DEFAULT 'running',
    rows_extracted bigint NOT NULL DEFAULT 0,
    rows_loaded    bigint NOT NULL DEFAULT 0,
    error_message  text NULL
);

CREATE INDEX IF NOT EXISTS idx_etl_job_runs_job_started
    ON app.etl_job_runs (job_id, started_at DESC);

-- ---------------------------------------------------------------------
-- Example: registering a nightly Oracle pull (adjust before running).
-- The password itself lives in the server's environment as
-- ETL_ORACLE_FINANCE_PASSWORD, never in this table.
-- ---------------------------------------------------------------------
-- INSERT INTO app.etl_sources (name, kind, host, port, database_name, username, password_env_var)
-- VALUES ('oracle_finance', 'oracle', 'oracle.internal', 1521, 'FINPROD', 'etl_reader', 'ETL_ORACLE_FINANCE_PASSWORD');
--
-- INSERT INTO app.etl_jobs (name, source_id, source_query, dest_table, dest_columns, mode, watermark_column, watermark_type, trigger_times, batch_size)
-- SELECT
--     'oracle_finance_invoices',
--     id,
--     'SELECT invoice_id, customer_id, amount, updated_at FROM invoices WHERE updated_at > {{WATERMARK}} ORDER BY updated_at',
--     'raw_oracle_invoices',
--     ARRAY['invoice_id', 'customer_id', 'amount', 'updated_at'],
--     'incremental',
--     'updated_at',
--     'timestamp',
--     ARRAY['01:00', '03:30'],
--     5000
-- FROM app.etl_sources WHERE name = 'oracle_finance';
