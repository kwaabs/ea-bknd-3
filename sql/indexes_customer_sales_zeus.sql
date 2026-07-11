-- customer_sales_zeus is large (15M+ rows). The Aggregate() query filters on
-- region + a lastbilldate range together, but there was no index covering
-- both — only single-column indexes on each. The planner combined them via
-- a bitmap AND that went lossy (page-granularity only) once the intersection
-- exceeded work_mem, forcing a recheck of the actual predicate against every
-- row on each flagged page. On a region + multi-month range this took 30-40s.
--
-- On a large live table, prefer running this with CREATE INDEX CONCURRENTLY
-- (cannot run inside a transaction, so run this line by itself if the table
-- has live traffic):
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_csz_region_lastbilldate
--       ON app.customer_sales_zeus (lower(regionname), lastbilldate);

CREATE INDEX IF NOT EXISTS idx_csz_region_lastbilldate
    ON app.customer_sales_zeus (lower(regionname), lastbilldate);
