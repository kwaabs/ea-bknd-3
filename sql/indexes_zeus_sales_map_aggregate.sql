-- app.zeus_sales is an 18M-row table with no pre-aggregated summary table
-- for this domain (see the comment on Service.base in
-- internal/zeusbilling/service.go). Most Aggregate() callers filter by
-- region/district, which is already selective. The map's choropleth call
-- (choropleth-map.tsx) is the one caller that does not — it fetches ALL
-- regions at once, grouped by (metermodeltype, regionname), filtered only
-- by a billingperiod_date range. With no other filter, an aggregate over
-- that shape has to touch every row in the date window no matter what
-- index exists on billingperiod_date/metermodeltype/regionname alone — the
-- only real lever is avoiding the heap fetch per row entirely.
--
-- This covering index includes every column both concurrent queries in
-- Aggregate() actually read (the sums query's SELECT list, and the
-- distinctCustomerCounts subquery's accountcode/servicepointcode), so
-- Postgres can answer the whole date-range scan as an Index Only Scan
-- instead of a heap scan/random heap fetch per matching row — same
-- principle as the summary-table alternative, without the ETL/refresh
-- machinery. Trade-off: this is a wide index (irow ~= sum of all included
-- columns), so it costs meaningfully more disk and per-write overhead than
-- a narrow one — acceptable here because it directly targets the one slow,
-- unfiltered caller rather than being applied speculatively.
--
-- On a large live table, prefer running this with CREATE INDEX CONCURRENTLY
-- (cannot run inside a transaction, so run this statement by itself if the
-- table has live traffic):
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_zeus_sales_map_aggregate
--       ON app.zeus_sales (billingperiod_date, metermodeltype, regionname)
--       INCLUDE (accountcode, servicepointcode, billamount, amountdue,
--                 debtamount, outstandingamount, billconsumptionvalue,
--                 paymentsamount);

CREATE INDEX IF NOT EXISTS idx_zeus_sales_map_aggregate
    ON app.zeus_sales (billingperiod_date, metermodeltype, regionname)
    INCLUDE (accountcode, servicepointcode, billamount, amountdue,
              debtamount, outstandingamount, billconsumptionvalue,
              paymentsamount);
