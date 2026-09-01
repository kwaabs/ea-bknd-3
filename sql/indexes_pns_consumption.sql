-- Efficiency: mirrors indexes_mms_customer_sales.sql for
-- app.pns_consumption (7M+ rows and growing). Every filter in
-- internal/pnsconsumption/service.go's base() uses lower(col) IN (...),
-- which needs a functional index on lower(col) to avoid a sequential scan
-- of the whole table — a plain index on col cannot be used by that query
-- shape. These make the existing queries index-driven with zero code
-- changes; run this manually against the database (same workflow as the
-- other sql/indexes_*.sql files in this repo — nothing here runs
-- automatically).

CREATE INDEX IF NOT EXISTS idx_pns_consumption_lower_regionid
    ON app.pns_consumption (lower(regionid));
CREATE INDEX IF NOT EXISTS idx_pns_consumption_lower_districtid
    ON app.pns_consumption (lower(districtid));
CREATE INDEX IF NOT EXISTS idx_pns_consumption_lower_tariffcategory
    ON app.pns_consumption (lower(tariffcategory));
CREATE INDEX IF NOT EXISTS idx_pns_consumption_lower_billmonth
    ON app.pns_consumption (lower(billmonth));

-- Exact-match filter (dbx.In, not InLower)
CREATE INDEX IF NOT EXISTS idx_pns_consumption_serviceid
    ON app.pns_consumption (serviceid);

-- Date range filter — the main one, since billdate is this table's real
-- time dimension and every date-range-filtered page hits it.
CREATE INDEX IF NOT EXISTS idx_pns_consumption_billdate
    ON app.pns_consumption (billdate);

-- The %search% LIKE across customerid/serviceid/servicepoint can never use
-- a btree index. pg_trgm GIN indexes make substring search index-assisted:
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_pns_consumption_trgm_customerid
    ON app.pns_consumption USING gin (lower(customerid) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_pns_consumption_trgm_serviceid
    ON app.pns_consumption USING gin (lower(serviceid) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_pns_consumption_trgm_servicepoint
    ON app.pns_consumption USING gin (lower(servicepoint) gin_trgm_ops);

-- The default sort for /detail (regionid, districtid, customerid). A
-- matching composite index lets Postgres serve deep pagination without a
-- full sort node:
CREATE INDEX IF NOT EXISTS idx_pns_consumption_default_sort
    ON app.pns_consumption (regionid, districtid, customerid);
