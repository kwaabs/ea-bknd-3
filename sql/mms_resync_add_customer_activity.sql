-- Adds resync_mms_customer_activity (sql/summary_mms_customer_activity.sql)
-- as a third step of resync_mms_incremental (sql/mms_resync_watermark.sql),
-- using the exact same watermark/window — no new scheduling, no new
-- ingestion-process hook, just one more PERFORM in the existing pipeline.
--
-- Run sql/summary_mms_customer_activity.sql (creates the table + function)
-- before this file.
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
    v_t2        timestamptz;
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

    RAISE NOTICE 'resync_mms_incremental: step 1/3 — resync_mms_duplicate_flags...';
    PERFORM app.resync_mms_duplicate_flags(v_from, v_to);
    v_t1 := clock_timestamp();
    RAISE NOTICE 'resync_mms_incremental: step 1/3 done in %', (v_t1 - v_t0);

    RAISE NOTICE 'resync_mms_incremental: step 2/3 — resync_mms_sales_summary...';
    PERFORM app.resync_mms_sales_summary(v_from, v_to);
    v_t2 := clock_timestamp();
    RAISE NOTICE 'resync_mms_incremental: step 2/3 done in %', (v_t2 - v_t1);

    RAISE NOTICE 'resync_mms_incremental: step 3/3 — resync_mms_customer_activity...';
    PERFORM app.resync_mms_customer_activity(v_from, v_to);
    RAISE NOTICE 'resync_mms_incremental: step 3/3 done in %', (clock_timestamp() - v_t2);

    INSERT INTO app.mms_resync_watermark (id, synced_through)
    VALUES (true, v_to)
    ON CONFLICT (id) DO UPDATE SET synced_through = EXCLUDED.synced_through;

    RAISE NOTICE 'resync_mms_incremental: done in % total, watermark advanced to %',
        (clock_timestamp() - v_t0), v_to;
END;
$$;
