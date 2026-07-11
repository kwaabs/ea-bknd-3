-- Incremental daily summary for app.mms_customer_sales.
--
-- Grain: one row per (day × region × district × contract_type × tariff ×
-- manufacturer × model). Sums and counts are re-aggregable, so the API can
-- serve ANY combination of dimension filters and ANY groupBy from this
-- table with plain SUM()s — never touching the raw table on the hot path.
--
-- Kept in sync by the ingestion process (single writer), NOT by a matview
-- refresh: cost scales with batch size, not table size.

CREATE TABLE IF NOT EXISTS app.mms_sales_daily_summary (
    day                          date    NOT NULL,
    region                       text,
    district                     text,
    contract_type                text,
    tariff                       text,
    manufacturer                 text,
    model                        text,
    customer_count               bigint  NOT NULL,
    sum_credit_balance_remaining numeric NOT NULL DEFAULT 0,
    sum_last_month_credit_read   numeric NOT NULL DEFAULT 0,
    sum_last_month_kwh_read      numeric NOT NULL DEFAULT 0
);

-- Date-range filters are the only selective predicate the API sends that
-- isn't a tiny dimension; everything else can seq-scan this small table.
CREATE INDEX IF NOT EXISTS idx_mms_sales_summary_day
    ON app.mms_sales_daily_summary (day);

-- ---------------------------------------------------------------------------
-- resync: delete + rebuild the summary for a date range, atomically.
--
-- CONTRACT FOR THE INGESTION PROCESS (single writer):
--   1. Apply raw deletes and inserts for the batch.
--   2. SELECT app.resync_mms_sales_summary(:from_day, :to_day);
--      where [from_day, to_day] covers the UNION of all dates touched by
--      the batch — dates of deleted rows AND dates of inserted rows. This
--      matters for delete-before-replace: a day whose rows were deleted but
--      not replaced must be inside the range so its summary rows vanish too.
--   3. Invalidate the Redis prefix (cache.DeleteByPrefix), same as today.
--
-- The function body runs as a single statement-level transaction, so readers
-- never observe a half-resynced range. Rows with NULL date_time are not
-- representable at a daily grain and are excluded from the summary; if your
-- pipeline can produce them, either backfill date_time or accept that only
-- date-stamped rows appear in aggregates served from the summary.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION app.resync_mms_sales_summary(p_from date, p_to date)
RETURNS void
LANGUAGE sql
AS $$
    DELETE FROM app.mms_sales_daily_summary
    WHERE day >= p_from
      AND day <= p_to;

    INSERT INTO app.mms_sales_daily_summary
        (day, region, district, contract_type, tariff, manufacturer, model,
         customer_count,
         sum_credit_balance_remaining,
         sum_last_month_credit_read,
         sum_last_month_kwh_read)
    SELECT
        date_trunc('day', date_time)::date AS day,
        region, district, contract_type, tariff, manufacturer, model,
        COUNT(*),
        COALESCE(SUM(sts_credit_balance_remaining), 0),
        COALESCE(SUM(sts_last_month_credit_read), 0),
        COALESCE(SUM(sts_last_month_kwh_read), 0)
    FROM app.mms_customer_sales
    WHERE date_time >= p_from
      AND date_time <  (p_to + 1)
    GROUP BY 1, 2, 3, 4, 5, 6, 7;
$$;

-- ---------------------------------------------------------------------------
-- One-time backfill over all existing data (run once after creating the
-- table; safe to re-run — it is just a full-range resync):
-- ---------------------------------------------------------------------------
-- SELECT app.resync_mms_sales_summary(
--     (SELECT min(date_time)::date FROM app.mms_customer_sales),
--     (SELECT max(date_time)::date FROM app.mms_customer_sales)
-- );
