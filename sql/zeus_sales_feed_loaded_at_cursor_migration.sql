-- One-time cutover: switches migrate_zeus_sales_feed_to_working's cursor
-- from (updated_at, "_id") to (loaded_at, id) — see the updated doc
-- comment on that procedure (sql/zeus_sales_feed_to_working_transform.sql)
-- for the full why. Run this BEFORE re-running that procedure with its
-- new definition.
--
-- 1. zeus_sales_feed_table.sql already defines loaded_at for a brand-new
--    table, but CREATE TABLE IF NOT EXISTS is a no-op against a table
--    that already exists — this ADD COLUMN IF NOT EXISTS is what actually
--    gets it onto the live table. DEFAULT now() backfills every existing
--    row in this one statement (a NOT NULL column with a non-volatile
--    default like now() applies it to the whole table at ALTER time,
--    no separate UPDATE needed) — existing rows all land on
--    approximately the same "now, at migration time" value, which is
--    fine: they've already been staged once under the old cursor, we
--    just need SOME value here that's consistently before whatever comes
--    next.
ALTER TABLE app.zeus_sales_feed
    ADD COLUMN IF NOT EXISTS loaded_at timestamptz NOT NULL DEFAULT now();

-- 2. Supports the new (loaded_at, id) keyset cursor, same role
--    idx_zeus_sales_feed_updated_at_id played for the old one.
CREATE INDEX IF NOT EXISTS idx_zeus_sales_feed_loaded_at_id
    ON app.zeus_sales_feed (loaded_at, id);

-- 3. Re-point the existing checkpoint at "everything currently in the
--    table" under the NEW cursor, so the next run doesn't reprocess
--    every row already staged under the old one. Safe even if it's off
--    by a little: re-staging a handful of already-staged rows a second
--    time is harmless (zeus_sales_working has no unique constraint by
--    design, and the downstream dedup in migrate_zeus_sales_from_working
--    already resolves duplicates by picking the highest id).
UPDATE app.migration_checkpoints
SET last_timestamp = (SELECT MAX(loaded_at) FROM app.zeus_sales_feed),
    last_text = (SELECT id::text FROM app.zeus_sales_feed ORDER BY loaded_at DESC, id DESC LIMIT 1),
    updated_at = clock_timestamp()
WHERE procedure_name = 'migrate_zeus_sales_feed_to_working';

-- After this runs, re-create the procedure from
-- sql/zeus_sales_feed_to_working_transform.sql (its new (loaded_at, id)
-- version), then any future load — live or a backfill for any past
-- period — just needs the usual:
--   CALL app.migrate_zeus_sales_feed_to_working();
--   CALL app.migrate_zeus_sales_from_working();
-- with no manual checkpoint rewind, regardless of which billing period
-- the load is for.
