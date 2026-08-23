-- Converts app.zeus_sales from a plain table into a TimescaleDB hypertable,
-- partitioned on billingperiod_date. Confirmed before writing this:
--   * `SELECT * FROM timescaledb_information.hypertables WHERE
--     hypertable_name = 'zeus_sales';` returns no rows today — this table
--     has never actually been a hypertable, despite comments in
--     internal/zeusbilling/{model,service}.go asserting otherwise (fixed in
--     the same change that adds this file).
--   * `timescaledb` is present in this database's available extensions
--     (confirmed via pg_available_extensions), so this is achievable here —
--     it is NOT available on standard/managed Supabase; only on
--     self-hosted/Timescale-Cloud-backed Postgres images.
--
-- Why 1-month chunks: billingperiod_date is always a first-of-month DATE,
-- derived from billingyear/billingmonth by an existing DB trigger (see the
-- comment on BillingPeriodDate in internal/zeusbilling/model.go) — every
-- row in a given calendar month shares the exact same value. A 1-month
-- chunk_time_interval means one chunk per billing month, which exactly
-- matches how every caller already queries this table: base() in
-- service.go always filters billingperiod_date as a
-- [start-of-month, start-of-next-month) range (see billingPeriodDateBounds).
-- That means most queries will touch whole chunks, not fragments of them —
-- close to the ideal case for chunk exclusion.
--
-- Why this table is a clean fit for conversion:
--   * No PRIMARY KEY. The only UNIQUE constraint
--     (uq_zeus_sales_id on (_id, billingperiod_date)) already includes the
--     partitioning column, which TimescaleDB requires — no constraint
--     rework needed.
--   * All existing indexes (idx_zeus_sales_*, including the covering
--     idx_zeus_sales_map_aggregate added for the map's aggregate query)
--     carry over automatically: TimescaleDB applies a hypertable's index
--     definitions to every chunk, current and future. Nothing needs to be
--     recreated.
--   * The existing "kept in sync by a DB trigger" trigger that derives
--     billingperiod_date on insert/update is a plain row-level trigger and
--     keeps working unchanged on a hypertable.
--   * Nothing in the Go query layer (internal/zeusbilling) needs to
--     change — hypertables are queried with ordinary SQL, so
--     SELECT/INSERT/UPDATE against app.zeus_sales behave identically
--     before and after this migration.
--
-- ── Before running this ──────────────────────────────────────────────────
-- This physically re-partitions all existing rows (currently ~18M) into
-- per-month chunk tables. That is not instant and holds locks while it
-- runs — do this in a low-traffic window, not against a live table
-- mid-business-day. Take a fresh backup/snapshot immediately before
-- running it regardless of how confident you are — this is the same table
-- that prompted the "did I just drop this?" scare earlier, so don't skip
-- this step. Run each numbered block separately and confirm its output
-- before moving to the next one; don't paste the whole file at once.

-- 1) Enable the extension (idempotent).
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- 2) Pre-migration sanity check — note this number down to compare against
--    step 4's count after the conversion.
SELECT count(*) AS row_count_before FROM app.zeus_sales;

-- 3) The conversion itself. migrate_data => true moves the existing rows
--    into chunks as part of this call — this is the step that takes real
--    time on 18M rows.
SELECT create_hypertable(
    'app.zeus_sales',
    'billingperiod_date',
    chunk_time_interval => INTERVAL '1 month',
    migrate_data => true
);

-- 4) Post-migration verification.
--    a) Row count must match step 2 exactly.
SELECT count(*) AS row_count_after FROM app.zeus_sales;

--    b) Confirms the table is now a hypertable, and shows the chunk column.
SELECT hypertable_name, num_dimensions
FROM timescaledb_information.hypertables
WHERE hypertable_name = 'zeus_sales';

--    c) One chunk per calendar month is expected — spot-check the count
--       looks sane for your data's date range (e.g. ~18-24 chunks for
--       1.5-2 years of monthly billing data).
SELECT chunk_name, range_start, range_end
FROM timescaledb_information.chunks
WHERE hypertable_name = 'zeus_sales'
ORDER BY range_start;

--    d) Confirms a range-filtered query actually excludes chunks outside
--       the range (look for "Chunks excluded during startup" or a small
--       "Chunks" count in the plan, not a scan touching every chunk).
EXPLAIN (ANALYZE, BUFFERS)
SELECT metermodeltype, regionname, sum(billconsumptionvalue)
FROM app.zeus_sales
WHERE billingperiod_date >= date_trunc('month', now() - interval '1 month')
  AND billingperiod_date < date_trunc('month', now() + interval '1 month')
GROUP BY metermodeltype, regionname;
