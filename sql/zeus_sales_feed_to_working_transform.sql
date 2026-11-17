-- Step 3 of adopting the new Zeus billing feed: moves rows from
-- app.zeus_sales_feed (step 2) into the existing app.zeus_sales_working,
-- in the shape it already has (step 1 added the only new columns it
-- needed). Everything downstream — app.migrate_zeus_sales_from_working,
-- app.zeus_sales, the Go API — is completely untouched by this file.
--
-- WHY NO ON CONFLICT (unlike the MMS migration): app.zeus_sales_working
-- has no unique constraint at all, by design — restaging a bill is
-- supposed to land as a brand-new row, not overwrite one, and the
-- existing migrate_zeus_sales_from_working is what later consolidates
-- those rows (via its own DISTINCT ON, keyed on (_id, billingperiod_date)
-- ordered by id DESC) when moving into app.zeus_sales. This procedure
-- mirrors that same append-only contract: every row from the new feed
-- becomes a brand-new row in zeus_sales_working, full stop. No dedup
-- logic needed here — duplicate work downstream is harmless and already
-- handled there.
--
-- THE id COLUMN NEEDS CARE even so: zeus_sales_working.id (int8) has no
-- default — nothing in Postgres auto-generates it. The EXISTING
-- migrate_zeus_sales_from_working dedups same-bill duplicates via
-- "ORDER BY _id, billingperiod_date, id DESC", picking the highest id as
-- "most recently staged." Leaving id NULL for new rows would make them
-- sort FIRST under a DESC order (Postgres's default null ordering) and
-- incorrectly win that tiebreak over real, older data. So this procedure
-- assigns real, strictly-increasing id values itself: it reads the
-- current MAX(id) once before the loop starts, then hands out
-- sequential values continuing from there, in (loaded_at, id) order — so
-- a more-recently-LOADED bill always gets a higher id, preserving the
-- "highest id = most recent" property the downstream dedup relies on.
-- This is actually a better match for that property's intent than the
-- original (updated_at, "_id") order was: "most recently staged" should
-- mean most recently loaded, not whatever timestamp happens to be on the
-- bill's own content — a backfilled reload of an old bill is a fresher
-- version of it and should win the tiebreak, regardless of its content's
-- own updated_at.
--
-- ASSUMPTION worth flagging: this only holds if nothing else is
-- concurrently inserting into zeus_sales_working while this runs (e.g.
-- the old zeus-sales-fetch ETL job still active in parallel) — two
-- writers computing their own "current MAX(id) + N" independently can
-- collide. Expected to be fine given the old feed is being retired, not
-- run alongside this one, but flagging it rather than assuming silently.
--
-- CURSOR: (loaded_at, id) keyset pagination — loaded_at (zeus_sales_feed's
-- own bookkeeping column, stamped by Postgres at INSERT time into THIS
-- table) not updated_at (the feed's source-side "when Zeus last touched
-- this record" timestamp). Originally built on (updated_at, "_id")
-- instead, which seemed like the more "meaningful" choice at the time —
-- but it broke the very first time a backfill for an EARLIER billing
-- period was loaded after a LATER one had already been processed: the
-- backfilled rows' updated_at was older than the checkpoint, so the
-- ">" comparison silently skipped every one of them (confirmed live:
-- 3.19M September rows sitting below an October checkpoint, "0
-- processed" with no error). loaded_at doesn't have this failure mode —
-- any INSERT, whether live current-month data or a backfill for a
-- period years in the past, happens "now," so it's always greater than
-- whatever the checkpoint currently is, regardless of which billing
-- period the data is actually for. id is zeus_sales_feed's own uuid PK
-- (globally unique, real primary key) — not ordered in any meaningful
-- sense (gen_random_uuid()), but that's fine here: it only needs to
-- break ties when two rows share the same loaded_at (realistic under a
-- bulk load, since loaded_at's DEFAULT now() is the transaction-start
-- timestamp, constant across one bulk INSERT statement), and any total
-- order works for that, meaningful or not.
CREATE OR REPLACE PROCEDURE app.migrate_zeus_sales_feed_to_working(
    IN p_batch_size integer DEFAULT 10000,
    IN p_reset boolean DEFAULT false
)
 LANGUAGE plpgsql
AS $procedure$
DECLARE
    v_last_loaded_at      timestamptz;
    v_last_id             uuid;
    v_batch_max_loaded_at timestamptz;
    v_batch_max_id        uuid;
    v_batch_count          bigint;
    v_next_id              bigint;
    v_total_processed      bigint := 0;
    v_started_at           timestamptz := clock_timestamp();
    v_batch_started_at     timestamptz;
    v_proc_name constant text := 'migrate_zeus_sales_feed_to_working';
    v_nil_uuid constant uuid := '00000000-0000-0000-0000-000000000000';
BEGIN
    IF p_batch_size IS NULL OR p_batch_size <= 0 THEN
        RAISE EXCEPTION 'p_batch_size must be greater than zero';
    END IF;

    IF p_reset THEN
        v_last_loaded_at := '-infinity';
        v_last_id := v_nil_uuid;
        INSERT INTO app.migration_checkpoints (procedure_name, last_timestamp, last_text, updated_at)
        VALUES (v_proc_name, v_last_loaded_at, v_last_id::text, clock_timestamp())
        ON CONFLICT (procedure_name)
        DO UPDATE SET last_timestamp = v_last_loaded_at, last_text = v_last_id::text, updated_at = clock_timestamp();
    ELSE
        SELECT last_timestamp, last_text::uuid INTO v_last_loaded_at, v_last_id
        FROM app.migration_checkpoints
        WHERE procedure_name = v_proc_name;

        IF NOT FOUND THEN
            v_last_loaded_at := '-infinity';
            v_last_id := v_nil_uuid;
            INSERT INTO app.migration_checkpoints (procedure_name, last_timestamp, last_text)
            VALUES (v_proc_name, v_last_loaded_at, v_last_id::text);
        ELSE
            v_last_loaded_at := COALESCE(v_last_loaded_at, '-infinity');
            v_last_id := COALESCE(v_last_id, v_nil_uuid);
        END IF;
    END IF;

    SELECT COALESCE(MAX(id), 0) INTO v_next_id FROM app.zeus_sales_working;

    RAISE NOTICE '==============================================';
    RAISE NOTICE 'ZEUS SALES FEED -> WORKING';
    RAISE NOTICE '==============================================';
    RAISE NOTICE 'Batch size: %', p_batch_size;
    RAISE NOTICE 'Resuming after (loaded_at, id): (%, %)', v_last_loaded_at, v_last_id;
    RAISE NOTICE 'Continuing zeus_sales_working.id from: %', v_next_id;
    RAISE NOTICE 'Started: %', v_started_at;

    LOOP
        v_batch_started_at := clock_timestamp();

        WITH batch AS (
            SELECT f.loaded_at, f.id
            FROM app.zeus_sales_feed f
            WHERE (f.loaded_at, f.id) > (v_last_loaded_at, v_last_id)
            ORDER BY f.loaded_at, f.id
            LIMIT p_batch_size
        )
        SELECT
            COUNT(*),
            (SELECT loaded_at FROM batch ORDER BY loaded_at DESC, id DESC LIMIT 1),
            (SELECT id FROM batch ORDER BY loaded_at DESC, id DESC LIMIT 1)
        INTO v_batch_count, v_batch_max_loaded_at, v_batch_max_id
        FROM batch;

        IF v_batch_count = 0 THEN
            EXIT;
        END IF;

        INSERT INTO app.zeus_sales_working (
            id,
            "_id",
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
            "__v",
            accounttype,
            sltstatus,
            accountgroups,
            debtage,
            bills
        )
        SELECT
            v_next_id + (ROW_NUMBER() OVER (ORDER BY f.loaded_at, f.id)),
            f."_id",
            f.bill,
            f.bill_type,
            f.service_point,
            f.service_point_code,
            f.service_point_status,
            f.tariff_class,
            f.tariff_class_code,
            f.tariff_class_name,
            f.service_class,
            f.geo_code,
            f.account_code,
            f.meter_code,
            f.meter_model_type,
            f.region,
            f.region_code,
            f.region_name,
            f.district,
            f.district_code,
            f.district_name,
            f.soe_name,
            f.mda_name,
            f.is_sensitive,
            f.customer_name,
            f.bill_consumption_value,
            f.bill_consumption_apparent_value,
            f.bill_consumption_max_demand_value,
            f.bill_consumption_export_value,
            f.bill_avg_consumption_value,
            f.bill_period,
            f.bill_consumption_type,
            f.outstanding_amount,
            f.life_line_amount,
            f.first_threshold_amount,
            f.second_threshold_amount,
            f.third_threshold_amount,
            f.energy_charge,
            f.service_charge,
            f.energy_plus_service_charge,
            f.power_factor_surcharge,
            f.vat_charge,
            f.nhil_charge,
            f.getfund_charge,
            f.street_light_charge,
            f.national_electrification_charge,
            f.bill_amount,
            f.payments_amount,
            f.adjustment_consumption_value,
            f.adjustment_amount,
            f.amount_due,
            f.debt_amount,
            f.last_payment_amount,
            f.last_payment_date,
            f.month,
            f.year,
            f.bill_status,
            f.created_at,
            f.updated_at,
            f."__v",
            f.account_type,
            f.slt_status,
            f.account_groups,
            f.debtage,
            f.bills
        FROM app.zeus_sales_feed f
        WHERE (f.loaded_at, f.id) > (v_last_loaded_at, v_last_id)
          AND (f.loaded_at, f.id) <= (v_batch_max_loaded_at, v_batch_max_id);

        v_next_id := v_next_id + v_batch_count;
        v_last_loaded_at := v_batch_max_loaded_at;
        v_last_id := v_batch_max_id;
        v_total_processed := v_total_processed + v_batch_count;

        UPDATE app.migration_checkpoints
        SET last_timestamp = v_last_loaded_at,
            last_text = v_last_id::text,
            updated_at = clock_timestamp()
        WHERE procedure_name = v_proc_name;

        COMMIT;

        RAISE NOTICE 'Batch complete | rows: % | total: % | cursor: (%, %) | next id: % | batch time: %',
            v_batch_count, v_total_processed, v_last_loaded_at, v_last_id, v_next_id, clock_timestamp() - v_batch_started_at;
    END LOOP;

    RAISE NOTICE '==============================================';
    RAISE NOTICE 'TRANSFORM COMPLETE';
    RAISE NOTICE '==============================================';
    RAISE NOTICE 'Total processed this run: %', v_total_processed;
    RAISE NOTICE 'Checkpoint cursor: (%, %)', v_last_loaded_at, v_last_id;
    RAISE NOTICE 'Total elapsed: %', clock_timestamp() - v_started_at;
END;
$procedure$
;

-- Run after loading new rows into app.zeus_sales_feed:
--   CALL app.migrate_zeus_sales_feed_to_working();
-- Resumable — a later run only processes rows newer than the checkpoint.
-- To force a full reprocess:
--   CALL app.migrate_zeus_sales_feed_to_working(p_reset => true);
--
-- Afterward, run the existing (unchanged) migration to carry the new
-- rows from zeus_sales_working into zeus_sales, exactly as today:
--   CALL app.migrate_zeus_sales_from_working();
