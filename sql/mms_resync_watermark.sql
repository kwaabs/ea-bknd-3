-- Incremental wrapper around resync_mms_duplicate_flags/resync_mms_sales_summary
-- (sql/mms_customer_sales_dedup.sql).
--
-- Problem: the "run everytime the data is updated" snippet at the bottom of
-- mms_customer_sales_dedup.sql passes min(date_time)..max(date_time) as the
-- range. resync_mms_duplicate_flags finds meters touched inside that range
-- and recomputes flags across each touched meter's ENTIRE history — with a
-- full min..max range, every meter in the table is "touched," so it's a
-- full-table rescan/rewrite. That form was meant as a one-time backfill,
-- not a per-load routine. At 21M+ rows and growing, running it after every
-- load means every load gets slower than the last, forever.
--
-- Fix: track how far the table has already been resynced (a watermark) and
-- only cover the new window each run, not the whole table.
CREATE TABLE IF NOT EXISTS app.mms_resync_watermark (
    id             boolean PRIMARY KEY DEFAULT true CHECK (id), -- single row
    synced_through date
);

-- resync_mms_incremental: run this after every MMS load instead of the
-- hand-built min/max snippet.
--
--   SELECT app.resync_mms_incremental();
--
-- overlap_days re-opens the window behind the watermark on every run, to
-- catch the case documented in mms_customer_sales_dedup.sql — the ingestion
-- process re-writing an already-synced reading under a NEW, more recent
-- date_time. That new row's date_time is always >= this run's window start
-- (it's freshly written), so it will be picked up regardless of overlap;
-- overlap_days is just a safety margin against ingestion lag/reordering,
-- not load-bearing for that specific bug. Default 3 days; widen it if the
-- ingestion process is ever observed re-syncing something older than that.
--
-- First run (no watermark row yet) falls back to the full min(date_time),
-- i.e. one real full-table pass — expected and fine, it only happens once.
-- Every run after that only touches meters with activity in the recent
-- window, so cost tracks batch size, not table size.
-- RAISE NOTICE below is what actually shows up "live" while this runs — in
-- psql, pgAdmin, DBeaver, and the Supabase SQL editor's Messages/Logs pane
-- it streams out statement-by-statement, unlike a return value (RETURNS
-- TABLE etc.), which a client can only ever see after the whole function
-- has already finished. That's the fix for "it doesn't output any logs to
-- show status": there was never a RAISE NOTICE anywhere in this call chain
-- before, so a client had nothing to display until the whole run — full
-- table pass included — completed.
CREATE OR REPLACE FUNCTION app.resync_mms_incremental(overlap_days int DEFAULT 3)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_from      date;
    v_to        date;
    v_is_backfill boolean;
    v_t0        timestamptz := clock_timestamp();
    v_t1        timestamptz;
BEGIN
    SELECT max(date_time)::date INTO v_to FROM app.mms_customer_sales;
    IF v_to IS NULL THEN
        RAISE NOTICE 'resync_mms_incremental: app.mms_customer_sales is empty, nothing to sync';
        RETURN;
    END IF;

    SELECT synced_through INTO v_from FROM app.mms_resync_watermark WHERE id;
    v_is_backfill := v_from IS NULL;
    IF v_is_backfill THEN
        SELECT min(date_time)::date INTO v_from FROM app.mms_customer_sales;
        RAISE NOTICE 'resync_mms_incremental: no watermark yet — this is a full backfill (% .. %), may take a while',
            v_from, v_to;
    ELSE
        v_from := v_from - overlap_days;
        RAISE NOTICE 'resync_mms_incremental: incremental run, range % .. % (watermark % minus % day overlap)',
            v_from, v_to, v_from + overlap_days, overlap_days;
    END IF;

    RAISE NOTICE 'resync_mms_incremental: step 1/2 — resync_mms_duplicate_flags...';
    PERFORM app.resync_mms_duplicate_flags(v_from, v_to);
    v_t1 := clock_timestamp();
    RAISE NOTICE 'resync_mms_incremental: step 1/2 done in %', (v_t1 - v_t0);

    RAISE NOTICE 'resync_mms_incremental: step 2/2 — resync_mms_sales_summary...';
    PERFORM app.resync_mms_sales_summary(v_from, v_to);
    RAISE NOTICE 'resync_mms_incremental: step 2/2 done in %', (clock_timestamp() - v_t1);

    INSERT INTO app.mms_resync_watermark (id, synced_through)
    VALUES (true, v_to)
    ON CONFLICT (id) DO UPDATE SET synced_through = EXCLUDED.synced_through;

    RAISE NOTICE 'resync_mms_incremental: done in % total, watermark advanced to %',
        (clock_timestamp() - v_t0), v_to;
END;
$$;
