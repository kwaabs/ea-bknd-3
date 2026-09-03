-- Populates app.mms_customer_sales by joining two raw source tables:
--
--   app.mms_customer_meter  — one row per physical meter (dimension):
--     meter_number, manufacturer, model, installation_date, removal_date,
--     customer_name, contract_code, contract_type,
--     service_commencement_date, service_termination_date, account_number,
--     tariff, usage_point, geocode, region, district, address, latitude,
--     longitude.
--
--   app."MMS_SALES"         — one row per meter PER PERIODIC READING
--     (fact, far larger than the meter table): METER_SERIAL_NUMBER,
--     STS_CREDIT_BALANCE_REMAINING, STS_LAST_MONTH_CREDIT_READ,
--     STS_LAST_MONTH_KWH_READ, DATE_TIME.
--
-- ASSUMPTIONS TO VERIFY BEFORE RUNNING — flagged explicitly because a
-- wrong one here means silently wrong data in a table the dashboards
-- already read from, not just a query error:
--   1. Both raw tables live in the `app` schema. mms_customer_meter's
--      columns are assumed lowercase/unquoted (standard Postgres); MMS_SALES's
--      table name AND column names are assumed to need double-quoting to
--      match the exact casing shown in the sample data
--      ("METER_SERIAL_NUMBER" etc.) — if MMS_SALES's actual columns turn
--      out to be plain lowercase, drop the quotes.
--   2. Join key: mms_customer_meter.meter_number = MMS_SALES.METER_SERIAL_NUMBER,
--      matched case-insensitively and trimmed (lower(trim(...)) both
--      sides) — Zeus/MMS/BOT/etc. have all had case/whitespace mismatches
--      between systems elsewhere in this app, so this defends against the
--      same class of issue here without assuming it won't happen.
--   3. LEFT JOIN (confirmed): a MMS_SALES reading with no matching meter
--      record still gets inserted, with all meter-dimension columns NULL,
--      rather than being silently dropped.
--   4. CONFIRMED LIVE: MMS_SALES."DATE_TIME" is stored as character
--      varying (text), not a native timestamp — every read/comparison of
--      it below casts explicitly with ::timestamptz. If it turns out to
--      already be a real timestamp/timestamptz column on your database,
--      the cast is a harmless no-op; it's only load-bearing in the
--      varchar case.
--
-- Batching note: this is periodic snapshot data — plausibly millions of
-- rows share the exact same DATE_TIME (one reading per meter per period,
-- for the whole fleet at once). A plain "take N rows ordered by DATE_TIME,
-- advance cursor to MAX(DATE_TIME)" approach (the pattern used in
-- migrate_zeus_sales_from_working_resumable.sql) would risk splitting one
-- DATE_TIME's rows across two batches and having the next batch's
-- `DATE_TIME > last_checkpoint` filter skip the remainder — since a
-- strict `>` on a non-unique value drops any sibling rows that share it
-- but didn't make the previous LIMIT. Fixed here with keyset pagination on
-- the full (DATE_TIME, METER_SERIAL_NUMBER) tuple instead of a single
-- column: the WHERE clause compares the whole tuple, so ties on DATE_TIME
-- are never skipped or duplicated regardless of where a batch boundary
-- falls within them.

-- ============================================================
-- 1. Generalize the checkpoint table (added in
--    migrate_zeus_sales_from_working_resumable.sql) with two more cursor
--    columns, so a tuple-keyset procedure like this one can use it too —
--    Zeus's procedure keeps using last_id and ignores these; this one uses
--    last_timestamp + last_text and ignores last_id.
-- ============================================================
CREATE TABLE IF NOT EXISTS app.migration_checkpoints (
    procedure_name text PRIMARY KEY,
    last_id        bigint NOT NULL DEFAULT 0,
    last_timestamp timestamptz NULL,
    last_text      text NULL,
    updated_at     timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE app.migration_checkpoints ADD COLUMN IF NOT EXISTS last_timestamp timestamptz NULL;
ALTER TABLE app.migration_checkpoints ADD COLUMN IF NOT EXISTS last_text text NULL;

-- ============================================================
-- 2. The population procedure.
-- ============================================================
CREATE OR REPLACE PROCEDURE app.populate_mms_customer_sales(
    IN p_batch_size integer DEFAULT 10000,
    IN p_reset boolean DEFAULT false
)
 LANGUAGE plpgsql
AS $procedure$
DECLARE
    v_last_dt      timestamptz := '-infinity';
    v_last_meter   text := '';
    v_batch_max_dt timestamptz;
    v_batch_max_meter text;
    v_batch_min_dt timestamptz;
    v_batch_count  bigint;
    v_total_processed bigint := 0;
    v_run_min_dt   timestamptz := NULL;
    v_run_max_dt   timestamptz := NULL;
    v_started_at   timestamptz := clock_timestamp();
    v_batch_started_at timestamptz;
    v_proc_name constant text := 'populate_mms_customer_sales';
BEGIN
    /*
     * ============================================================
     * MMS CUSTOMER SALES POPULATION (resumable)
     * ============================================================
     *
     * Sources:
     *     app.mms_customer_meter (dimension)
     *     app."MMS_SALES"        (periodic fact — far larger, grows
     *                              indefinitely)
     *
     * Destination:
     *     app.mms_customer_sales
     *
     * Source cursor:
     *     keyset pagination on (MMS_SALES."DATE_TIME",
     *     MMS_SALES."METER_SERIAL_NUMBER"), persisted in
     *     app.migration_checkpoints so separate CALLs resume instead of
     *     restarting, exactly like migrate_zeus_sales_from_working.
     *
     * Neither source table is modified. This is a straight append into
     * app.mms_customer_sales — that table has no unique constraint to
     * upsert against (confirmed in mms_customer_sales_dedup.sql), so
     * there is no ON CONFLICT here. Re-running this procedure without
     * resetting the checkpoint will not re-insert rows already
     * processed; it only picks up rows added to MMS_SALES since the
     * last run.
     *
     * After the loop, resync_mms_duplicate_flags and
     * resync_mms_sales_summary (sql/mms_customer_sales_dedup.sql) are
     * called ONCE for the whole run's date range — not per batch, which
     * would be far more expensive than necessary — so newly inserted
     * rows are correctly deduped and reflected in the fast aggregate
     * path without a separate manual step.
     * ============================================================
     */

    IF p_batch_size IS NULL OR p_batch_size <= 0 THEN
        RAISE EXCEPTION 'p_batch_size must be greater than zero';
    END IF;

    IF p_reset THEN
        v_last_dt := '-infinity';
        v_last_meter := '';
        INSERT INTO app.migration_checkpoints (procedure_name, last_timestamp, last_text, updated_at)
        VALUES (v_proc_name, v_last_dt, v_last_meter, clock_timestamp())
        ON CONFLICT (procedure_name)
        DO UPDATE SET last_timestamp = v_last_dt, last_text = v_last_meter, updated_at = clock_timestamp();
    ELSE
        SELECT last_timestamp, last_text INTO v_last_dt, v_last_meter
        FROM app.migration_checkpoints
        WHERE procedure_name = v_proc_name;

        IF NOT FOUND THEN
            v_last_dt := '-infinity';
            v_last_meter := '';
            INSERT INTO app.migration_checkpoints (procedure_name, last_timestamp, last_text)
            VALUES (v_proc_name, v_last_dt, v_last_meter);
        ELSE
            v_last_dt := COALESCE(v_last_dt, '-infinity');
            v_last_meter := COALESCE(v_last_meter, '');
        END IF;
    END IF;

    RAISE NOTICE '==============================================';
    RAISE NOTICE 'MMS CUSTOMER SALES POPULATION';
    RAISE NOTICE '==============================================';
    RAISE NOTICE 'Batch size: %', p_batch_size;
    RAISE NOTICE 'Resuming after (date_time, meter): (%, %)', v_last_dt, v_last_meter;
    RAISE NOTICE 'Started: %', v_started_at;

    LOOP

        v_batch_started_at := clock_timestamp();

        /*
         * Find this batch's boundary — the (date_time, meter) tuple of
         * the last row in keyset order within the next p_batch_size
         * rows. MAX(meter) FILTER (WHERE dt = the batch's max dt)
         * correctly resolves ties: if several rows share the maximal
         * date_time reached by this batch, the true boundary is the
         * largest meter among just those, not an arbitrary one.
         */
        WITH batch AS (
            SELECT s."DATE_TIME"::timestamptz AS dt, s."METER_SERIAL_NUMBER" AS meter
            FROM app."MMS_SALES" s
            WHERE (s."DATE_TIME"::timestamptz, s."METER_SERIAL_NUMBER") > (v_last_dt, v_last_meter)
            ORDER BY s."DATE_TIME"::timestamptz, s."METER_SERIAL_NUMBER"
            LIMIT p_batch_size
        )
        SELECT
            COUNT(*),
            MIN(dt),
            MAX(dt),
            MAX(meter) FILTER (WHERE dt = (SELECT MAX(dt) FROM batch))
        INTO
            v_batch_count,
            v_batch_min_dt,
            v_batch_max_dt,
            v_batch_max_meter
        FROM batch;

        IF v_batch_count = 0 THEN
            EXIT;
        END IF;

        /*
         * ========================================================
         * INSERT THE CURRENT BATCH (left join — an MMS_SALES reading
         * with no matching meter record still gets inserted, with
         * meter-dimension columns NULL, per explicit direction).
         * ========================================================
         */
        INSERT INTO app.mms_customer_sales (
            meter_number,
            manufacturer,
            model,
            installation_date,
            removal_date,
            customer_name,
            contract_code,
            contract_type,
            service_commencement_date,
            service_termination_date,
            account_number,
            tariff,
            usage_point,
            geocode,
            region,
            district,
            address,
            latitude,
            longitude,
            meter_serial_number,
            sts_credit_balance_remaining,
            sts_last_month_credit_read,
            sts_last_month_kwh_read,
            date_time
        )
        SELECT
            m.meter_number,
            m.manufacturer,
            m.model,
            m.installation_date,
            m.removal_date,
            m.customer_name,
            m.contract_code,
            m.contract_type,
            m.service_commencement_date,
            m.service_termination_date,
            m.account_number,
            m.tariff,
            m.usage_point,
            m.geocode,
            m.region,
            m.district,
            m.address,
            m.latitude::float8,
            m.longitude::float8,
            s."METER_SERIAL_NUMBER",
            s."STS_CREDIT_BALANCE_REMAINING",
            s."STS_LAST_MONTH_CREDIT_READ",
            s."STS_LAST_MONTH_KWH_READ",
            s."DATE_TIME"::timestamptz
        FROM app."MMS_SALES" s
        LEFT JOIN app.mms_customer_meter m
            ON lower(trim(m.meter_number)) = lower(trim(s."METER_SERIAL_NUMBER"))
        WHERE (s."DATE_TIME"::timestamptz, s."METER_SERIAL_NUMBER") > (v_last_dt, v_last_meter)
          AND (s."DATE_TIME"::timestamptz, s."METER_SERIAL_NUMBER") <= (v_batch_max_dt, v_batch_max_meter);

        /*
         * Advance the cursor.
         */
        v_last_dt := v_batch_max_dt;
        v_last_meter := v_batch_max_meter;
        v_total_processed := v_total_processed + v_batch_count;

        v_run_min_dt := LEAST(COALESCE(v_run_min_dt, v_batch_min_dt), v_batch_min_dt);
        v_run_max_dt := GREATEST(COALESCE(v_run_max_dt, v_batch_max_dt), v_batch_max_dt);

        /*
         * ========================================================
         * PERSIST THE CHECKPOINT — same transaction as the batch
         * insert above, right before COMMIT, for the same reason as
         * migrate_zeus_sales_from_working_resumable.sql: if this
         * UPDATE and the COMMIT never run, the whole transaction
         * (insert included) rolls back together.
         * ========================================================
         */
        UPDATE app.migration_checkpoints
        SET last_timestamp = v_last_dt,
            last_text = v_last_meter,
            updated_at = clock_timestamp()
        WHERE procedure_name = v_proc_name;

        COMMIT;

        RAISE NOTICE
            'Batch complete | rows: % | total: % | cursor: (%, %) | batch time: %',
            v_batch_count,
            v_total_processed,
            v_last_dt,
            v_last_meter,
            clock_timestamp() - v_batch_started_at;

    END LOOP;

    /*
     * ========================================================
     * RESYNC dedup flags and the daily summary ONCE for the whole
     * run's date range — see sql/mms_customer_sales_dedup.sql. Skipped
     * entirely if this run inserted nothing (v_run_min_dt still NULL).
     * ========================================================
     */
    IF v_run_min_dt IS NOT NULL THEN
        RAISE NOTICE 'Resyncing duplicate flags and daily summary for % .. %',
            v_run_min_dt::date, v_run_max_dt::date;
        PERFORM app.resync_mms_duplicate_flags(v_run_min_dt::date, v_run_max_dt::date);
        PERFORM app.resync_mms_sales_summary(v_run_min_dt::date, v_run_max_dt::date);
        COMMIT;
    END IF;

    RAISE NOTICE '==============================================';
    RAISE NOTICE 'POPULATION COMPLETE';
    RAISE NOTICE '==============================================';
    RAISE NOTICE 'Total processed this run: %', v_total_processed;
    RAISE NOTICE 'Checkpoint cursor: (%, %)', v_last_dt, v_last_meter;
    RAISE NOTICE 'Total elapsed: %', clock_timestamp() - v_started_at;

END;
$procedure$
;
