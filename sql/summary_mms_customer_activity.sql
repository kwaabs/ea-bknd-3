-- Incremental per-customer activity summary for app.mms_customer_sales,
-- built to answer "how many distinct customers were active in date range
-- [from, to]" without a live scan+dedup over the raw table (11M+ rows,
-- 5.4GB — confirmed via EXPLAIN ANALYZE to take ~65s for an 8-month
-- range, even with a covering index the planner correctly declines to
-- use once the range covers most of the table). See
-- internal/mmssales/service.go's distinctCustomerCountsFast.
--
-- Grain: one row per customer per (region x district x contract_type x
-- tariff x manufacturer x model) combination they've ever had — the same
-- dimension set as app.mms_sales_daily_summary, so every groupBy value
-- the API already supports works identically. active_months is the set
-- of first-of-month dates (deduped) this combination had ANY raw row in.
--
-- Trade-off: month granularity, not exact day. A customer whose only
-- activity in a boundary month falls outside the exact requested date
-- range (e.g. range starts Jan 15, customer's only January row is Jan 3)
-- still counts as active for that month. Given customers post roughly
-- once a month on average (confirmed: 11.57M raw rows / 1.37M distinct
-- customers ~= 8.4 rows/customer across ~8 months of history), this
-- loses essentially no real precision for the normal case — and this
-- feeds a dashboard KPI, not a billing-critical figure.
--
-- Kept in sync the same way app.mms_sales_daily_summary is — see
-- resync_mms_incremental() in sql/mms_resync_watermark.sql, which now
-- calls resync_mms_customer_activity as its third step, same watermark,
-- same ingestion-process contract.

CREATE TABLE IF NOT EXISTS app.mms_customer_activity (
    account_number text   NOT NULL,
    meter_number   text   NOT NULL,
    region         text   NOT NULL DEFAULT '',
    district       text   NOT NULL DEFAULT '',
    contract_type  text   NOT NULL DEFAULT '',
    tariff         text   NOT NULL DEFAULT '',
    manufacturer   text   NOT NULL DEFAULT '',
    model          text   NOT NULL DEFAULT '',
    active_months  date[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (account_number, meter_number, region, district, contract_type, tariff, manufacturer, model)
);

-- GIN index for fast `active_months && ARRAY[...]` overlap queries — the
-- one operation every read against this table performs.
CREATE INDEX IF NOT EXISTS idx_mms_customer_activity_months
    ON app.mms_customer_activity USING gin (active_months);

-- Dimension filters (region/district/contract_type/tariff/manufacturer/
-- model) are applied the same way base()/summaryBase() apply them
-- elsewhere in this package — lower(col) IN (...) or blank-match — via
-- the same dimensionFilters() helper, reused as-is since this table
-- shares those column names.
CREATE INDEX IF NOT EXISTS idx_mms_customer_activity_lower_region
    ON app.mms_customer_activity (lower(region));
CREATE INDEX IF NOT EXISTS idx_mms_customer_activity_lower_district
    ON app.mms_customer_activity (lower(district));

-- ---------------------------------------------------------------------------
-- resync: for the touched date range, strip any of its months from every
-- existing row's active_months (they'll be re-added below if the raw data
-- still supports them — this is what makes deletes/edits correct, not
-- just inserts), drop rows left with no months at all, then re-add
-- whichever months are actually present now per customer combination.
--
-- CONTRACT: same as resync_mms_sales_summary — call with [p_from, p_to]
-- covering the UNION of all dates touched by the batch (deleted AND
-- inserted). Driven by resync_mms_incremental() (sql/mms_resync_watermark.sql).
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION app.resync_mms_customer_activity(p_from date, p_to date)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_months date[];
BEGIN
    SELECT array_agg(DISTINCT date_trunc('month', d)::date)
    INTO v_months
    FROM generate_series(p_from, p_to, '1 day'::interval) AS d;

    -- Step 1: strip touched months from existing rows.
    UPDATE app.mms_customer_activity t
    SET active_months = ARRAY(
        SELECT m FROM unnest(t.active_months) AS m
        WHERE m <> ALL (v_months)
    )
    WHERE t.active_months && v_months;

    -- Step 2: drop rows left with no months at all (their only activity
    -- was in the touched range and it's gone now).
    DELETE FROM app.mms_customer_activity
    WHERE active_months = '{}';

    -- Step 3: re-add whichever months are actually present now, per
    -- customer combination, upserting into existing or freshly-deleted
    -- (now absent) rows alike.
    INSERT INTO app.mms_customer_activity
        (account_number, meter_number, region, district, contract_type, tariff, manufacturer, model, active_months)
    SELECT
        account_number, meter_number,
        COALESCE(region, ''), COALESCE(district, ''),
        COALESCE(contract_type, ''), COALESCE(tariff, ''),
        COALESCE(manufacturer, ''), COALESCE(model, ''),
        array_agg(DISTINCT date_trunc('month', date_time)::date)
    FROM app.mms_customer_sales
    WHERE NOT is_duplicate_reading
      AND date_time >= p_from
      AND date_time <  (p_to + 1)
    GROUP BY account_number, meter_number, COALESCE(region, ''), COALESCE(district, ''),
             COALESCE(contract_type, ''), COALESCE(tariff, ''), COALESCE(manufacturer, ''), COALESCE(model, '')
    ON CONFLICT (account_number, meter_number, region, district, contract_type, tariff, manufacturer, model)
    DO UPDATE SET active_months = (
        SELECT array_agg(DISTINCT m ORDER BY m)
        FROM unnest(app.mms_customer_activity.active_months || EXCLUDED.active_months) AS m
    );
END;
$$;

-- ---------------------------------------------------------------------------
-- One-time backfill over all existing data (run once after creating the
-- table; safe to re-run — it is just a full-range resync). This will take
-- a while over 11.5M rows — it's the one time this table's cost scales
-- with table size instead of batch size:
-- ---------------------------------------------------------------------------
-- SELECT app.resync_mms_customer_activity(
--     (SELECT min(date_time)::date FROM app.mms_customer_sales),
--     (SELECT max(date_time)::date FROM app.mms_customer_sales)
-- );
