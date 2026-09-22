-- Efficiency: mirrors indexes_holley_consumption.sql for
-- app.ecash4_consumption. Every filter in
-- internal/ecash4consumption/service.go's base() uses lower(col) IN (...),
-- which needs a functional index on lower(col) to avoid a sequential scan
-- of the whole table — a plain index on col cannot be used by that query
-- shape. These make the existing queries index-driven with zero code
-- changes; run this manually against the database (same workflow as the
-- other sql/indexes_*.sql files in this repo — nothing here runs
-- automatically).

CREATE INDEX IF NOT EXISTS idx_ecash4_consumption_lower_region
    ON app.ecash4_consumption (lower(region));
CREATE INDEX IF NOT EXISTS idx_ecash4_consumption_lower_district
    ON app.ecash4_consumption (lower(district));
CREATE INDEX IF NOT EXISTS idx_ecash4_consumption_lower_tariff_class
    ON app.ecash4_consumption (lower(tariff_class));

-- Exact-match filter (dbx.In, not InLower)
CREATE INDEX IF NOT EXISTS idx_ecash4_consumption_meter_serial
    ON app.ecash4_consumption (meter_serial);

-- Date range filter — period_date is this table's real time dimension
-- (generated from year_month) and every date-range-filtered page hits it.
-- Already covered by the table's own primary key (meter_serial, spn,
-- year_month), but that composite can't be used to seek on period_date
-- alone (it's not the leading column, and it's a different column from
-- year_month besides) — a dedicated index is needed.
CREATE INDEX IF NOT EXISTS idx_ecash4_consumption_period_date
    ON app.ecash4_consumption (period_date);

-- The %search% LIKE across customer_name/meter_serial/spn can never use a
-- btree index. pg_trgm GIN indexes make substring search index-assisted.
-- pg_trgm is installed in the `app` schema on this database (confirmed via
-- pg_extension/pg_namespace), not `public` — the operator class is
-- schema-qualified below so this doesn't depend on `app` being on
-- whatever session's search_path happens to run this file.
CREATE EXTENSION IF NOT EXISTS pg_trgm SCHEMA app;
CREATE INDEX IF NOT EXISTS idx_ecash4_consumption_trgm_customer_name
    ON app.ecash4_consumption USING gin (lower(customer_name) app.gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_ecash4_consumption_trgm_meter_serial
    ON app.ecash4_consumption USING gin (lower(meter_serial) app.gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_ecash4_consumption_trgm_spn
    ON app.ecash4_consumption USING gin (lower(spn) app.gin_trgm_ops);

-- The default sort for /detail (region, district, customer_name,
-- meter_serial). A matching composite index lets Postgres serve deep
-- pagination without a full sort node:
CREATE INDEX IF NOT EXISTS idx_ecash4_consumption_default_sort
    ON app.ecash4_consumption (region, district, customer_name, meter_serial);
