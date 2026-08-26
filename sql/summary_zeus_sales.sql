-- Aggregate fast path for app.zeus_sales (18M+ rows and growing), mirroring
-- the app.mms_sales_daily_summary pattern (sql/summary_mms_customer_sales.sql,
-- MIGRATION.md "Aggregate fast path") that internal/zeusbilling/service.go's
-- own comment on Service.base already points at: "If Aggregate turns out
-- slow at scale, follow the app.mms_sales_daily_summary pattern... rather
-- than adding more indexes here." A wide, lightly-filtered date range (e.g.
-- a 6-month customer-sales query with no region narrowing it) has to touch
-- every matching row no matter what index exists — confirmed as the cause
-- of "1 Jan - 30 Jun doesn't load" even with the region+period covering
-- index and the hypertable conversion already applied, both of which only
-- help a query that's narrow in region and/or month count.
--
-- Two tables, not one — this is the one place this diverges from the MMS
-- pattern, and it's a real correctness issue, not a style choice:
--
--   1. app.zeus_sales_period_summary — grain: billingperiod_date (month,
--      not day — every zeus_sales row in a calendar month already shares
--      one billingperiod_date value, see the hypertable conversion's
--      comment) × the 9 dimension columns Aggregate can group by. Money
--      and consumption sums are safely re-aggregable: SUM(billamount)
--      across 6 months of summary rows equals SUM(billamount) across 6
--      months of raw rows, always.
--
--   2. app.zeus_customer_roster — grain: one row per distinct
--      (accountcode, servicepointcode), NOT per period. customer_count is
--      COUNT(DISTINCT (accountcode, servicepointcode)) in the existing raw
--      -table code (service.go's distinctCustomerCounts) specifically
--      because one account has a row per billing period — a customer
--      billed in 3 of the requested 6 months must count once, not three
--      times. A period-grain summary can't represent that: summing a
--      per-month customer_count across months double/triple-counts every
--      returning customer, which is *every* customer over a 6-month
--      window. (This is a real gap in the MMS pattern too — mmssales's own
--      fast path still runs distinctCustomerCounts against the RAW table,
--      per its own comment: "a true distinct count needs the raw table
--      instead." That was an acceptable trade there; for zeus_sales the
--      exact failure being fixed here is a distinct-count-heavy query, so
--      it needs its own fast path.) The roster stores each customer's
--      MOST RECENT dimension values (region, account type, etc. as of
--      their latest billing period) plus their first/last active
--      billingperiod_date. customer_count for a group+date-range query
--      becomes: rows in the roster whose [first, last] overlaps the
--      requested range, grouped and counted — one comparison per distinct
--      customer instead of one per bill.
--
--      Semantics note: this means a customer whose region changed
--      mid-window is now counted under their CURRENT region for the whole
--      window, not split across the periods they were in each region (the
--      raw-table computation's exact, period-by-period behavior). Confirmed
--      as the intended trade-off — "how many distinct customers are in
--      region X" is a more useful answer keyed to where they are now than
--      to a historical snapshot.
--
-- Prerequisite: a leading (accountcode, servicepointcode) index on the raw
-- table. Every existing zeus_sales index has these columns only as INCLUDE
-- (payload) columns on a different leading key, so a lookup keyed on
-- exactly these two columns — which the roster resync function needs, to
-- recompute one customer's full history when any of their rows change —
-- would otherwise be a sequential scan.
--
-- On a large live table, prefer CREATE INDEX CONCURRENTLY (cannot run
-- inside a transaction, so run this statement by itself if the table has
-- live traffic):
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_zeus_sales_account_servicepoint
--       ON app.zeus_sales (accountcode, servicepointcode);
CREATE INDEX IF NOT EXISTS idx_zeus_sales_account_servicepoint
    ON app.zeus_sales (accountcode, servicepointcode);

-- ---------------------------------------------------------------------------
-- 1) Period summary — accelerates the SUM columns.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS app.zeus_sales_period_summary (
    billingperiod_date date NOT NULL,
    regionname          text,
    districtname        text,
    tariffclasscode     text,
    tariffclassname     text,
    serviceclass         text,
    accounttype           text,
    billstatus             text,
    metermodeltype          text,
    servicepointstatus       text,
    sum_billamount            numeric NOT NULL DEFAULT 0,
    sum_amountdue             numeric NOT NULL DEFAULT 0,
    sum_debtamount            numeric NOT NULL DEFAULT 0,
    sum_outstandingamount     numeric NOT NULL DEFAULT 0,
    sum_billconsumptionvalue  numeric NOT NULL DEFAULT 0,
    sum_paymentsamount        numeric NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_zeus_sales_period_summary_date
    ON app.zeus_sales_period_summary (billingperiod_date);

-- resync_zeus_sales_period_summary: delete + rebuild the summary for a
-- [p_from, p_to] date range, atomically. Same ingestion contract as
-- resync_mms_sales_summary — call this, and resync_zeus_customer_roster
-- below, AFTER the batch's raw deletes+inserts and BEFORE cache
-- invalidation, with [p_from, p_to] = the union of dates touched by the
-- batch (deleted rows' dates AND inserted rows' dates). Bounds snap to
-- whole months since that's zeus_sales's real grain.
CREATE OR REPLACE FUNCTION app.resync_zeus_sales_period_summary(p_from date, p_to date)
RETURNS void
LANGUAGE sql
AS $$
    DELETE FROM app.zeus_sales_period_summary
    WHERE billingperiod_date >= date_trunc('month', p_from)
      AND billingperiod_date <  date_trunc('month', p_to) + interval '1 month';

    INSERT INTO app.zeus_sales_period_summary (
        billingperiod_date, regionname, districtname, tariffclasscode,
        tariffclassname, serviceclass, accounttype, billstatus,
        metermodeltype, servicepointstatus,
        sum_billamount, sum_amountdue, sum_debtamount, sum_outstandingamount,
        sum_billconsumptionvalue, sum_paymentsamount
    )
    SELECT
        billingperiod_date, regionname, districtname, tariffclasscode,
        tariffclassname, serviceclass, accounttype, billstatus,
        metermodeltype, servicepointstatus,
        COALESCE(SUM(billamount), 0),
        COALESCE(SUM(amountdue), 0),
        COALESCE(SUM(debtamount), 0),
        COALESCE(SUM(outstandingamount), 0),
        COALESCE(SUM(billconsumptionvalue), 0),
        COALESCE(SUM(paymentsamount), 0)
    FROM app.zeus_sales
    WHERE billingperiod_date >= date_trunc('month', p_from)
      AND billingperiod_date <  date_trunc('month', p_to) + interval '1 month'
    GROUP BY 1, 2, 3, 4, 5, 6, 7, 8, 9, 10;
$$;

-- ---------------------------------------------------------------------------
-- 2) Customer roster — accelerates customer_count (see the correctness
--    note above for why this can't just be another summary column).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS app.zeus_customer_roster (
    accountcode              text NOT NULL,
    servicepointcode         text NOT NULL,
    regionname               text,
    districtname             text,
    tariffclasscode          text,
    tariffclassname          text,
    serviceclass             text,
    accounttype              text,
    billstatus                text,
    metermodeltype             text,
    servicepointstatus          text,
    first_billingperiod_date date NOT NULL,
    last_billingperiod_date  date NOT NULL,
    PRIMARY KEY (accountcode, servicepointcode)
);

-- Overlap check for a requested [p_from, p_to) range: last >= p_from AND
-- first < p_to. Both columns need to be searchable for that, hence two
-- single-column indexes rather than one composite (Postgres can combine
-- them with a bitmap AND; a composite couldn't serve both bounds well
-- since neither column is a prefix of the other for this query shape).
CREATE INDEX IF NOT EXISTS idx_zeus_customer_roster_last_period
    ON app.zeus_customer_roster (last_billingperiod_date);
CREATE INDEX IF NOT EXISTS idx_zeus_customer_roster_first_period
    ON app.zeus_customer_roster (first_billingperiod_date);
CREATE INDEX IF NOT EXISTS idx_zeus_customer_roster_dims
    ON app.zeus_customer_roster (regionname, districtname, metermodeltype, accounttype);

-- resync_zeus_customer_roster: unlike the period summary, a roster row
-- can't be recomputed from just the batch's date range — a customer's
-- first/last active period can fall outside the touched window (e.g. a
-- batch only touches month 5, but the customer's roster row needs to
-- reflect months 1 through 5). So this finds which (accountcode,
-- servicepointcode) pairs have ANY row in [p_from, p_to] ("touched
-- accounts" — bounded to batch size, using
-- idx_zeus_sales_account_servicepoint), then recomputes those accounts'
-- roster rows from their FULL history (not date-bounded). Cost scales with
-- (batch size × average per-account row count), not total table size.
CREATE OR REPLACE FUNCTION app.resync_zeus_customer_roster(p_from date, p_to date)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_from date := date_trunc('month', p_from);
    v_to   date := date_trunc('month', p_to) + interval '1 month';
BEGIN
    -- Defensive: ON COMMIT DROP only clears this at the end of the
    -- enclosing transaction, not at the end of this function call — drop
    -- explicitly first in case a caller ever invokes this more than once
    -- inside one transaction (e.g. a multi-chunk backfill loop).
    DROP TABLE IF EXISTS _zcr_touched;
    CREATE TEMP TABLE _zcr_touched ON COMMIT DROP AS
    SELECT DISTINCT accountcode, servicepointcode
    FROM app.zeus_sales
    WHERE billingperiod_date >= v_from AND billingperiod_date < v_to;

    -- Touched accounts with no rows left at all (fully deleted) drop out
    -- of the roster entirely.
    DELETE FROM app.zeus_customer_roster r
    USING _zcr_touched t
    WHERE r.accountcode = t.accountcode
      AND r.servicepointcode = t.servicepointcode
      AND NOT EXISTS (
          SELECT 1 FROM app.zeus_sales z
          WHERE z.accountcode = t.accountcode
            AND z.servicepointcode = t.servicepointcode
      );

    -- Upsert fresh roster rows for touched accounts that still have data,
    -- recomputed from their complete history.
    INSERT INTO app.zeus_customer_roster (
        accountcode, servicepointcode, regionname, districtname,
        tariffclasscode, tariffclassname, serviceclass, accounttype,
        billstatus, metermodeltype, servicepointstatus,
        first_billingperiod_date, last_billingperiod_date
    )
    SELECT DISTINCT ON (z.accountcode, z.servicepointcode)
        z.accountcode, z.servicepointcode, z.regionname, z.districtname,
        z.tariffclasscode, z.tariffclassname, z.serviceclass, z.accounttype,
        z.billstatus, z.metermodeltype, z.servicepointstatus,
        MIN(z.billingperiod_date) OVER (PARTITION BY z.accountcode, z.servicepointcode),
        MAX(z.billingperiod_date) OVER (PARTITION BY z.accountcode, z.servicepointcode)
    FROM app.zeus_sales z
    JOIN _zcr_touched t
      ON z.accountcode = t.accountcode AND z.servicepointcode = t.servicepointcode
    ORDER BY z.accountcode, z.servicepointcode, z.billingperiod_date DESC
    ON CONFLICT (accountcode, servicepointcode) DO UPDATE SET
        regionname = EXCLUDED.regionname,
        districtname = EXCLUDED.districtname,
        tariffclasscode = EXCLUDED.tariffclasscode,
        tariffclassname = EXCLUDED.tariffclassname,
        serviceclass = EXCLUDED.serviceclass,
        accounttype = EXCLUDED.accounttype,
        billstatus = EXCLUDED.billstatus,
        metermodeltype = EXCLUDED.metermodeltype,
        servicepointstatus = EXCLUDED.servicepointstatus,
        first_billingperiod_date = EXCLUDED.first_billingperiod_date,
        last_billingperiod_date = EXCLUDED.last_billingperiod_date;
END;
$$;

-- ---------------------------------------------------------------------------
-- One-time backfill over all existing data. Run this once, right after
-- creating these tables/functions — it alone fixes today's "wide range
-- doesn't load" problem immediately, independent of whether the ingestion
-- process has been wired to call these functions per-batch yet (see
-- Service.Aggregate's routing comment in internal/zeusbilling/service.go
-- for what happens either way: dimension/date-only queries use these
-- tables once populated; queries with account/service point/meter code or
-- search filters keep using the raw table, unaffected by any of this).
-- Safe to re-run — it's just a full-range resync. This scans the whole
-- 18M-row table once, so run it in a low-traffic window.
-- ---------------------------------------------------------------------------
-- SELECT app.resync_zeus_sales_period_summary(
--     (SELECT min(billingperiod_date) FROM app.zeus_sales),
--     (SELECT max(billingperiod_date) FROM app.zeus_sales)
-- );
-- SELECT app.resync_zeus_customer_roster(
--     (SELECT min(billingperiod_date) FROM app.zeus_sales),
--     (SELECT max(billingperiod_date) FROM app.zeus_sales)
-- );
