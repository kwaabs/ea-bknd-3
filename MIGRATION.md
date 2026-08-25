# Restructure: layered spaghetti → domain packages

## Target layout

```
internal/
  httpx/          # JSON writing + query parsing (pagination, CSV, dates) — shared
  dbx/            # generic Paginate[T], reusable filter builders — shared
  cache/          # unchanged
  config/         # unchanged
  auth/           # unchanged (already domain-shaped)
  middleware/     # unchanged
  mmssales/       # ← migrated in this delivery (the template)
    model.go      #   row type, filter params, aggregate types — nothing else
    service.go    #   base() filter builder + Detail/Aggregate
    handler.go    #   parseFilters once, thin endpoints
    routes.go     #   Routes(db, log, mw...) chi.Router
  zeussales/      # migrate next, copy the mmssales shape
  amrcustomer/    # then this (largest — do it after two smaller ones)
  meters/         # the big one; split into meters/, consumption/, spatial/ if it helps
  feeders/
  feedback/
  comments/
  serviceareas/
routes/route.go   # shrinks to mounts only
```

Rules that keep it manageable:
- A domain package owns everything about its endpoints. Adding a feature = one new package + one `Mount` line.
- `models/`, `services/`, `handlers/` stop growing. Migrate out of them one domain at a time; delete each file as its domain package goes live.
- Cross-domain sharing goes in `httpx`/`dbx` only if at least two domains need it. Resist premature generalization.

## route.go after migration (shape)

```go
r.Route("/api/v1", func(r chi.Router) {
    r.Mount("/auth", auth.Routes(db, jwtMgr, cfg, logr))
    r.Mount("/meters/consumption/mms-customer-sales", mmssales.Routes(db, logr.Logger, cacheMW))
    r.Mount("/meters/consumption/customer-sales-zeus", zeussales.Routes(db, logr.Logger, cacheMW))
    r.Mount("/amr", amrcustomer.Routes(db, logr.Logger))
    r.Mount("/feeders", feeders.Routes(db, logr.Logger))
    // ...
})
```

During migration, old and new registrations coexist — chi will happily serve
legacy routes from the old handlers while migrated domains are mounted. Just
make sure a path is registered in exactly one place at a time.

## Incremental steps (safe order)

1. Add `internal/httpx` and `internal/dbx` (no behavior change, nothing depends on them yet).
2. Drop in `internal/mmssales`, change the two mms routes in route.go to a single `Mount`, delete the old mms handler/service files and the mms types from `models/`. Verify responses are byte-identical (they are, JSON tags unchanged) — `data: []` now instead of `data: null` on empty pages, which is almost certainly what your frontend wants anyway.
3. Run `sql/indexes_mms_customer_sales.sql` (safe, `IF NOT EXISTS`, but note: on a large live table prefer `CREATE INDEX CONCURRENTLY`, which cannot run inside a transaction).
4. Migrate zeussales the same way (it's near-identical to mmssales).
5. Migrate the remaining domains one per PR. `meters` last — it's the biggest and you'll be fluent in the pattern by then.
6. When `handlers/`, `services/`, `models/` are empty, delete them.

## Where efficiency improved (not just preserved)

1. **Concurrent count + scan.** The old pattern ran `COUNT(*)` then the data
   query sequentially: latency = count + scan. `dbx.Paginate` uses bun's
   `ScanAndCount`, which executes both concurrently on the pool:
   latency = max(count, scan). On heavy filtered queries this is close to a
   2x latency cut for the endpoint, and it also builds the filter set once
   instead of twice.
2. **Functional + trigram indexes.** Every `lower(col) IN (...)` filter was a
   guaranteed sequential scan without `lower(col)` indexes. The SQL file makes
   all of them index-driven. The 4-column `%LIKE%` search gets pg_trgm GIN
   indexes — the difference between milliseconds and full-table scans as the
   table grows.
3. **Composite index matching the stable sort** lets Postgres paginate
   without a sort node.
4. **Zero-cost abstraction.** `httpx`/`dbx` helpers are thin functions and a
   generic instantiated at compile time — no reflection, no interfaces on the
   hot path, no extra allocations beyond what the old code did.

One behavior note: internal errors now return `{"error":"internal error"}`
instead of leaking `err.Error()` (raw SQL/driver errors) to clients — the
full error still goes to the zap log. Keep that; it's both safer and cleaner.

## Optional next steps

- A `Deps` struct (`db, cfg, log, cache, cacheMW, jwtMgr`) passed to each
  `Routes()` if the argument lists start feeling repetitive.
- If zeussales/mms/amr filter param parsing turns out near-identical, a shared
  `dbx.InFilterSet` (map of column → values) can shrink `base()` further —
  but only do it once you see the third copy.

## Aggregate fast path: incremental daily summary (added)

Aggregates are NOT paginated and never need to be: group-by dimensions are
low-cardinality, so responses stay small. The cost was scanning raw rows at
request time. That is gone:

- `sql/summary_mms_customer_sales.sql` creates `app.mms_sales_daily_summary`
  (grain: day × all six dimensions) plus `app.resync_mms_sales_summary(from, to)`.
- The ingestion process (single writer) calls, per batch, AFTER its raw
  deletes+inserts and BEFORE cache invalidation:

      SELECT app.resync_mms_sales_summary(:from_day, :to_day);

  [:from_day, :to_day] = union of dates deleted and dates inserted. The
  delete-before-replace pattern is covered: days emptied by a delete fall
  inside the range and their summary rows are removed. Cost scales with the
  batch, not the table. Run the commented backfill once to seed history.
- `Service.Aggregate` routes automatically: dimension/date filters only →
  summary (milliseconds, any table size); search/accountNumber/meterNumber
  present → raw-table fallback (index-assisted, rare).
- `parseFilters` now normalizes dateTo to end-of-day. The old code compared
  `date_time <= midnight`, excluding nearly all of the end day — this fixes
  that and keeps both aggregate paths numerically identical.

Ingestion sequence per batch:
  raw deletes → raw inserts → resync_mms_sales_summary(range) → DeleteByPrefix.

## Aggregate fast path for zeus_sales: summary + customer roster (added)

`internal/zeusbilling` (app.zeus_sales, 18M+ rows) got the same treatment,
with one structural difference from the MMS pattern above — see
`sql/summary_zeus_sales.sql` for the full rationale:

- `app.zeus_sales_period_summary` — grain: billingperiod_date (month, not
  day — zeus_sales's real grain) × the 9 dimension columns Aggregate can
  group by. Accelerates the SUM columns exactly like the MMS summary does.
- `app.zeus_customer_roster` — grain: one row per distinct (accountcode,
  servicepointcode), storing their MOST RECENT dimension values and their
  [first, last] active billingperiod_date. Exists because
  `customer_count` is a distinct-account count, not a row count — a
  period-grain summary can't re-aggregate that correctly across a
  multi-month range (a customer billed in 3 of 6 requested months would
  get counted 3 times). This is a real gap the MMS pattern still has
  (mmssales' own fast path runs its distinct count against the raw table,
  per its own comment) that zeus_sales needed closed, since the reported
  failure (a 6-month, lightly-filtered customer-sales query not loading)
  was specifically a distinct-count-heavy case.
- `Service.Aggregate` in `internal/zeusbilling/service.go` routes sums and
  customer_count independently: sums use the raw table only when a
  row-level filter is present (search / account / service point / meter
  code / last-payment or created-at range / exact billingyear-or-month);
  customer_count additionally falls back to the raw table when grouping by
  billingyear/billingmonth, since the roster's single [first, last] window
  per customer can't answer a period-scoped distinct-count.
- Ingestion contract is the same shape as MMS's, with two functions to
  call instead of one: after a batch's raw deletes+inserts and before
  cache invalidation,

      SELECT app.resync_zeus_sales_period_summary(:from_date, :to_date);
      SELECT app.resync_zeus_customer_roster(:from_date, :to_date);

  [:from_date, :to_date] = union of billingperiod_date values touched by
  the batch (same contract as MMS). The roster function is more expensive
  per call than the summary one — it re-scans each touched customer's
  *entire* history, not just the batch's date range, because a customer's
  first/last active period can extend outside the touched window. Cost
  scales with (batch size × average rows per touched customer), not table
  size.
- Run the one-time backfill (commented at the bottom of
  `sql/summary_zeus_sales.sql`) once after creating these — it alone fixes
  a currently-slow wide-range query immediately, independent of whether
  the ingestion process has been wired to call the two resync functions
  per batch yet. Wiring that ongoing sync is out of scope for this repo:
  the Zeus ingestion writer is an external process, same as MMS's.
