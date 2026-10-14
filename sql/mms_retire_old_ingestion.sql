-- Retires the old MMS_SALES ingestion path now that mms_sales_accounting
-- (sql/mms_sales_accounting_migration.sql) is the live data source.
--
-- Nothing in this Go codebase calls app.populate_mms_customer_sales or
-- app.resync_mms_incremental directly — confirmed by grep, they're invoked
-- externally (a scheduler/cron/manual CALL outside this repo). This script
-- can only close the door from the database side: it makes the OLD
-- procedure refuse to run, loudly, instead of silently writing another
-- round of per-reading/credit-balance rows into app.mms_customer_sales
-- alongside the new monthly rows. If something outside this repo is
-- scheduled to call app.populate_mms_customer_sales() on an interval
-- (a cron job, a Task Scheduler entry, a Supabase scheduled function —
-- anything not in this Git history), THAT still needs to be disabled or
-- repointed at app.populate_mms_customer_sales_accounting() separately —
-- this script cannot see or touch it.
--
-- NOT dropped outright: the original definition stays recoverable (just
-- renamed) rather than destroyed, in case historical MMS_SALES data ever
-- needs a one-off re-run for backfill/audit purposes.
--
-- app.resync_mms_incremental() (sql/mms_resync_watermark.sql) is left
-- alone — it's generic dedup/summary maintenance over
-- app.mms_customer_sales, not specific to the old source, and both the
-- old and new pipelines can share it safely.

ALTER PROCEDURE app.populate_mms_customer_sales(integer, boolean)
    RENAME TO populate_mms_customer_sales_retired_do_not_call;

CREATE OR REPLACE PROCEDURE app.populate_mms_customer_sales(
    IN p_batch_size integer DEFAULT 10000,
    IN p_reset boolean DEFAULT false
)
 LANGUAGE plpgsql
AS $procedure$
BEGIN
    RAISE EXCEPTION
        'app.populate_mms_customer_sales is retired — MMS now ingests from app.mms_sales_accounting. Call app.populate_mms_customer_sales_accounting() instead. (The original definition is preserved as app.populate_mms_customer_sales_retired_do_not_call for historical MMS_SALES backfill/audit only — do not schedule it.)';
END;
$procedure$
;
