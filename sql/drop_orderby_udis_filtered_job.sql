-- load-udis-aug-filtered's source_query carried an ORDER BY rmr.id, rmr.TV
-- copied from the incremental job's pattern (where ORDER BY on the
-- watermark column is required -- see run.go's runExtractQuery comment:
-- the watermark advances off "the last row of the batch", which only
-- means anything if rows arrive in that order). This job is
-- mode=full_refresh with filter_query set, which never tracks a
-- watermark at all (extractAndLoadFiltered always passes wmIdx=-1) --
-- the ORDER BY buys nothing here.
--
-- Cost: with no index producing (ID, TV) order directly, Oracle can't
-- stream results under that ORDER BY -- it has to buffer and sort the
-- entire filtered result for a chunk before returning even one row to
-- the client. That's why the run produced zero rows in
-- app.raw_meter_readings_temp despite running -- not stuck, just
-- withholding everything until a sort over however many rows 800 meters'
-- worth of readings turns out to be finishes (or the run times out
-- first). Dropping it lets rows stream/commit per batch as found, same
-- as the incremental job did.
UPDATE app.etl_jobs
SET source_query = $q$SELECT rmr.ID, rmr.TV, rmr.DATA_ITEM_ID, rmr.VAL, rmr.TV_UPDATE, '202608' AS SHEET_NAME
FROM UDIS_CH.RD_METER_READS202608 rmr
WHERE rmr.ID IN ({{FILTER}})
  AND rmr.TV >= 1786838400
  AND rmr.TV < (SELECT (TRUNC(SYSDATE) - TO_DATE('1970-01-01', 'YYYY-MM-DD')) * 86400 FROM DUAL)
  AND rmr.DATA_ITEM_ID IN ('00100000','00300000','01500001','01500002','05001211','05001212')$q$
WHERE name = 'load-udis-aug-filtered';
