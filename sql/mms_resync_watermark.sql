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
CREATE OR REPLACE FUNCTION app.resync_mms_incremental(overlap_days int DEFAULT 3)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_from date;
    v_to   date;
BEGIN
    SELECT max(date_time)::date INTO v_to FROM app.mms_customer_sales;
    IF v_to IS NULL THEN
        RETURN; -- table empty, nothing to sync
    END IF;

    SELECT synced_through INTO v_from FROM app.mms_resync_watermark WHERE id;
    IF v_from IS NULL THEN
        SELECT min(date_time)::date INTO v_from FROM app.mms_customer_sales;
    ELSE
        v_from := v_from - overlap_days;
    END IF;

    PERFORM app.resync_mms_duplicate_flags(v_from, v_to);
    PERFORM app.resync_mms_sales_summary(v_from, v_to);

    INSERT INTO app.mms_resync_watermark (id, synced_through)
    VALUES (true, v_to)
    ON CONFLICT (id) DO UPDATE SET synced_through = EXCLUDED.synced_through;
END;
$$;
