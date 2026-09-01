-- Makes app.migrate_zeus_sales_from_working resumable across separate
-- CALLs (not part of this repo's Go application — this stored procedure
-- runs manually against the database as an operational migration tool,
-- moving rows from app.zeus_sales_working into app.zeus_sales in
-- committed batches).
--
-- The original version tracked its cursor (v_last_id) as a local plpgsql
-- variable only, which resets to 0 every time the procedure is CALLed —
-- so an interrupted run (or a deliberate re-run) always re-scanned the
-- entire source table (18.8M+ rows) from the beginning. Re-running was
-- always *safe* (INSERT ... ON CONFLICT DO UPDATE, never DELETE, so no
-- data loss or duplication either way) — just wasteful, since every row
-- gets re-upserted with the same values even when nothing changed.
--
-- This version persists the cursor in app.migration_checkpoints, updated
-- atomically with each batch (in the same transaction, right before that
-- batch's COMMIT), so a plain `CALL app.migrate_zeus_sales_from_working();`
-- with no arguments automatically continues from wherever the last run
-- stopped instead of starting over. Pass p_reset := true to deliberately
-- force a full re-run from the beginning (e.g. after fixing a column
-- mapping bug).
--
-- The cursor is app.zeus_sales_working.id (an arbitrary surrogate key,
-- walked in ascending order) — NOT a date. Resuming automatically via the
-- checkpoint needs no date reasoning at all; jumping to a specific date
-- manually (skip-ahead, not the normal resume path) means computing the
-- corresponding MAX(id) for that cutoff and writing it into the
-- checkpoint row yourself, e.g.:
--
--     UPDATE app.migration_checkpoints
--     SET last_id = (
--         SELECT MAX(id) FROM app.zeus_sales_working
--         WHERE make_date(billingyear::integer, billingmonth::integer, 1) < '2026-06-02'
--     ),
--         updated_at = clock_timestamp()
--     WHERE procedure_name = 'migrate_zeus_sales_from_working';

-- ============================================================
-- 1. Checkpoint table — durable, survives across separate CALLs
--    (unlike v_last_id, which is just a local plpgsql variable
--    and resets to 0 every time the procedure is invoked).
--    One row per procedure, in case other batch migrations
--    want to reuse this same table later.
-- ============================================================
CREATE TABLE IF NOT EXISTS app.migration_checkpoints (
    procedure_name text PRIMARY KEY,
    last_id        bigint NOT NULL DEFAULT 0,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- ============================================================
-- 2. Modified procedure — same batching/upsert logic as before, plus:
--      - reads the checkpoint at the start instead of hardcoding 0
--      - writes the checkpoint back INSIDE the loop, in the same
--        transaction as the batch upsert, right before COMMIT
--      - a new p_reset flag to force a full re-run from 0 when
--        you actually want that (e.g. after a schema change)
-- ============================================================
CREATE OR REPLACE PROCEDURE app.migrate_zeus_sales_from_working(
    IN p_batch_size integer DEFAULT 10000,
    IN p_reset boolean DEFAULT false
)
 LANGUAGE plpgsql
AS $procedure$
DECLARE
    v_last_id bigint := 0;
    v_batch_max_id bigint;
    v_batch_count bigint;
    v_total_processed bigint := 0;
    v_started_at timestamptz := clock_timestamp();
    v_batch_started_at timestamptz;
    v_proc_name constant text := 'migrate_zeus_sales_from_working';
BEGIN
    /*
     * ============================================================
     * ZEUS SALES BATCH MIGRATION (resumable)
     * ============================================================
     *
     * Source:
     *     app.zeus_sales_working
     *
     * Destination:
     *     app.zeus_sales
     *
     * Source cursor:
     *     zeus_sales_working.id, persisted in
     *     app.migration_checkpoints so separate CALLs resume
     *     instead of restarting.
     *
     * Destination conflict key:
     *     (_id, billingperiod_date)
     *
     * The source table is NOT modified.
     *
     * Each batch is committed independently, checkpoint included:
     *
     *     10,000 rows
     *          ↓
     *       UPSERT
     *          ↓
     *   UPDATE checkpoint
     *          ↓
     *       COMMIT
     *          ↓
     *     next 10,000 rows
     *
     * IMPORTANT:
     *     There is intentionally NO EXCEPTION block here.
     *
     *     PostgreSQL implements PL/pgSQL EXCEPTION blocks using
     *     subtransactions, and COMMIT cannot occur while a
     *     subtransaction is active.
     * ============================================================
     */

    IF p_batch_size IS NULL OR p_batch_size <= 0 THEN
        RAISE EXCEPTION
            'p_batch_size must be greater than zero';
    END IF;

    /*
     * ========================================================
     * RESOLVE STARTING POINT
     * ========================================================
     * p_reset = true forces a full re-run from the beginning
     * (e.g. after a schema/mapping change). Otherwise, pick up
     * exactly where the last CALL left off.
     */
    IF p_reset THEN
        v_last_id := 0;
        INSERT INTO app.migration_checkpoints (procedure_name, last_id, updated_at)
        VALUES (v_proc_name, 0, clock_timestamp())
        ON CONFLICT (procedure_name)
        DO UPDATE SET last_id = 0, updated_at = clock_timestamp();
    ELSE
        SELECT last_id INTO v_last_id
        FROM app.migration_checkpoints
        WHERE procedure_name = v_proc_name;

        IF NOT FOUND THEN
            v_last_id := 0;
            INSERT INTO app.migration_checkpoints (procedure_name, last_id)
            VALUES (v_proc_name, 0);
        END IF;
    END IF;

    RAISE NOTICE '==============================================';
    RAISE NOTICE 'ZEUS SALES BATCH MIGRATION';
    RAISE NOTICE '==============================================';
    RAISE NOTICE 'Batch size: %', p_batch_size;
    RAISE NOTICE 'Resuming from id: %', v_last_id;
    RAISE NOTICE 'Started: %', v_started_at;

    LOOP

        v_batch_started_at := clock_timestamp();

        /*
         * Find the highest ID in the next batch.
         *
         * The index:
         *
         *     idx_zeus_sales_working_id (id)
         *
         * allows PostgreSQL to efficiently locate the next
         * batch instead of scanning the entire 18.8M rows.
         */
        SELECT
            MAX(id),
            COUNT(*)
        INTO
            v_batch_max_id,
            v_batch_count
        FROM (
            SELECT id
            FROM app.zeus_sales_working
            WHERE id > v_last_id
            ORDER BY id
            LIMIT p_batch_size
        ) batch;

        /*
         * No more rows.
         */
        IF v_batch_count = 0 THEN
            EXIT;
        END IF;

        /*
         * ========================================================
         * UPSERT THE CURRENT BATCH
         * ========================================================
         */
        INSERT INTO app.zeus_sales (
            _id,
            bill,
            billtype,
            servicepoint,
            servicepointcode,
            servicepointstatus,
            tariffclass,
            tariffclasscode,
            tariffclassname,
            serviceclass,
            geocode,
            accountcode,
            metercode,
            metermodeltype,
            region,
            regioncode,
            regionname,
            district,
            districtcode,
            districtname,
            soename,
            mdaname,
            issensitive,
            customername,
            billconsumptionvalue,
            billconsumptionapparentvalue,
            billconsumptionmaxdemandvalue,
            billconsumptionexportvalue,
            billavgconsumptionvalue,
            billperiod,
            billconsumptiontype,
            outstandingamount,
            lifelineamount,
            firstthresholdamount,
            secondthresholdamount,
            thirdthresholdamount,
            energycharge,
            servicecharge,
            energyplusservicecharge,
            powerfactorsurcharge,
            vatcharge,
            nhilcharge,
            getfundcharge,
            streetlightcharge,
            nationalelectrificationcharge,
            billamount,
            paymentsamount,
            adjustmentconsumptionvalue,
            adjustmentamount,
            amountdue,
            debtamount,
            lastpaymentamount,
            lastpaymentdate,
            billingmonth,
            billingyear,
            billstatus,
            createdat,
            updatedat,
            __v,
            accounttype,
            billingperiod_date
        )
        SELECT
            _id,
            bill,
            billtype,
            servicepoint,
            servicepointcode,
            servicepointstatus,
            tariffclass,
            tariffclasscode,
            tariffclassname,
            serviceclass,
            geocode,
            accountcode,
            metercode,
            metermodeltype,
            region,
            regioncode,
            regionname,
            district,
            districtcode,
            districtname,
            soename,
            mdaname,
            issensitive,
            customername,
            billconsumptionvalue,
            billconsumptionapparentvalue,
            billconsumptionmaxdemandvalue,
            billconsumptionexportvalue,
            billavgconsumptionvalue,
            billperiod,
            billconsumptiontype,
            outstandingamount,
            lifelineamount,
            firstthresholdamount,
            secondthresholdamount,
            thirdthresholdamount,
            energycharge,
            servicecharge,
            energyplusservicecharge,
            powerfactorsurcharge,
            vatcharge,
            nhilcharge,
            getfundcharge,
            streetlightcharge,
            nationalelectrificationcharge,
            billamount,
            paymentsamount,
            adjustmentconsumptionvalue,
            adjustmentamount,
            amountdue,
            debtamount,
            lastpaymentamount,
            lastpaymentdate,
            billingmonth,
            billingyear,
            billstatus,
            createdat,
            updatedat,
            __v,
            accounttype,

            /*
             * First day of the billing month.
             */
            make_date(
                billingyear::integer,
                billingmonth::integer,
                1
            )

        FROM app.zeus_sales_working
        WHERE id > v_last_id
          AND id <= v_batch_max_id

        ON CONFLICT (_id, billingperiod_date)
        DO UPDATE SET
            billstatus        = EXCLUDED.billstatus,
            billamount        = EXCLUDED.billamount,
            amountdue         = EXCLUDED.amountdue,
            debtamount        = EXCLUDED.debtamount,
            outstandingamount = EXCLUDED.outstandingamount,
            paymentsamount    = EXCLUDED.paymentsamount,
            lastpaymentamount = EXCLUDED.lastpaymentamount,
            lastpaymentdate   = EXCLUDED.lastpaymentdate,
            updatedat         = EXCLUDED.updatedat;

        /*
         * The UPSERT succeeded.
         * Advance the cursor.
         */
        v_last_id := v_batch_max_id;
        v_total_processed := v_total_processed + v_batch_count;

        /*
         * ========================================================
         * PERSIST THE CHECKPOINT — in the SAME transaction as the
         * batch upsert above, so the checkpoint and the actual
         * migrated data are always in sync. If this UPDATE and the
         * COMMIT below never run (crash mid-batch), the whole
         * transaction rolls back together — batch upsert included
         * — so there's no way for the checkpoint to advance past
         * data that wasn't actually committed.
         * ========================================================
         */
        UPDATE app.migration_checkpoints
        SET last_id = v_last_id,
            updated_at = clock_timestamp()
        WHERE procedure_name = v_proc_name;

        /*
         * ========================================================
         * COMMIT THIS BATCH
         * ========================================================
         */
        COMMIT;

        /*
         * Report progress after the commit.
         */
        RAISE NOTICE
            'Batch complete | rows: % | total: % | last_id: % | batch time: %',
            v_batch_count,
            v_total_processed,
            v_last_id,
            clock_timestamp() - v_batch_started_at;

    END LOOP;

    RAISE NOTICE '==============================================';
    RAISE NOTICE 'MIGRATION COMPLETE';
    RAISE NOTICE '==============================================';
    RAISE NOTICE 'Total processed this run: %', v_total_processed;
    RAISE NOTICE 'Checkpoint (last_id): %', v_last_id;
    RAISE NOTICE 'Total elapsed: %',
        clock_timestamp() - v_started_at;

END;
$procedure$
;
