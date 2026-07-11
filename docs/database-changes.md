# Database changes made during the bknd-3 migration/testing session

This documents every schema change (new table, index, function) applied to
the local `ea-5` Postgres database while migrating and testing bknd-3. Each
change is idempotent (`IF NOT EXISTS`) and has a corresponding `.sql` file in
[`sql/`](../sql) so it can be replayed against another environment.

## 1. `app.mms_sales_daily_summary` — pre-aggregated summary table

**File:** [`sql/summary_mms_customer_sales.sql`](../sql/summary_mms_customer_sales.sql)

**What it is:** a daily rollup of `app.mms_customer_sales`, grain = one row
per `(day × region × district × contract_type × tariff × manufacturer ×
model)`, with pre-computed `customer_count` and sum columns.

**Why:** `mmssales.Aggregate()` serves the common case (dimension/date
filters only) from this table instead of scanning the raw table — SUM over a
few thousand summary rows instead of every raw row in range.

**Includes:**
- `app.mms_sales_daily_summary` table
- `idx_mms_sales_summary_day` index on `(day)`
- `app.resync_mms_sales_summary(p_from date, p_to date)` function — deletes
  and rebuilds the summary for a date range. The ingestion pipeline is
  expected to call this after every raw insert/delete batch, covering the
  union of dates touched (see comments in the SQL file for the exact
  contract).

**Status:** table created and **backfilled** for all existing data
(`resync_mms_sales_summary` run once over the full min/max date range of
`app.mms_customer_sales`). No ongoing ingestion pipeline currently calls it
on new writes — that wiring is still needed wherever MMS data is loaded, or
the summary will drift stale as new rows land.

## 2. `app.idx_csz_region_lastbilldate` — composite index on `customer_sales_zeus`

**File:** [`sql/indexes_customer_sales_zeus.sql`](../sql/indexes_customer_sales_zeus.sql)

**What it is:** `CREATE INDEX ON app.customer_sales_zeus (lower(regionname), lastbilldate)`.

**Why:** `zeussales.Aggregate()` filters on region + a `lastbilldate` range
together, but the table (15M+ rows) only had single-column indexes on each.
Postgres combined them via a bitmap AND that went lossy (page-granularity
only) once the intersection exceeded `work_mem`, forcing a full-predicate
recheck on every row in each flagged page — measured at 30-40s for a region
+ multi-month range.

**Honest caveat:** this index exists and doesn't hurt, but in testing the
planner did **not** end up using it for the query that motivated it — it
kept choosing a parallel sequential scan or an existing single-column index
instead, even when other plans were disabled. The performance fix that
actually worked was on the Go side: rewriting the `customer_count`
computation from `COUNT(DISTINCT (a, b))` (forces a full sort) to a
two-level `GROUP BY` (hash aggregate), and running it concurrently with the
sums query. See `internal/zeussales/service.go`. If Zeus aggregate queries
are still slow later, this index is a reasonable first thing to
re-investigate (check `EXPLAIN ANALYZE` again — the planner's choice may
change as data volume or Postgres version changes), but don't assume it's
doing anything today.

## Not yet done

- No summary table exists for Zeus (unlike MMS). If `customer_sales_zeus`
  aggregate queries need to be faster than ~15-20s, the real fix is
  probably a daily/monthly pre-aggregated summary table mirroring
  `mms_sales_daily_summary`, not more indexing on the 15M-row raw table.
- The MMS summary table has no automated refresh hooked into ingestion yet
  (see caveat above).
