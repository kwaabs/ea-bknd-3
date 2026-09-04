# ETL engine

A small in-process data-loading engine (`internal/etl`) that pulls rows out
of external databases — Oracle, MSSQL, Postgres today, anything else
`database/sql`-compatible later — on a nightly schedule and lands them into
a table in this app's own `app` schema.

It only does **E + L**. It deliberately does not transform on the way in.
A landing table this engine populates is meant to be read by a separate
PL/pgSQL procedure that merges/transforms it into the real app-facing
table — the same two-step shape already used for MMS/Zeus (see
`sql/populate_mms_customer_sales_from_raw.sql`). Keep that split: each half
stays independently re-runnable and testable.

Sources and jobs are **rows in Postgres**, not Go code. Adding a new
nightly pull from a new system is an `INSERT`, not a redeploy — the engine
re-reads `app.etl_sources`/`app.etl_jobs` every `ETL_RELOAD_INTERVAL_SECONDS`
(default 5 minutes) and picks up new/edited/removed rows automatically.

## Setup

1. Apply `sql/etl_engine.sql` against the app database. It creates
   `app.etl_sources`, `app.etl_jobs`, `app.etl_job_state`, `app.etl_job_runs`.
   If this database previously had the older `password_env_var`-based
   schema, also apply `sql/etl_sources_password_encryption.sql`.
2. Set `ETL_CREDENTIALS_ENCRYPTION_KEY` in the server's environment (`.env`
   in dev) — a passphrase used to encrypt/decrypt every source's password
   at rest (pgcrypto PGP symmetric). This is the **one** secret this
   feature needs in the process environment; see "Source passwords" below
   for why.
3. Register a source and a job — either through the ETL admin UI
   (`/admin/etl` in the frontend: Sources tab, then Jobs tab; both have a
   Test button before you commit) or directly in SQL, e.g.:

   ```sql
   INSERT INTO app.etl_sources (name, kind, host, port, database_name, username, password_encrypted)
   VALUES ('oracle_finance', 'oracle', 'oracle.internal', 1521, 'FINPROD', 'etl_reader',
           pgp_sym_encrypt('the-password', 'the-same-key-as-ETL_CREDENTIALS_ENCRYPTION_KEY'));
   ```

   `kind` is one of `oracle`, `mssql`, `postgres`. `extra_params` (jsonb) is
   for driver-specific extras, e.g. `{"encrypt":"disable"}` for an MSSQL box
   without a trusted cert.

4. Register a job (admin UI, or SQL):

   ```sql
   INSERT INTO app.etl_jobs (name, source_id, source_query, dest_table, dest_columns, mode, watermark_column, watermark_type, trigger_times, batch_size)
   SELECT
       'oracle_finance_invoices',
       id,
       'SELECT invoice_id, customer_id, amount, updated_at FROM invoices WHERE updated_at > {{WATERMARK}} ORDER BY updated_at',
       'raw_oracle_invoices',
       ARRAY['invoice_id', 'customer_id', 'amount', 'updated_at'],
       'incremental',
       'updated_at',
       'timestamp',
       ARRAY['01:00', '03:30'],
       5000
   FROM app.etl_sources WHERE name = 'oracle_finance';
   ```

   The destination table (`raw_oracle_invoices` here) must already exist —
   this engine loads into it, it doesn't create/manage its schema. Column
   order in `dest_columns` must match `source_query`'s `SELECT` list
   position-for-position; the engine maps by position, not by name.

5. Start (or restart) the server. Within `ETL_RELOAD_INTERVAL_SECONDS` the
   new job is picked up and scheduled — no restart needed for jobs added
   *after* that.

## Source passwords

Stored encrypted at rest in `app.etl_sources.password_encrypted`
(pgcrypto, PGP symmetric / AES-256), under `ETL_CREDENTIALS_ENCRYPTION_KEY`
— not plaintext, and not the earlier `password_env_var` design (one env
var per source). The switch was deliberate: these source-system passwords
rotate on a ~30-day policy, and one-env-var-per-source meant an ops ticket
plus a server restart every rotation, for every source. An encrypted
column lets whoever manages a source rotate its password from the Edit
Source form itself — no server access needed for routine rotation, only
for the one encryption key, which is set once and rotated rarely (see
`sql/etl_sources_password_encryption.sql` for the full write-up,
including how to re-key if `ETL_CREDENTIALS_ENCRYPTION_KEY` itself ever
needs to change).

The Edit Source form's password field is write-only — it's never
prefilled with the current password (the API never returns it, only a
`has_password` boolean), and leaving it blank on an update keeps the
existing password unchanged.

## The `{{WATERMARK}}` contract (incremental jobs only)

`source_query` may reference the literal token `{{WATERMARK}}` anywhere a
value would go. Before each run, the engine substitutes it with the last
watermark seen (from `app.etl_job_state`, or a documented default on a
job's very first run — see `defaultWatermark` in `internal/etl/query.go`),
formatted as a SQL literal per `watermark_type`:

- `timestamp` / `string` → single-quoted, with embedded quotes escaped
- `integer` → a bare number

This is a text substitution, not a bind parameter — safe specifically
because the substituted value only ever comes from this engine's own
`app.etl_job_state`, never from request/user input (see the comment on
`source_query` in `sql/etl_engine.sql` for the full reasoning).

Two requirements on an incremental job's `source_query`:

- It must `ORDER BY` the watermark column ascending — the engine tracks
  "furthest reached" as the *last row of the last completed batch*, not a
  computed max, the same assumption the keyset-pagination procedures
  elsewhere in this repo already place on their own `ORDER BY`.
- `watermark_column` must also appear in `dest_columns` — the engine reads
  the watermark back off the *destination* column by name after loading,
  not the source query's own column naming.

## Upserts vs. plain append

Set `conflict_columns` (text[]) on a job to get
`INSERT ... ON CONFLICT (conflict_columns) DO UPDATE` per batch — the same
upsert shape as `populate_mms_customer_sales_from_raw.sql`'s
`ON CONFLICT (meter_number, date_time)`. Leave it `NULL` for a pure
append-only landing table (right for a fact/log table that's meant to
accumulate every extract untouched, same shape as the raw `MMS_SALES` table
this whole engine's landing-table pattern was modeled on).

## Scheduling model

One goroutine per enabled job waits for its next `trigger_times` entry
("HH:MM", 24h, UTC — same "Ghana has no DST" assumption
`scheduler.StartDailySessionReset` already documents) and dispatches onto a
channel drained by a bounded pool of `ETL_WORKERS` (default 3) worker
goroutines — no more than `ETL_WORKERS` extracts ever run concurrently
regardless of how many jobs' trigger times land close together. A job
whose previous run is still in progress when its next trigger fires is
skipped, not queued or run twice at once.

## Observability

```sql
-- Recent runs for one job
SELECT started_at, finished_at, status, rows_extracted, rows_loaded, error_message
FROM app.etl_job_runs
WHERE job_id = (SELECT id FROM app.etl_jobs WHERE name = 'oracle_finance_invoices')
ORDER BY started_at DESC LIMIT 20;

-- Current watermark
SELECT * FROM app.etl_job_state WHERE job_id = (SELECT id FROM app.etl_jobs WHERE name = 'oracle_finance_invoices');
```

A run stuck on `status = 'running'` with no `finished_at` is the signal
that the server crashed or was killed mid-job — nothing auto-reconciles it
(same as this repo's `app.migration_checkpoints` has no auto-recovery for a
crashed run either). Re-running the job is safe; incremental jobs resume
from the last *committed* batch's watermark, not the crashed one.

## Current limitations (v1)

- Binary/blob source columns aren't supported — `[]byte` scanned off a
  source row is always treated as text (see `normalizeValue` in
  `internal/etl/run.go`). Fine for ordinary business data; not fine for a
  column that's genuinely a BLOB/image/file.
- A disabled or deleted source's already-open connection pool keeps being
  reused by the engine until the process restarts (`Engine.sourceFor` in
  `internal/etl/engine.go`) — disabling the *job* is what actually stops
  it from running, not disabling the source out from under a still-enabled
  job.
