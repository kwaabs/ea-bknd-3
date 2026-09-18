-- Efficiency: mirrors indexes_pns_consumption.sql for
-- app.holley_consumption. Every filter in
-- internal/holleyconsumption/service.go's base() uses lower(col) IN (...),
-- which needs a functional index on lower(col) to avoid a sequential scan
-- of the whole table — a plain index on col cannot be used by that query
-- shape. These make the existing queries index-driven with zero code
-- changes; run this manually against the database (same workflow as the
-- other sql/indexes_*.sql files in this repo — nothing here runs
-- automatically).

CREATE INDEX IF NOT EXISTS idx_holley_consumption_lower_region
    ON app.holley_consumption (lower(region));
CREATE INDEX IF NOT EXISTS idx_holley_consumption_lower_district
    ON app.holley_consumption (lower(district));
CREATE INDEX IF NOT EXISTS idx_holley_consumption_lower_tariff_class
    ON app.holley_consumption (lower(tariff_class));

-- Exact-match filter (dbx.In, not InLower)
CREATE INDEX IF NOT EXISTS idx_holley_consumption_meter_no
    ON app.holley_consumption (meter_no);

-- Date range filter — the main one, since date_time is this table's real
-- time dimension and every date-range-filtered page hits it. Already
-- covered by the table's own primary key (meter_id, meter_no, customer_id,
-- date_time), but that composite can't be used to seek on date_time alone
-- (it's not the leading column) — a dedicated index is needed.
CREATE INDEX IF NOT EXISTS idx_holley_consumption_date_time
    ON app.holley_consumption (date_time);

-- The %search% LIKE across customer_name/meter_no/customer_no can never use
-- a btree index. pg_trgm GIN indexes make substring search index-assisted:
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_holley_consumption_trgm_customer_name
    ON app.holley_consumption USING gin (lower(customer_name) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_holley_consumption_trgm_meter_no
    ON app.holley_consumption USING gin (lower(meter_no) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_holley_consumption_trgm_customer_no
    ON app.holley_consumption USING gin (lower(customer_no) gin_trgm_ops);

-- The default sort for /detail (region, district, customer_name, meter_no).
-- A matching composite index lets Postgres serve deep pagination without a
-- full sort node:
CREATE INDEX IF NOT EXISTS idx_holley_consumption_default_sort
    ON app.holley_consumption (region, district, customer_name, meter_no);
