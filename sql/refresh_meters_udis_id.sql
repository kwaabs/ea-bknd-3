-- Keeps app.meters.udis_id in sync with UDIS_CH.M_METER.METER_ID.
--
-- Confirmed stale/wrong on a live sample: app.meters.udis_id for
-- meter_number 234402049 was 9297833, but UDIS_CH.M_METER's actual
-- current METER_ID for that ASSET_NO is 9297815 -- a real mismatch, not
-- a rounding/formatting artifact. udis_id can't be trusted as a fixed,
-- one-time-populated value; the old FME process re-resolved this live
-- against M_METER on every run (matching ASSET_NO = meter_number) rather
-- than trusting a cached column, for exactly this reason. Same landing-
-- table -> merge-procedure shape as the rest of this pipeline (see
-- ETL.md) so this can be its own scheduled job, independent of the main
-- readings job that depends on it being correct.

CREATE TABLE IF NOT EXISTS app.udis_meter_id_raw (
    meter_number text NOT NULL,
    udis_id      text NOT NULL,
    loaded_at    timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION app.refresh_meters_udis_id()
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_updated int;
BEGIN
    -- Only touches rows whose udis_id actually changed -- IS DISTINCT
    -- FROM (not <>) so a meter going from NULL to a real value, or vice
    -- versa, counts as a change too, not just value-to-value edits.
    UPDATE app.meters m
    SET udis_id = raw.udis_id
    FROM app.udis_meter_id_raw raw
    WHERE m.meter_number = raw.meter_number
      AND m.udis_id IS DISTINCT FROM raw.udis_id;
    GET DIAGNOSTICS v_updated = ROW_COUNT;

    RAISE NOTICE 'refresh_meters_udis_id: % meter(s) updated', v_updated;

    TRUNCATE app.udis_meter_id_raw;
END;
$$;

-- ---------------------------------------------------------------------------
-- ETL job configuration (create via the admin UI's Add Job wizard, or the
-- SQL below once you have the "udis" source's id -- see ETL.md's own
-- example for the INSERT shape).
--
--   Source: the existing Oracle "udis" source
--   Mode: Full refresh (this is what filter_query/{{FILTER}} requires --
--   see ETL.md's "{{FILTER}} contract" section)
--   filter_query (runs against THIS app database, not Oracle):
--     SELECT DISTINCT meter_number FROM app.meters
--     WHERE TRIM(meter_number) <> ''
--       AND lower(TRIM(meter_number)) NOT IN ('no_meter','no meter','non access')
--   source_query (runs against Oracle, {{FILTER}} required since
--   filter_query is set):
--     SELECT ASSET_NO, TO_CHAR(METER_ID)
--     FROM UDIS_CH.M_METER
--     WHERE ASSET_NO IN ({{FILTER}})
--   Destination: app.udis_meter_id_raw
--   Column mapping (source -> dest, in order): ASSET_NO -> meter_number,
--     METER_ID -> udis_id
--   Conflict columns: none -- plain append, TRUNCATEd by the refresh above
--   Trigger times: run periodically (weekly is plenty -- meter-to-device
--   assignments don't change often), and always before the first run of
--   the udis_meter_reads job so its meter_number resolution starts correct
--
-- After each run of this job:
--   SELECT app.refresh_meters_udis_id();
-- ---------------------------------------------------------------------------
