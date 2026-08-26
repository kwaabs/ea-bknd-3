-- MMS-takes-precedence rule for Zeus Prepaid vs MMS, confirmed against
-- production data (not assumed): 249,091 Zeus Prepaid meters matched an
-- MMS meter_number, 186,514 (75%) of those also shared a normalized
-- district — real duplication, not a coincidental code collision in a
-- small ID space. Business rule (confirmed): where Zeus Prepaid and MMS
-- are combined into one blended figure, a meter known to MMS is counted
-- from MMS; Zeus Prepaid fills in only the meters MMS doesn't have.
--
-- Scope: ONLY the blended/combined views (Prepaid hub page,
-- customer-sales-overview's "Combined Postpaid+Prepaid" chart/table).
-- Zeus's own standalone Prepaid reporting elsewhere (region-detail,
-- postpaid/prepaid hub's own Zeus tab, etc.) is UNCHANGED — that's Zeus's
-- own book of record and stays complete, exactly as Zeus reports it. This
-- file only adds an alternate, opt-in set of columns; it does not modify
-- anything an existing caller already reads.
--
-- Why this needs its own resync-time columns rather than a request-time
-- join: sql/summary_zeus_sales.sql's period summary is already SUMMED
-- past individual meter identity (that's what makes it fast) — you can't
-- exclude "meters MMS also has" from a number that no longer knows which
-- meters went into it. The exclusion has to happen before the sum, so it
-- has to happen at resync time, same as everything else in that file.
--
-- Prerequisite: MMS's meter_number needs to be efficiently searchable in
-- normalized (trim+upper) form — the resync functions below check "does
-- this Zeus meter's code exist in MMS" once per touched row, and without
-- this index that's a sequential scan of app.mms_customer_sales (1.29M+
-- distinct meters) per check.
--
-- On a large live table, prefer CREATE INDEX CONCURRENTLY (cannot run
-- inside a transaction, so run this statement by itself if the table has
-- live traffic):
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mms_customer_sales_meter_number_norm
--       ON app.mms_customer_sales (upper(trim(meter_number)));
CREATE INDEX IF NOT EXISTS idx_mms_customer_sales_meter_number_norm
    ON app.mms_customer_sales (upper(trim(meter_number)));

-- ---------------------------------------------------------------------------
-- 1) Period summary: an MMS-precedence-excluded sibling of every existing
--    sum column. For any row where metermodeltype isn't 'prepaid', these
--    are identical to the regular sum columns — the exclusion only ever
--    fires for Prepaid, per the confirmed rule's scope. Nothing about the
--    existing sum_* columns changes; existing callers are unaffected.
-- ---------------------------------------------------------------------------
ALTER TABLE app.zeus_sales_period_summary
    ADD COLUMN IF NOT EXISTS sum_billamount_excl_mms_dup           numeric NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sum_amountdue_excl_mms_dup            numeric NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sum_debtamount_excl_mms_dup           numeric NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sum_outstandingamount_excl_mms_dup    numeric NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sum_billconsumptionvalue_excl_mms_dup numeric NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sum_paymentsamount_excl_mms_dup       numeric NOT NULL DEFAULT 0;

-- Replaces the period-summary resync from sql/summary_zeus_sales.sql —
-- same signature and delete-then-rebuild contract, now also populating
-- the _excl_mms_dup columns via a per-row FILTER against MMS's normalized
-- meter numbers.
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
        sum_billconsumptionvalue, sum_paymentsamount,
        sum_billamount_excl_mms_dup, sum_amountdue_excl_mms_dup,
        sum_debtamount_excl_mms_dup, sum_outstandingamount_excl_mms_dup,
        sum_billconsumptionvalue_excl_mms_dup, sum_paymentsamount_excl_mms_dup
    )
    SELECT
        z.billingperiod_date, z.regionname, z.districtname, z.tariffclasscode,
        z.tariffclassname, z.serviceclass, z.accounttype, z.billstatus,
        z.metermodeltype, z.servicepointstatus,
        COALESCE(SUM(z.billamount), 0),
        COALESCE(SUM(z.amountdue), 0),
        COALESCE(SUM(z.debtamount), 0),
        COALESCE(SUM(z.outstandingamount), 0),
        COALESCE(SUM(z.billconsumptionvalue), 0),
        COALESCE(SUM(z.paymentsamount), 0),
        COALESCE(SUM(z.billamount) FILTER (WHERE NOT is_mms_dup.matched), 0),
        COALESCE(SUM(z.amountdue) FILTER (WHERE NOT is_mms_dup.matched), 0),
        COALESCE(SUM(z.debtamount) FILTER (WHERE NOT is_mms_dup.matched), 0),
        COALESCE(SUM(z.outstandingamount) FILTER (WHERE NOT is_mms_dup.matched), 0),
        COALESCE(SUM(z.billconsumptionvalue) FILTER (WHERE NOT is_mms_dup.matched), 0),
        COALESCE(SUM(z.paymentsamount) FILTER (WHERE NOT is_mms_dup.matched), 0)
    FROM app.zeus_sales z
    LEFT JOIN LATERAL (
        SELECT
            lower(z.metermodeltype) = 'prepaid'
            AND EXISTS (
                SELECT 1 FROM app.mms_customer_sales m
                WHERE upper(trim(m.meter_number)) = upper(trim(z.metercode))
            ) AS matched
    ) is_mms_dup ON true
    WHERE z.billingperiod_date >= date_trunc('month', p_from)
      AND z.billingperiod_date <  date_trunc('month', p_to) + interval '1 month'
    GROUP BY 1, 2, 3, 4, 5, 6, 7, 8, 9, 10;
$$;

-- ---------------------------------------------------------------------------
-- 2) Customer roster: flag per customer instead of a sibling count column
--    (distinct counts don't work as sibling sums, per the roster's whole
--    reason for existing — see sql/summary_zeus_sales.sql). A blended
--    customer_count query filters WHERE NOT has_mms_duplicate.
-- ---------------------------------------------------------------------------
ALTER TABLE app.zeus_customer_roster
    ADD COLUMN IF NOT EXISTS has_mms_duplicate boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_zeus_customer_roster_mms_dup
    ON app.zeus_customer_roster (has_mms_duplicate);

-- Replaces the roster resync from sql/summary_zeus_sales.sql — identical
-- shape, now also setting has_mms_duplicate from each touched customer's
-- latest metercode (same DISTINCT ON row the rest of the upsert already
-- picks).
CREATE OR REPLACE FUNCTION app.resync_zeus_customer_roster(p_from date, p_to date)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_from date := date_trunc('month', p_from);
    v_to   date := date_trunc('month', p_to) + interval '1 month';
BEGIN
    DROP TABLE IF EXISTS _zcr_touched;
    CREATE TEMP TABLE _zcr_touched ON COMMIT DROP AS
    SELECT DISTINCT accountcode, servicepointcode
    FROM app.zeus_sales
    WHERE billingperiod_date >= v_from AND billingperiod_date < v_to;

    DELETE FROM app.zeus_customer_roster r
    USING _zcr_touched t
    WHERE r.accountcode = t.accountcode
      AND r.servicepointcode = t.servicepointcode
      AND NOT EXISTS (
          SELECT 1 FROM app.zeus_sales z
          WHERE z.accountcode = t.accountcode
            AND z.servicepointcode = t.servicepointcode
      );

    INSERT INTO app.zeus_customer_roster (
        accountcode, servicepointcode, regionname, districtname,
        tariffclasscode, tariffclassname, serviceclass, accounttype,
        billstatus, metermodeltype, servicepointstatus,
        first_billingperiod_date, last_billingperiod_date, has_mms_duplicate
    )
    SELECT DISTINCT ON (z.accountcode, z.servicepointcode)
        z.accountcode, z.servicepointcode, z.regionname, z.districtname,
        z.tariffclasscode, z.tariffclassname, z.serviceclass, z.accounttype,
        z.billstatus, z.metermodeltype, z.servicepointstatus,
        MIN(z.billingperiod_date) OVER (PARTITION BY z.accountcode, z.servicepointcode),
        MAX(z.billingperiod_date) OVER (PARTITION BY z.accountcode, z.servicepointcode),
        lower(z.metermodeltype) = 'prepaid'
        AND EXISTS (
            SELECT 1 FROM app.mms_customer_sales m
            WHERE upper(trim(m.meter_number)) = upper(trim(z.metercode))
        )
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
        last_billingperiod_date = EXCLUDED.last_billingperiod_date,
        has_mms_duplicate = EXCLUDED.has_mms_duplicate;
END;
$$;

-- ---------------------------------------------------------------------------
-- One-time backfill: re-run both resyncs over the full history so the new
-- _excl_mms_dup columns and has_mms_duplicate flag are populated
-- everywhere, not just for future batches. Safe to re-run. This re-scans
-- everything (including the MMS EXISTS check per Prepaid row), so it costs
-- meaningfully more than sql/summary_zeus_sales.sql's original backfill —
-- run it in a low-traffic window, and sanity-check on one narrow month
-- first, same recommendation as before.
-- ---------------------------------------------------------------------------
-- SELECT app.resync_zeus_sales_period_summary(
--     (SELECT min(billingperiod_date) FROM app.zeus_sales),
--     (SELECT max(billingperiod_date) FROM app.zeus_sales)
-- );
-- SELECT app.resync_zeus_customer_roster(
--     (SELECT min(billingperiod_date) FROM app.zeus_sales),
--     (SELECT max(billingperiod_date) FROM app.zeus_sales)
-- );
