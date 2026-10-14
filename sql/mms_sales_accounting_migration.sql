-- Migrates MMS Prepaid consumption off the old MMS_SALES per-reading fact
-- feed onto the new app.mms_sales_accounting monthly fact feed.
--
-- WHY: MMS_SALES (sql/populate_mms_customer_sales_from_raw.sql) delivered
-- one row per meter PER READING, with STS credit-balance figures and a
-- genuine per-reading DATE_TIME, requiring the dedup machinery in
-- sql/mms_customer_sales_dedup.sql to cope with the ingestion process
-- periodically re-writing the same reading under a new DATE_TIME.
-- app.mms_sales_accounting is a different shape entirely: one row per
-- meter PER CALENDAR MONTH (year/month/monthyear), carrying `consumption`/
-- `rounded_consumption` and a new `consumption_band` bucket, but NO credit
-- balance fields at all (confirmed: that reporting capability is being
-- dropped, not replaced — see internal/mmssales model/service changes in
-- this same commit). It also already carries region/district/tariff/
-- manufacturer/model/installation_date itself, unlike MMS_SALES, which had
-- none of those and needed the join to app.mms_customer_meter for all of
-- them.
--
-- DECISION (confirmed): minimal-footprint migration. app.mms_customer_sales
-- keeps its name and existing columns — meter_number, account_number,
-- region, district, tariff, manufacturer, model, customer_name,
-- contract_code, contract_type, service dates, usage_point, geocode,
-- address, latitude, longitude, meter_serial_number, date_time,
-- is_duplicate_reading — so internal/mmssales/service.go's base queries,
-- zeus_prepaid_mms_precedence.sql's idx_mms_customer_sales_meter_number_norm
-- index, and every other existing consumer of this table need ZERO
-- changes to keep working. sts_last_month_kwh_read is REPOINTED to be
-- populated from the new `consumption` field (same column, new source) —
-- every existing SUM(sts_last_month_kwh_read) consumer (API, daily
-- summary, customer activity) keeps working unchanged. The two credit
-- columns (sts_credit_balance_remaining, sts_last_month_credit_read) are
-- left in the schema for historical rows but are always NULL for every
-- row this new pipeline inserts — SUM() ignores NULLs, so they just
-- silently contribute zero to the daily summary's credit columns rather
-- than erroring, and the Go/frontend layers stop reading them per this
-- same commit's other changes.
--
-- COEXISTENCE WITH HISTORICAL DATA: date_time for every row this pipeline
-- inserts is synthesized as the first-of-month date for that row's
-- monthyear (not a real reading timestamp — there isn't one anymore).
-- Old MMS_SALES-sourced rows keep their real per-reading date_time, which
-- only coincidentally equals a first-of-month date. In practice this means
-- old and new rows for the same meter essentially never collide on the
-- existing uq_meter_time (meter_number, date_time) unique constraint, so
-- historical data is left completely untouched — this migration is purely
-- additive going forward, not a rewrite of existing rows. If a correction
-- needs to restate an already-loaded month, re-running this procedure
-- naturally overwrites just that (meter_number, first-of-month) row via
-- ON CONFLICT DO UPDATE, which is also why the old dedup machinery
-- (resync_mms_duplicate_flags) is NOT needed for rows from this pipeline:
-- duplicates of the same (meter, month) can't occur by construction, the
-- upsert IS the dedup.
--
-- ASSUMPTIONS TO VERIFY BEFORE RUNNING (flagged per this repo's
-- convention — see populate_mms_customer_sales_from_raw.sql's own list):
--   1. The raw feed lands in `app` schema as app.mms_sales_accounting,
--      lowercase/unquoted columns, matching the sample JSON's keys
--      exactly (spn, meter_id, region, district, installation_date,
--      model, manufacturer, tariff, consumption, rounded_consumption,
--      year, month, monthyear, consumption_band). If the actual delivered
--      table differs, adjust the CREATE TABLE and the procedure's SELECT
--      list together.
--   2. spn = account number, meter_id = the same physical-meter identifier
--      previously called meter_number/meter_serial_number (confirmed).
--   3. Join key against app.mms_customer_meter: meter_id = meter_number,
--      matched lower(trim(...)) both sides, same defensive pattern as
--      populate_mms_customer_sales_from_raw.sql's join.
--   4. installation_date arrives as a real date/parseable date string
--      ("2024-05-25" in the sample) — cast directly; adjust if it turns
--      out to be text in a different format.
--   5. region/district arrive with incidental trailing whitespace in the
--      sample data ("Accra West ", "Bortianor" without) — trimmed on
--      insert, same as every other legacy source in this app.

-- ============================================================
-- 1. Raw landing table for the new feed. Ingestion mechanism (external
--    direct write vs this app's ETL engine) is not yet decided — this
--    table exists either way; it's what the procedure below reads from.
--    Unique on (meter_id, monthyear): one row per meter per month, so a
--    re-delivered/corrected month overwrites in place rather than
--    duplicating.
-- ============================================================
CREATE TABLE IF NOT EXISTS app.mms_sales_accounting (
    spn                 text,
    meter_id            text NOT NULL,
    region              text,
    district            text,
    installation_date   date,
    model               text,
    manufacturer        text,
    tariff              text,
    consumption         double precision,
    rounded_consumption double precision,
    year                text,
    month               text,
    monthyear           text NOT NULL,
    consumption_band    text,
    loaded_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_mms_sales_accounting_meter_month UNIQUE (meter_id, monthyear)
);

CREATE INDEX IF NOT EXISTS idx_mms_sales_accounting_monthyear
    ON app.mms_sales_accounting (monthyear);
CREATE INDEX IF NOT EXISTS idx_mms_sales_accounting_meter_id
    ON app.mms_sales_accounting (meter_id);

-- ============================================================
-- 2. Widen app.mms_customer_sales (additive only — nothing dropped,
--    nothing renamed, every existing column/index/constraint untouched).
-- ============================================================
ALTER TABLE app.mms_customer_sales
    ADD COLUMN IF NOT EXISTS consumption         double precision,
    ADD COLUMN IF NOT EXISTS rounded_consumption double precision,
    ADD COLUMN IF NOT EXISTS consumption_band    text,
    ADD COLUMN IF NOT EXISTS year                text,
    ADD COLUMN IF NOT EXISTS month               text,
    ADD COLUMN IF NOT EXISTS monthyear           text;

CREATE INDEX IF NOT EXISTS idx_mms_customer_sales_lower_consumption_band
    ON app.mms_customer_sales (lower(consumption_band));
CREATE INDEX IF NOT EXISTS idx_mms_customer_sales_monthyear
    ON app.mms_customer_sales (monthyear);

-- ============================================================
-- 3. The population procedure. Resumable, same checkpoint table and
--    batching discipline as populate_mms_customer_sales_from_raw.sql,
--    reusing app.migration_checkpoints (last_text holds a composite
--    "monthyear\x01meter_id" sort key — monthyear is always a fixed
--    6-character YYYYMM string, so concatenation preserves the same
--    ordering as comparing the (monthyear, meter_id) tuple directly, and
--    the \x01 separator can't collide with any real meter_id).
-- ============================================================
CREATE OR REPLACE PROCEDURE app.populate_mms_customer_sales_accounting(
    IN p_batch_size integer DEFAULT 10000,
    IN p_reset boolean DEFAULT false
)
 LANGUAGE plpgsql
AS $procedure$
DECLARE
    v_last_key        text;
    v_batch_min_key   text;
    v_batch_max_key   text;
    v_batch_count     bigint;
    v_total_processed bigint := 0;
    v_started_at      timestamptz := clock_timestamp();
    v_batch_started_at timestamptz;
    v_proc_name constant text := 'populate_mms_customer_sales_accounting';
BEGIN
    IF p_batch_size IS NULL OR p_batch_size <= 0 THEN
        RAISE EXCEPTION 'p_batch_size must be greater than zero';
    END IF;

    IF p_reset THEN
        v_last_key := '';
        INSERT INTO app.migration_checkpoints (procedure_name, last_text, updated_at)
        VALUES (v_proc_name, v_last_key, clock_timestamp())
        ON CONFLICT (procedure_name)
        DO UPDATE SET last_text = v_last_key, updated_at = clock_timestamp();
    ELSE
        SELECT last_text INTO v_last_key
        FROM app.migration_checkpoints
        WHERE procedure_name = v_proc_name;

        IF NOT FOUND THEN
            v_last_key := '';
            INSERT INTO app.migration_checkpoints (procedure_name, last_text)
            VALUES (v_proc_name, v_last_key);
        ELSE
            v_last_key := COALESCE(v_last_key, '');
        END IF;
    END IF;

    RAISE NOTICE '==============================================';
    RAISE NOTICE 'MMS SALES ACCOUNTING POPULATION';
    RAISE NOTICE '==============================================';
    RAISE NOTICE 'Batch size: %', p_batch_size;
    RAISE NOTICE 'Resuming after key: %', v_last_key;
    RAISE NOTICE 'Started: %', v_started_at;

    LOOP
        v_batch_started_at := clock_timestamp();

        WITH batch AS (
            SELECT s.monthyear || chr(1) || s.meter_id AS sort_key
            FROM app.mms_sales_accounting s
            WHERE (s.monthyear || chr(1) || s.meter_id) > v_last_key
            ORDER BY s.monthyear, s.meter_id
            LIMIT p_batch_size
        )
        SELECT COUNT(*), MIN(sort_key), MAX(sort_key)
        INTO v_batch_count, v_batch_min_key, v_batch_max_key
        FROM batch;

        IF v_batch_count = 0 THEN
            EXIT;
        END IF;

        INSERT INTO app.mms_customer_sales (
            meter_number,
            meter_serial_number,
            account_number,
            region,
            district,
            tariff,
            manufacturer,
            model,
            installation_date,
            removal_date,
            customer_name,
            contract_code,
            contract_type,
            service_commencement_date,
            service_termination_date,
            usage_point,
            geocode,
            address,
            latitude,
            longitude,
            consumption,
            rounded_consumption,
            consumption_band,
            year,
            month,
            monthyear,
            sts_last_month_kwh_read,
            sts_credit_balance_remaining,
            sts_last_month_credit_read,
            date_time,
            is_duplicate_reading
        )
        SELECT
            d.meter_id,
            d.meter_id,
            COALESCE(NULLIF(trim(d.spn), ''), m.account_number),
            trim(d.region),
            trim(d.district),
            d.tariff,
            d.manufacturer,
            d.model,
            d.installation_date,
            m.removal_date,
            m.customer_name,
            m.contract_code,
            m.contract_type,
            m.service_commencement_date,
            m.service_termination_date,
            m.usage_point,
            m.geocode,
            m.address,
            m.latitude::float8,
            m.longitude::float8,
            d.consumption,
            d.rounded_consumption,
            d.consumption_band,
            d.year,
            d.month,
            d.monthyear,
            d.consumption,                                   -- sts_last_month_kwh_read repointed to the new measure
            NULL,                                             -- sts_credit_balance_remaining: no longer delivered
            NULL,                                             -- sts_last_month_credit_read: no longer delivered
            to_date(d.monthyear, 'YYYYMM'),                   -- synthesized first-of-month date_time
            false
        FROM (
            -- DISTINCT ON collapses duplicate (meter_id, monthyear) rows
            -- within this batch's window to exactly one each BEFORE the
            -- INSERT — live data confirmed to contain them (23M+ raw rows
            -- with no unique constraint enforced on the source table), and
            -- without this, ON CONFLICT DO UPDATE fails outright with
            -- "ON CONFLICT DO UPDATE command cannot affect row a second
            -- time" the moment two source rows for the same meter+month
            -- land in one batch. No reliable "latest wins" signal exists
            -- on the source table (no load timestamp column) — ctid DESC
            -- is a deterministic, if arbitrary, tiebreaker (effectively
            -- "last physically loaded"). Flag if a better tiebreaker
            -- (e.g. an actual load sequence/timestamp column) becomes
            -- available upstream.
            SELECT DISTINCT ON (s.meter_id, s.monthyear) s.*
            FROM app.mms_sales_accounting s
            WHERE (s.monthyear || chr(1) || s.meter_id) > v_last_key
              AND (s.monthyear || chr(1) || s.meter_id) <= v_batch_max_key
            ORDER BY s.meter_id, s.monthyear, s.ctid DESC
        ) d
        LEFT JOIN app.mms_customer_meter m
            ON lower(trim(m.meter_number)) = lower(trim(d.meter_id))
        ON CONFLICT (meter_number, date_time) DO UPDATE SET
            account_number       = EXCLUDED.account_number,
            region               = EXCLUDED.region,
            district             = EXCLUDED.district,
            tariff               = EXCLUDED.tariff,
            manufacturer         = EXCLUDED.manufacturer,
            model                = EXCLUDED.model,
            installation_date    = EXCLUDED.installation_date,
            consumption          = EXCLUDED.consumption,
            rounded_consumption  = EXCLUDED.rounded_consumption,
            consumption_band     = EXCLUDED.consumption_band,
            year                 = EXCLUDED.year,
            month                = EXCLUDED.month,
            monthyear            = EXCLUDED.monthyear,
            sts_last_month_kwh_read = EXCLUDED.sts_last_month_kwh_read;

        v_last_key := v_batch_max_key;
        v_total_processed := v_total_processed + v_batch_count;

        -- Keep the daily summary in sync for this batch's FULL touched
        -- month range — a batch ordered by (monthyear, meter_id) can span
        -- a month boundary, so this must cover from the batch's minimum
        -- key's month through its maximum key's month, not just the
        -- last one, or rows from the batch's earlier month(s) would never
        -- get resynced. Same per-batch-before-commit contract as
        -- populate_mms_customer_sales_from_raw.sql. Safe to call
        -- unconditionally — resync_mms_sales_summary already tolerates
        -- being re-run over the same range.
        PERFORM app.resync_mms_sales_summary(
            to_date(split_part(v_batch_min_key, chr(1), 1), 'YYYYMM'),
            (to_date(split_part(v_batch_max_key, chr(1), 1), 'YYYYMM') + interval '1 month' - interval '1 day')::date
        );

        UPDATE app.migration_checkpoints
        SET last_text = v_last_key,
            updated_at = clock_timestamp()
        WHERE procedure_name = v_proc_name;

        COMMIT;

        RAISE NOTICE 'Batch complete | rows: % | total: % | cursor: % | batch time: %',
            v_batch_count, v_total_processed, v_last_key, clock_timestamp() - v_batch_started_at;
    END LOOP;

    RAISE NOTICE '==============================================';
    RAISE NOTICE 'POPULATION COMPLETE';
    RAISE NOTICE '==============================================';
    RAISE NOTICE 'Total processed this run: %', v_total_processed;
    RAISE NOTICE 'Checkpoint cursor: %', v_last_key;
    RAISE NOTICE 'Total elapsed: %', clock_timestamp() - v_started_at;
END;
$procedure$
;

-- Run after loading new rows into app.mms_sales_accounting:
--   CALL app.populate_mms_customer_sales_accounting();
-- First run processes everything currently in the table; later runs
-- resume from the checkpoint, so re-running after a fresh monthly load
-- only processes that load's rows. To force a full reprocess (e.g. after
-- a correction to historical months), reset the checkpoint:
--   CALL app.populate_mms_customer_sales_accounting(p_reset => true);
