-- Adds soft-delete support to app.meters for the meter-management admin UI.
-- Retiring a meter sets this instead of removing the row, so historical
-- consumption/billing data that references it by id/meter_number stays
-- intact. Nullable/unconstrained on purpose — bun's soft_delete convention
-- on internal/meters.Meter.DeletedAt expects a plain nullable timestamptz.
ALTER TABLE app.meters ADD COLUMN IF NOT EXISTS deleted_at timestamptz NULL;
