-- Step 2 of adopting the new Zeus billing feed shape: the holding table
-- the new source lands fresh data into, one row per JSON record, before
-- a transform step (step 3, not this file) moves it into the existing
-- app.zeus_sales_working — which then flows into app.zeus_sales exactly
-- as it already does today via app.migrate_zeus_sales_from_working,
-- completely untouched.
--
-- Column names: snake_case throughout (bill_consumption_value, not
-- billconsumptionvalue) — this is a brand-new table, not bound to
-- zeus_sales_working's older no-underscore convention. "_id" and "__v"
-- keep their literal source names (quoted identifiers) for direct
-- traceability back to the feed's own field names, same as
-- zeus_sales_working already does for both.
--
-- id: a generated uuid, serving the same row-identity role the old
-- zeus_sales_working.id (int8) played — NOT used as the resumable
-- migration cursor (see below for why).
--
-- CURSOR DECISION (important, discussed before building this): a random
-- uuid cannot serve as an incremental cursor BY ITSELF the way the old
-- sequential int8 id did — gen_random_uuid() has no relationship to
-- insertion order, so "WHERE id > last_checkpoint" would silently skip
-- rows forever whenever a new row's generated uuid happens to sort
-- before an already-processed one. id is only ever used as a tiebreaker
-- alongside loaded_at below, where meaningful ordering doesn't matter —
-- it only needs to be unique.
--
-- Step 3's transform originally used (updated_at, "_id") instead —
-- updated_at seemed like the more meaningful choice, a real "when was
-- this last touched" timestamp. That broke the first time a backfill for
-- an EARLIER billing period was loaded after processing had already
-- moved past a LATER one: the backfilled rows' updated_at was older than
-- the checkpoint, so they were silently skipped forever. loaded_at
-- doesn't have that failure mode — any load, live or a backfill for any
-- past period, happens "now," always after whatever the checkpoint
-- currently is. See sql/zeus_sales_feed_to_working_transform.sql's own
-- doc comment for the full story.
--
-- No unique constraint on "_id": restaging still applies (confirmed) —
-- the same bill can land again later under the same "_id" with a new
-- updated_at when its status/amount changes, same as
-- zeus_sales_working today. Step 3's transform will need the same
-- DISTINCT ON (..., updated_at DESC) "latest wins" dedup the existing
-- migrate_zeus_sales_from_working and the MMS migration both already
-- use for the same reason.
CREATE TABLE IF NOT EXISTS app.zeus_sales_feed (
    id                               uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    "_id"                            text NOT NULL,
    bill                             text,
    bill_type                        text,
    service_point                    text,
    service_point_code               text,
    service_point_status             text,
    tariff_class                     text,
    tariff_class_code                text,
    tariff_class_name                text,
    service_class                    text,
    geo_code                         text,
    account_code                     text,
    meter_code                       text,
    meter_model_type                 text,
    region                           text,
    region_code                      text,
    region_name                      text,
    district                         text,
    district_code                    text,
    district_name                   text,
    account_type                    text,
    account_groups                   jsonb,
    soe_name                         text,
    mda_name                         text,
    is_sensitive                     text,
    customer_name                    text,
    bill_consumption_value           numeric(18, 4),
    bill_consumption_apparent_value  numeric(18, 4),
    bill_consumption_max_demand_value numeric(18, 4),
    bill_consumption_export_value    numeric(18, 4),
    bill_avg_consumption_value       numeric(18, 4),
    bill_period                      int4,
    bill_consumption_type            text,
    outstanding_amount               numeric(18, 4),
    life_line_amount                 numeric(18, 4),
    first_threshold_amount           numeric(18, 4),
    second_threshold_amount          numeric(18, 4),
    third_threshold_amount           numeric(18, 4),
    energy_charge                    numeric(18, 4),
    service_charge                   numeric(18, 4),
    energy_plus_service_charge       numeric(18, 4),
    power_factor_surcharge           numeric(18, 4),
    vat_charge                       numeric(18, 4),
    nhil_charge                      numeric(18, 4),
    getfund_charge                   numeric(18, 4),
    street_light_charge              numeric(18, 4),
    national_electrification_charge  numeric(18, 4),
    bill_amount                      numeric(18, 4),
    payments_amount                  numeric(18, 4),
    adjustment_consumption_value     numeric(18, 4),
    adjustment_amount                numeric(18, 4),
    amount_due                       numeric(18, 4),
    debt_amount                      numeric(18, 4),
    last_payment_amount              numeric(18, 4),
    last_payment_date                timestamptz,
    month                            int4,   -- feed's own name — maps to zeus_sales_working.billingmonth in step 3's transform, not a name match
    year                             int4,   -- maps to zeus_sales_working.billingyear
    bill_status                      text,
    bills                            jsonb,
    created_at                       timestamptz,
    updated_at                       timestamptz,
    "__v"                            int4,
    debtage                          jsonb,  -- matches zeus_sales_working's debtage column name (step 1)
    slt_status                       text,
    loaded_at                        timestamptz NOT NULL DEFAULT now()  -- when THIS row landed here — step 3's transform cursor (see sql/zeus_sales_feed_to_working_transform.sql)
);

-- Supports the (loaded_at, id) keyset cursor step 3's transform uses,
-- same role idx_mms_sales_accounting_monthyear/meter_id played for the
-- MMS migration.
CREATE INDEX IF NOT EXISTS idx_zeus_sales_feed_loaded_at_id
    ON app.zeus_sales_feed (loaded_at, id);

-- Kept for lookups by updated_at alone (diagnostics, the kind of
-- "is new data actually here yet" checks this session's backfill
-- incident needed) — no longer the transform's cursor.
CREATE INDEX IF NOT EXISTS idx_zeus_sales_feed_updated_at_id
    ON app.zeus_sales_feed (updated_at, "_id");

CREATE INDEX IF NOT EXISTS idx_zeus_sales_feed_source_id
    ON app.zeus_sales_feed ("_id");
