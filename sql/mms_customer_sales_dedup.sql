-- MMS re-sync duplicate rows: the ingestion process appears to periodically
-- re-write the SAME (meter_number, sts_credit_balance_remaining,
-- sts_last_month_credit_read, sts_last_month_kwh_read) reading under a NEW
-- date_time — observed live: identical values for the same meter 2 days
-- apart (04-13 and 04-15), then genuinely different values a month later
-- (05-12). Every current MMS query (Detail and Aggregate, both the raw
-- fallback and the daily summary fast path — see internal/mmssales/service.go)
-- sums sts_last_month_kwh_read/credit figures ACROSS whatever days fall in
-- the requested range, so two same-value rows landing on different days
-- get added together — inflating every chart/KPI covering more than one of
-- those re-sync dates. customer_count is unaffected (already
-- COUNT(DISTINCT account_number, meter_number)).
--
-- Confirmed rule: same meter_number + all 3 reading values = a duplicate
-- re-sync, NOT two genuinely different readings — but ONLY within the same
-- calendar month. sts_last_month_kwh_read is a monthly snapshot; a meter
-- legitimately CAN report the identical figure (e.g. 0 kWh, vacant
-- property) in two different months, and that must not be collapsed into
-- one row — only re-syncs of what is still describing the same month's
-- reading are duplicates. Latest date_time wins as the representative row
-- (confirmed with the user).
--
-- This is a resync-time flag, not a live filter or a delete: deduping
-- against the full 1.29M+-row table on every request would reintroduce the
-- exact full-scan cost the Zeus fast-path work
-- (sql/summary_zeus_sales.sql) just eliminated there. Not a DELETE either
-- — app.mms_customer_sales is externally, single-writer ingested (same
-- assumption as sql/summary_mms_customer_sales.sql); deleting rows now
-- doesn't stop the ingestion process from writing the same kind of
-- duplicate again on its own cadence, so only a durable query-time fix
-- actually solves this going forward. Raw duplicate rows stay in the table
-- (still visible to anyone querying it directly / for audit), just flagged
-- and excluded from every app-facing query.
--
-- Needed for the resync function's per-meter lookup (recomputing a touched
-- meter's flags needs its whole history, not just the touched batch's date
-- range — see the comment on resync_mms_duplicate_flags).
--
-- On a large live table, prefer CREATE INDEX CONCURRENTLY (cannot run
-- inside a transaction, so run this statement by itself if the table has
-- live traffic):
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mms_customer_sales_meter_number
--       ON app.mms_customer_sales (meter_number);
CREATE INDEX IF NOT EXISTS idx_mms_customer_sales_meter_number
    ON app.mms_customer_sales (meter_number);

ALTER TABLE app.mms_customer_sales
    ADD COLUMN IF NOT EXISTS is_duplicate_reading boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_mms_customer_sales_dup_flag
    ON app.mms_customer_sales (is_duplicate_reading);

-- resync_mms_duplicate_flags: finds meters touched in [p_from, p_to)
-- (bounded to batch size via idx_mms_customer_sales_meter_number), then
-- recomputes is_duplicate_reading for ALL of those meters' rows across
-- their ENTIRE history — not just the touched date range. This matters:
-- a newly inserted row can retroactively change which prior row in its
-- (meter, month, reading) group is "latest," and a duplicate group's
-- members are not necessarily confined to the batch window being synced.
-- Same "touched set, full-history recompute" shape as
-- resync_zeus_customer_roster in sql/summary_zeus_sales.sql, for the same
-- reason. Uses ctid to write back per-row flags — app.mms_customer_sales
-- has no declared primary key/id column, and ctid is stable for the
-- duration of this one statement, which is all this needs.
CREATE OR REPLACE FUNCTION app.resync_mms_duplicate_flags(p_from date, p_to date)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_from timestamptz := p_from;
    v_to   timestamptz := p_to + 1;
    v_touched_meters int;
    v_rows_changed int;
BEGIN
    DROP TABLE IF EXISTS _mms_touched_meters;
    CREATE TEMP TABLE _mms_touched_meters ON COMMIT DROP AS
    SELECT DISTINCT meter_number
    FROM app.mms_customer_sales
    WHERE date_time >= v_from AND date_time < v_to
      AND meter_number IS NOT NULL AND meter_number <> '';

    GET DIAGNOSTICS v_touched_meters = ROW_COUNT;
    RAISE NOTICE 'resync_mms_duplicate_flags(%, %): % meter(s) touched, recomputing full history for each',
        p_from, p_to, v_touched_meters;

    WITH ranked AS (
        SELECT
            z.ctid,
            ROW_NUMBER() OVER (
                PARTITION BY z.meter_number, date_trunc('month', z.date_time),
                             z.sts_credit_balance_remaining, z.sts_last_month_credit_read,
                             z.sts_last_month_kwh_read
                ORDER BY z.date_time DESC
            ) AS rn
        FROM app.mms_customer_sales z
        JOIN _mms_touched_meters t ON z.meter_number = t.meter_number
    )
    UPDATE app.mms_customer_sales z
    SET is_duplicate_reading = (ranked.rn > 1)
    FROM ranked
    WHERE z.ctid = ranked.ctid
      AND z.is_duplicate_reading IS DISTINCT FROM (ranked.rn > 1);

    GET DIAGNOSTICS v_rows_changed = ROW_COUNT;
    RAISE NOTICE 'resync_mms_duplicate_flags(%, %): done, % row(s) had their flag changed',
        p_from, p_to, v_rows_changed;
END;
$$;

-- resync_mms_sales_summary is REPLACED here (same name/signature as
-- sql/summary_mms_customer_sales.sql) to exclude flagged duplicates from
-- the pre-aggregated daily summary — otherwise the summary's SUM columns
-- would still be inflated even after Detail/Aggregate's raw-fallback path
-- get the fix below. Everything else about this function is unchanged
-- from the original.
--
-- ORDERING: resync_mms_duplicate_flags must run BEFORE this, for the same
-- batch's touched meters — this function only excludes rows already
-- flagged, it doesn't compute the flag itself.
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
      AND NOT is_duplicate_reading
    GROUP BY 1, 2, 3, 4, 5, 6, 7;
$$;

-- ---------------------------------------------------------------------------
-- Ingestion sequence per batch (extends the existing contract in
-- sql/summary_mms_customer_sales.sql — this adds a step, doesn't replace
-- the rest of it):
--   raw deletes → raw inserts
--     → resync_mms_duplicate_flags(range)      [NEW]
--     → resync_mms_sales_summary(range)         [now duplicate-excluding]
--     → DeleteByPrefix (cache invalidation, unchanged)
-- ---------------------------------------------------------------------------

-- ONE-TIME backfill over all existing data only. Run resync_mms_duplicate_flags
-- FIRST, then resync_mms_sales_summary — the summary rebuild depends on the
-- flags already being set. Safe to re-run either. This re-scans the whole
-- table, so run it in a low-traffic window; sanity-check on one narrow
-- month first before the full range, same recommendation as the Zeus
-- backfills.
--
-- Do NOT run this min..max form after every load — every meter in the
-- table falls inside [min(date_time), max(date_time)], so it is a full
-- rescan/rewrite of app.mms_customer_sales every time, and gets slower as
-- the table grows (21M+ rows and counting). For routine "run after every
-- update" maintenance, use sql/mms_resync_watermark.sql's
-- app.resync_mms_incremental() instead — it only covers the window since
-- the last run:
--   SELECT app.resync_mms_incremental();
-- SELECT app.resync_mms_duplicate_flags(
--     (SELECT min(date_time)::date FROM app.mms_customer_sales),
--     (SELECT max(date_time)::date FROM app.mms_customer_sales)
-- );
-- SELECT app.resync_mms_sales_summary(
--     (SELECT min(date_time)::date FROM app.mms_customer_sales),
--     (SELECT max(date_time)::date FROM app.mms_customer_sales)
-- );
