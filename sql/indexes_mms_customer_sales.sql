-- Efficiency: every filter in the service uses lower(col) IN (...).
-- Without a functional index on lower(col), Postgres cannot use a plain
-- index on col and falls back to a sequential scan of the whole table.
-- These make the existing queries index-driven with zero code changes.

CREATE INDEX IF NOT EXISTS idx_mms_sales_lower_region
    ON app.mms_customer_sales (lower(region));
CREATE INDEX IF NOT EXISTS idx_mms_sales_lower_district
    ON app.mms_customer_sales (lower(district));
CREATE INDEX IF NOT EXISTS idx_mms_sales_lower_contract_type
    ON app.mms_customer_sales (lower(contract_type));
CREATE INDEX IF NOT EXISTS idx_mms_sales_lower_tariff
    ON app.mms_customer_sales (lower(tariff));
CREATE INDEX IF NOT EXISTS idx_mms_sales_lower_manufacturer
    ON app.mms_customer_sales (lower(manufacturer));
CREATE INDEX IF NOT EXISTS idx_mms_sales_lower_model
    ON app.mms_customer_sales (lower(model));

-- Exact-match filters
CREATE INDEX IF NOT EXISTS idx_mms_sales_account_number
    ON app.mms_customer_sales (account_number);
CREATE INDEX IF NOT EXISTS idx_mms_sales_meter_number
    ON app.mms_customer_sales (meter_number);

-- Date range filter
CREATE INDEX IF NOT EXISTS idx_mms_sales_date_time
    ON app.mms_customer_sales (date_time);

-- The %search% LIKE across four columns can never use a btree index.
-- pg_trgm GIN indexes make substring search index-assisted:
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_mms_sales_trgm_customer_name
    ON app.mms_customer_sales USING gin (lower(customer_name) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_mms_sales_trgm_account_number
    ON app.mms_customer_sales USING gin (lower(account_number::text) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_mms_sales_trgm_meter_number
    ON app.mms_customer_sales USING gin (lower(meter_number::text) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_mms_sales_trgm_meter_serial
    ON app.mms_customer_sales USING gin (lower(meter_serial_number::text) gin_trgm_ops);

-- The default sort for /detail. A matching composite index lets Postgres
-- serve deep pagination without a full sort node:
CREATE INDEX IF NOT EXISTS idx_mms_sales_default_sort
    ON app.mms_customer_sales (region, district, customer_name, account_number);
