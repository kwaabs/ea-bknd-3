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

## MMS re-sync duplicate rows (added)

Observed live: the ingestion process periodically re-writes the SAME
(meter_number, sts_credit_balance_remaining, sts_last_month_credit_read,
sts_last_month_kwh_read) reading under a new date_time — e.g. identical
values for one meter 2 days apart, then genuinely different values a month
later. Every MMS query sums these figures across whatever days fall in the
requested range, so an unflagged duplicate inflates any chart/KPI covering
more than one of its re-sync dates.

- `sql/mms_customer_sales_dedup.sql` adds `is_duplicate_reading boolean` to
  `app.mms_customer_sales` and `app.resync_mms_duplicate_flags(from, to)`,
  which flags every row in a (meter_number, calendar month, 3 reading
  values) group except the one with the latest date_time. Scoped to
  calendar month deliberately — the same reading recurring in two
  DIFFERENT months (e.g. a vacant meter reading 0 kWh twice) is real data,
  not a duplicate, and must not be collapsed.
- `Service.base` (Detail, and Aggregate's raw fallback) now excludes
  `is_duplicate_reading` rows unconditionally. `resync_mms_sales_summary`
  (replaced in the same file) excludes them when building the daily
  summary too, so the fast aggregate path never sums an inflated figure.
- Ingestion sequence per batch, extending the sequence above:
  raw deletes → raw inserts
    → resync_mms_duplicate_flags(range)   [NEW, must run first]
    → resync_mms_sales_summary(range)      [now duplicate-excluding]
    → DeleteByPrefix
- **Deployment order matters here**: `Service.base` unconditionally
  references the new `is_duplicate_reading` column, so
  `sql/mms_customer_sales_dedup.sql` must be applied BEFORE this backend
  build is deployed — deploying the code first means every MMS request
  fails until the column exists.

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

## zeus-billing/detail sort index (added)

The fast path above only covers `Service.Aggregate` (grouped sums/counts).
`Service.Detail` — the paginated Customer Records row listing — always
scans `app.zeus_sales` directly via `base(p)` and was never given a fast
path, so a wide, region-unfiltered `Detail` call still forces a full sort
of every matching row before returning one page. Reported slow:
a `meterModelType=Postpaid`, 6-month range, `sortBy=billconsumptionvalue
desc` call — which isn't an edge case, since that's
`customer-sales-detail.tsx`'s default sort with no region selected, i.e.
what runs on every first load of the Postpaid/Prepaid tab.

`sql/indexes_zeus_sales_detail_sort.sql` adds
`idx_zeus_sales_metermodeltype_consumption` on
`(lower(metermodeltype), billconsumptionvalue DESC NULLS LAST, accountcode,
servicepointcode)` — deliberately without `billingperiod_date`, so
Postgres can walk the index already in the requested sort order and stop
at `LIMIT`, filtering out-of-range rows as it goes, instead of sorting the
whole matching set first. See that file's header for the full reasoning,
including why the date range isn't part of the index and the tradeoff that
implies for narrow date ranges. Only `billconsumptionvalue` (the reported
and default case) is indexed; `detailSortCols`' other sort columns
(`createdat`, `billamount`, `amountdue`, `debtamount`, `customername`) can
get the same treatment if/when they're reported slow too — not added
speculatively here.

## Daily forced logout (added)

`internal/scheduler/session_reset.go` starts a goroutine in `cmd/server/main.go`
that force-logs-out every user once a day at 00:00 UTC (== 00:00 in Ghana,
no DST), calling the new `AuthService.ResetAllSessions`
(`internal/services/auth.go`). No DB function or schema change was needed —
the existing auth design already made this possible with two statements:

- `UPDATE app.users SET token_version = token_version + 1` — every
  currently-issued access token is checked against this column on every
  request (`middleware.JWTAuth` → `CheckTokenVersion`), so a mismatch is an
  instant 401 on the very next request after the bump, regardless of the
  token's own expiry.
- `UPDATE app.refresh_tokens SET revoked = true WHERE revoked = false` —
  required *in addition to* the bump above. `AuthService.Refresh` does not
  compare the refresh token's embedded `ver` claim against the user's
  current `token_version`; it only checks the `refresh_tokens` row
  (jti + hash + not revoked + not expired) and then mints a fresh pair
  using the user's *current* token_version. Without this second statement,
  a client silently retrying `/refresh` after a 401 would get handed new,
  valid tokens and the reset would have no visible effect.

Both statements run in one transaction. The job is a plain in-process timer
(`time.Until` next UTC midnight, `time.NewTimer`, loop) — no new dependency
(e.g. a cron-expression library) since this is a single fixed daily time,
and no pg_cron dependency either, so it doesn't need that Supabase
extension enabled. Safe to run on more than one server instance
simultaneously: both UPDATEs are effectively idempotent (a redundant
token_version bump or an already-revoked refresh_tokens row is harmless),
so no distributed lock was added.

## zeus-billing region filter normalization (added)

`app.zeus_sales.regionname` always stores the administrative-unit-qualified
form ("Tema Region", "Accra East Region") — confirmed against live data:
`region=Tema` returns nothing, `region=Tema Region` returns real rows. Every
other source of a region value in this system (the meter table, the
frontend's region-select options, callers sending the short form) uses the
unqualified form ("Tema", "Accra East"), so an unqualified filter value
silently matched zero rows.

`parseFilters` in `internal/zeusbilling/handler.go` now runs every incoming
`region` value through `normalizeZeusRegionNames` (`internal/zeusbilling/service.go`)
before it becomes part of `FilterParams` — appends " Region" to any value
that doesn't already end with it (case-insensitively), so it's a no-op on a
value that's already correctly qualified. This is the single point where
query params become `FilterParams` (see `parseFilters`'s own doc comment),
so both `base()`'s and `dimensionFilters()`'s `regionname` filters get the
normalized value automatically — no separate fix needed at either call site.

Frontend pages that resolve a region against Zeus's own known region-name
list first (`useResolvedRegionName`, e.g. region-detail.tsx, district-detail.tsx)
already send the correctly-qualified form and are unaffected either way —
this is now normalized on the server regardless of what a caller sends, so
those pages don't strictly need to keep doing that resolution for Zeus
specifically, though there's no harm in leaving it (MMS's own regionname
convention is unrelated and still needs it independently).

`internal/zeussales` (targets the older, deprecated `app.customer_sales_zeus`
table, still mounted at `/customer-sales-zeus`) has the identical
`regionname` filter pattern but was deliberately left untouched — it's not
the endpoint in active use.

## New source: bot_consumption (added)

`internal/botconsumption` is a new, independent domain package for
`app.bot_consumption` — a bot-ingested customer consumption source with no
shared keys or relationship to Zeus/MMS/AMR. Mounted at
`/meters/consumption/bot-consumption` (`detail`, `aggregate`), same shape as
`mmssales`/`zeusbilling`: `FilterParams` → `base()` → `Detail`/`Aggregate`.

Two things worth knowing about this table specifically:

- `region` is declared `bpchar(15)`, so Postgres stores and returns it
  blank-padded to the full column width (e.g. `"ACCRA WEST     "`).
  `trim(region)` is used everywhere the column is filtered or grouped by,
  so callers never see or have to match the padding — the same class of
  region-naming mismatch this session hit repeatedly with Zeus/MMS, just
  caused by the column type here instead of the source data.
- There's no real date column — `billmonth` is a free-text label
  (`"june-2026"`), not a date/timestamp. `Detail`/`Aggregate` still accept
  the app-wide `dateFrom`/`dateTo` params for consistency with every other
  page's date picker, but `Service.resolveDateRangeToBillMonths` treats
  them as **month-precision only**: it fetches the table's distinct
  `billmonth` labels, parses each ("monthname-year", case-insensitive) via
  `parseBillMonth`, and keeps whichever whole months the requested range
  touches — so `dateFrom=2026-06-15` behaves identically to
  `dateFrom=2026-06-01`. A malformed label is skipped rather than failing
  the request. An explicit `billMonth` param always takes precedence over
  `dateFrom`/`dateTo` if both are given.

  **Bug fixed after initial deploy**: a date range that overlapped zero
  billmonth labels (e.g. filtering Jan-Apr 2026 when the table only has
  June 2026 data) returned *every* row instead of none. Cause:
  `resolveDateRangeToBillMonths` set `p.BillMonth` to an empty slice when
  nothing matched, but `dbx.InLower` treats an empty slice as "no filter
  requested" (correct for the normal, no-filter case) — so the date filter
  silently vanished. Fixed by having the resolver return a `noMatch bool`
  alongside `FilterParams`; `Detail`/`Aggregate` check it and return an
  empty result directly, without querying `base()` at all when it's true.

No summary/fast-path table yet (this source starts small) and no indexes
added — both are the kind of thing this session only added in response to a
confirmed slow query on the other sources, not proactively at
table-introduction time. Revisit once real data volume and query patterns
are known.

## New source: bxc_consumption (added)

`internal/bxcconsumption` is a second bot-ingested legacy source, mounted
at `/meters/consumption/bxc-consumption`, structurally identical to
`botconsumption` above (same 8 columns, same `resolveDateRangeToBillMonths`
month-precision date handling, same `noMatch` fix included from the
start). One real difference: `region` here is plain `varchar(10)`, not
`bpchar(15)`, so no `trim()` is needed anywhere it's filtered, grouped, or
returned.

**Flagging a real risk in both `bot_consumption.billmonth` and
`bxc_consumption.billmonth`**, not something introduced or fixed here:
both are declared `bpchar(9)`, which only fits `"<month>-YYYY"` for
May/June/July (8-9 characters). Every other month name overflows it and
gets silently truncated by Postgres — e.g. `"september-2026"` (14 chars)
becomes `"september"` with the hyphen and year cut off entirely, which
`parseBillMonth` then correctly (but silently) fails to parse as a date,
excluding it from any `dateFrom`/`dateTo`-filtered query. It would still
appear in `Detail`/`Aggregate` results when no date filter is applied, and
in a raw `billMonth` value list, just not resolvable by date. Both sample
payloads seen so far only used May/June/July, so this hasn't surfaced yet
— it's a ticking time bomb the moment another month's data lands, and the
only real fix is widening the source column (or changing it to a proper
date/varchar-without-length-limit type), which is outside this repo's
control since these are externally-ingested tables.

## New package: internal/salessummary — cross-source totals (added)

Root cause being fixed: "what is the total Prepaid (or Postpaid) figure
across every source" was computed independently, by hand, in five-plus
separate frontend files (dashboard's Sales card, customer-sales-overview,
region-detail-marquee, energy-flow-diagram, choropleth-map), each
re-deriving its own copy of "Zeus (deduped) + MMS". When BOT and then BXC
were added as new sources, they were folded into `customer-sales-overview`
correctly but the same fix was initially missed in the other four files —
a mistake, not a one-off, because the totals logic had no single home.

`internal/salessummary` (mounted at
`/meters/consumption/customer-sales-summary`) is that single home. It
calls each source's own `Service.Aggregate` directly — in-process Go
function calls in the same binary, not HTTP calls to their own endpoints
— and merges the results server-side into one cross-source total, grouped
by region or district. `GET ?category=prepaid|postpaid&dateFrom=...&
dateTo=...&groupBy=region|district&region=...&district=...`.

Two things are centralized here that used to be re-implemented per file:

- **Which sources belong to which category, and the one real dedup rule.**
  `sourcesFor(category)` in service.go is now the only place a new source
  gets registered. Confirmed business rule: MMS takes precedence over Zeus
  Prepaid on any meter it already has (the pre-existing
  `excludeMmsPrepaidDuplicates` rule, the one *real* overlap in this
  system) — every other pairing (BOT vs Zeus/MMS, BXC vs Zeus/MMS, BOT vs
  BXC, and any future legacy source against any other) is confirmed
  genuinely unique with no overlap, so those sum in directly with no
  precedence logic at all. Postpaid keeps Zeus Postpaid and Zeus AMR as
  two separate `BySource` entries rather than merging them, since existing
  callers show them as separate KPI badges — a caller that wants one
  combined Postpaid number just sums both entries itself.
- **Region/district name normalization across sources.** Zeus stores
  suffixed names ("Tema Region", "Cape Coast District"); MMS/BOT/BXC store
  the short form ("Tema", "Cape Coast"), and casing isn't guaranteed
  consistent either. `normalizeGroupKey` (a Go port of the frontend's
  `normalizeRegionName`/`shortRegionLabel` in
  `use-resolved-region-name.ts`) strips a trailing "Region"/"District" and
  lowercases before using the result as the merge key, so the same real
  region reported four different ways by four sources lands in one `Row`.
  The row's own displayed `GroupValue` keeps the short, unsuffixed form.

Sources are fetched concurrently (one goroutine per source, results merged
off a channel) since they're independent queries against different
tables/services.

Verified via a standalone script exercising `normalizeGroupKey`/the merge
logic against a hand-built scenario (4 sources reporting "Accra East"
under 4 different spellings/casings correctly collapsing into one row with
correct summed kwh/customers and correct per-source breakdown; a
single-source region staying separate; edge cases like a bare "Region"
input, leading/trailing whitespace, and empty input) — no live DB access
from this sandbox, same constraint as every other verification this
session.

**Not yet done**: no frontend consumer has been migrated to call this
endpoint yet. `overview-main-tab-v3.tsx`, `region-detail-marquee.tsx`,
`energy-flow-diagram.tsx`, and `choropleth-map.tsx` still compute their
own stale per-source totals and need to be migrated to call this endpoint
instead — that's the next piece of work, not part of this commit.

## New source: pns_consumption (added)

`internal/pnsconsumption` is a third legacy consumption source, mounted at
`/meters/consumption/pns-consumption`, independent of Zeus/MMS/AMR/BOT/BXC
(no shared keys with any of them). The main consumption figure is the
`energy` column specifically — **not** `totalenergy`, which folds in
service/demand charges on top of energy and isn't a consumption number.

Two things set this source apart from bot_consumption/bxc_consumption:

- **A real `billdate` timestamp column.** Unlike bot/bxc (free-text
  `billmonth` label only, requiring `resolveDateRangeToBillMonths` to parse
  and month-match it against a date range), pns_consumption has an actual
  timestamp — `dateFrom`/`dateTo` filter directly against it via
  `dbx.DateRange`, a plain SQL range, no label-parsing machinery needed.
  `billmonth` (`bpchar(6)`, `"YYYYMM"` e.g. `"201809"`) is still stored and
  returned/filterable by exact value, just not used for date-range
  matching.
- **No region/district name anywhere.** `regionid`/`districtid` are opaque
  codes (e.g. `"10001001"`, `"10011060"`) — confirmed via a full-repo
  search that no lookup table or mapping from these codes to human-readable
  region/district names exists anywhere in this codebase today (every
  other source stores a name directly). `Reading`/`AggregateRow` expose the
  raw codes as-is for now; the DBA is expected to provide a proper mapping
  later. When that lands, resolving it only needs a JOIN added to
  `base()` plus new Name fields — not a reshape of this package. Until
  then, any frontend surface built on this source will show codes, not
  region/district names.

`region`/`district`/`tariff` are accepted as query-param/groupBy aliases
for `regionid`/`districtid`/`tariffcategory` (mapped in `groupExpr` and
`parseFilters`) so callers use the same param names as every other source,
even though the values themselves are codes here. No summary/fast-path
table yet — same "add only once a real slow query shows up" approach as
bot/bxc_consumption.

**Indexes added** (`sql/indexes_pns_consumption.sql`, not yet run against
the database — apply manually) once the real row count came back at 7M+:
functional `lower(...)` indexes on `regionid`/`districtid`/
`tariffcategory`/`billmonth` (every filter on these goes through
`dbx.InLower`, which a plain index can't serve), a plain index on
`serviceid` (exact match via `dbx.In`), an index on `billdate` (the real
date-range filter), `pg_trgm` GIN indexes on `customerid`/`serviceid`/
`servicepoint` for the `%search%` LIKE, and a composite
`(regionid, districtid, customerid)` index matching `/detail`'s default
sort. Same shape as `sql/indexes_mms_customer_sales.sql`; unlike bot/bxc
(which start small), pns_consumption needed this from the start given its
size.

## Resumable app.migrate_zeus_sales_from_working (added)

`sql/migrate_zeus_sales_from_working_resumable.sql` — not tied to any Go
application code, this is a pure operational DB script (run manually
against the database, same as every other `sql/*.sql` file here).

The existing `app.migrate_zeus_sales_from_working` stored procedure
batch-migrates `app.zeus_sales_working` (18.8M+ rows) into `app.zeus_sales`
in committed chunks, but tracked its cursor as a local `plpgsql` variable
only — so every fresh `CALL` (including one resuming after an interruption)
restarted from row 0 and re-scanned the entire source table. Re-running was
always safe (`INSERT ... ON CONFLICT DO UPDATE`, never `DELETE` — no risk
of data loss or duplication), just wasteful.

This file adds `app.migration_checkpoints` (one row per procedure,
`last_id` + `updated_at`) and replaces the procedure with a version that
reads the checkpoint at the start and writes it back atomically with each
batch — in the same transaction, right before that batch's `COMMIT` — so a
plain `CALL app.migrate_zeus_sales_from_working();` with no arguments now
resumes exactly where the last run stopped. A new `p_reset boolean`
parameter (default `false`) forces a full re-run from the beginning when
that's actually wanted.

The cursor stays `id`-based (an arbitrary surrogate key on
`zeus_sales_working`, not a date) — jumping to a specific date manually is
possible by writing the corresponding `MAX(id)` into the checkpoint row
directly, but that's a deliberate skip-ahead, not the normal resume path.

**Bug fixed after first real run**: `ERROR: ON CONFLICT DO UPDATE command
cannot affect row a second time`. Cause: `zeus_sales_working` can have more
than one row for the same `(_id, billing month)` — a bill re-staged after
a status/amount change lands as a new row with a new working-table `id`,
same `_id`. A single `INSERT ... ON CONFLICT DO UPDATE` command can only
touch a given target row once; two source rows resolving to the same
conflict key within one batch's `SELECT` broke that. Fixed by wrapping the
batch's source query in `SELECT DISTINCT ON (_id, billingperiod_date) ...
ORDER BY _id, billingperiod_date, id DESC`, keeping only the
highest-working-table-id (most recently staged) row per key before it
reaches `INSERT`. This only dedupes within one batch — a row a *previous*
batch already inserted is still correctly updated by a later batch's own
`ON CONFLICT`, since that's a separate statement/transaction.
