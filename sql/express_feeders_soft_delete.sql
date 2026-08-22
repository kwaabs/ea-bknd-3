-- Adds soft-delete support to app.express_feeders, mirroring
-- sql/meters_soft_delete.sql. Run manually against Supabase (this repo has
-- no migration tool) before the express-feeders admin endpoints go live.
--
-- Before running: confirm app.express_feeders already has a `id uuid`
-- primary key column (every other app.* table in this schema does, but no
-- existing query ever selects it, so it was unverified against the real
-- schema when this file was written). If it's missing, add it too:
--   ALTER TABLE app.express_feeders ADD COLUMN IF NOT EXISTS id uuid PRIMARY KEY DEFAULT uuid_generate_v4();

ALTER TABLE app.express_feeders ADD COLUMN IF NOT EXISTS deleted_at timestamptz NULL;
