-- Step 1 of adopting the new Zeus billing feed shape (see discussion —
-- the new source sends debtAge aging buckets and a bills array that
-- app.zeus_sales_working has no columns for yet, and reshapes
-- accountGroups from a single value into an array).
--
-- Purely additive — nothing dropped, nothing renamed, every existing
-- column/row untouched. Confirmed safe: zero Go code references
-- app.zeus_sales_working at all (grep across the whole repo) — it's pure
-- SQL-only staging, read only by app.migrate_zeus_sales_from_working,
-- which this change doesn't touch.
--
-- month/year (the new feed's field names, replacing billingmonth/
-- billingyear) and everything else from the new JSON shape map onto
-- EXISTING columns during the transform step (step 2, not this file) —
-- only debt_age, bills, and accountgroups' type need anything here.

-- 1. debtage: new field entirely. The new feed sends it as a nested
--    object (month0..month24, moreThan24Months) — stored as jsonb as-is
--    rather than normalized into 26 separate columns, since nothing
--    reads it yet and jsonb keeps every bucket queryable later
--    (debtage->>'month3', etc.) without a schema change if/when
--    reporting on it is actually wanted. Named without an underscore
--    (not debt_age) to match every other column on this table — all
--    lowercase, no underscores (billconsumptionvalue, lastpaymentamount,
--    etc.). If an earlier version of this script already ran and
--    created debt_age, rename it instead:
--      ALTER TABLE app.zeus_sales_working RENAME COLUMN debt_age TO debtage;
ALTER TABLE app.zeus_sales_working
    ADD COLUMN IF NOT EXISTS debtage jsonb;

-- 2. bills: new field entirely, empty array in both samples seen so far,
--    purpose not yet confirmed. Stored as jsonb for the same reason —
--    captured without guessing at a shape to normalize into.
ALTER TABLE app.zeus_sales_working
    ADD COLUMN IF NOT EXISTS bills jsonb;

-- 3. accountgroups: existed already as varchar (a single value), but the
--    new feed sends it as a JSON array (empty in both samples). A plain
--    varchar->jsonb cast would error on any existing value that isn't
--    already valid JSON text, so this uses to_jsonb() instead, which
--    safely WRAPS whatever is there now as a JSON string scalar (NULL
--    stays NULL) rather than trying to parse it. Old rows end up holding
--    a JSON string; new rows from the new feed will hold a real JSON
--    array — a mixed shape, but accountgroups isn't read by any Go code
--    today (confirmed), so nothing downstream is affected by the mix.
ALTER TABLE app.zeus_sales_working
    ALTER COLUMN accountgroups TYPE jsonb USING to_jsonb(accountgroups);
