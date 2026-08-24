-- app.zeus_sales's region-filtered Aggregate/Detail calls (region-detail
-- page, district-detail page, customer-sales pages — the majority of
-- zeus-billing/aggregate callers, as opposed to the map's one unfiltered
-- call handled by idx_zeus_sales_map_aggregate) were observed taking
-- 34-36s on a single-region, single-month, metermodeltype-filtered query —
-- live log:
--   SELECT ... FROM app.zeus_sales
--   WHERE (lower(regionname) IN ('accra east'))
--     AND (lower(metermodeltype) IN ('postpaid', 'prepaid'))
--     AND (billingperiod_date >= '2026-01-01' AND billingperiod_date < '2026-02-01')
--   GROUP BY districtname, metermodeltype
--
-- Same root cause already diagnosed once on this table's predecessor (see
-- the comment on idx_csz_region_lastbilldate in
-- sql/indexes_customer_sales_zeus.sql, for the now-deprecated
-- app.customer_sales_zeus): filtering on region + a date range together,
-- with no index covering both, forces the planner into either a lossy
-- bitmap AND across separate single-purpose indexes or a straight seq
-- scan. Confirmed no existing index on this table pairs lower(regionname)
-- with billingperiod_date — the closest, idx_zeus_sales_lower_region_period,
-- pairs lower(regionname) with billingyear/billingmonth (not
-- billingperiod_date, what base() actually filters on), and
-- idx_zeus_sales_metermodeltype_billingperiod pairs billingperiod_date
-- with lower(metermodeltype), which is low-cardinality (postpaid/
-- prepaid/amr) and barely narrows anything on its own.
--
-- regionname leads (not metermodeltype) because it's far more selective —
-- one of ~16 Ghana regions vs. one of 2-3 meter model types. Covering
-- (INCLUDE) the columns both concurrent Aggregate() queries actually read
-- (the sums query's SELECT list, and distinctCustomerCounts' accountcode/
-- servicepointcode/districtname) so this can run as an Index Only Scan
-- once region+date have narrowed the range, same principle as
-- idx_zeus_sales_map_aggregate.
--
-- On a large live table, prefer CREATE INDEX CONCURRENTLY (cannot run
-- inside a transaction, so run this statement by itself if the table has
-- live traffic):
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_zeus_sales_region_billingperiod
--       ON app.zeus_sales (lower(regionname), billingperiod_date)
--       INCLUDE (metermodeltype, districtname, accountcode, servicepointcode,
--                 billamount, amountdue, debtamount, outstandingamount,
--                 billconsumptionvalue, paymentsamount);

CREATE INDEX IF NOT EXISTS idx_zeus_sales_region_billingperiod
    ON app.zeus_sales (lower(regionname), billingperiod_date)
    INCLUDE (metermodeltype, districtname, accountcode, servicepointcode,
              billamount, amountdue, debtamount, outstandingamount,
              billconsumptionvalue, paymentsamount);
