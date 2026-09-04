package etl

import (
	"fmt"
	"strconv"
	"strings"
)

const watermarkToken = "{{WATERMARK}}"

// defaultWatermark is substituted for a job's very first run, before
// app.etl_job_state has a row for it — a deliberately-old-but-real lower
// bound rather than a sentinel the source database might not understand
// (Postgres accepts '-infinity' for a timestamp; Oracle/MSSQL do not),
// mirroring how the resumable migration procedures elsewhere in this repo
// seed a fresh cursor (see populate_mms_customer_sales_from_raw.sql's
// v_last_dt := '-infinity', which is Postgres-side only for the same
// reason — this engine's source side can be any of the three kinds).
func defaultWatermark(t WatermarkType) string {
	switch t {
	case WatermarkTimestamp:
		return "1900-01-01T00:00:00"
	case WatermarkInteger:
		return "0"
	default: // WatermarkString
		return ""
	}
}

// formatWatermarkLiteral turns a raw watermark value into a SQL literal
// safe to substitute directly into a query string. This is a literal text
// substitution, not a bind parameter (see source_query's comment in
// sql/etl_engine.sql for why that's acceptable here): the value always
// originates from THIS engine's own app.etl_job_state (or defaultWatermark
// above), never from end-user input, and every code path that writes
// last_watermark does so as a value scanned straight out of the source
// database's own watermark column — not user-composed text — so there's no
// injection surface even before this formatting/escaping is applied.
func formatWatermarkLiteral(raw string, t WatermarkType) (string, error) {
	switch t {
	case WatermarkTimestamp:
		// Kept as an ISO-ish string wrapped in quotes rather than parsed
		// into time.Time and re-rendered — different source dialects (Oracle
		// TIMESTAMP literals, MSSQL datetime2, Postgres timestamptz) accept
		// slightly different textual forms, and re-formatting here risks
		// silently producing one a given source's ANSI-date parsing rejects.
		// A job's source_query is expected to CAST/TO_TIMESTAMP this string
		// itself if its dialect needs an explicit conversion function rather
		// than an implicit string-to-timestamp cast.
		escaped := strings.ReplaceAll(raw, "'", "''")
		return "'" + escaped + "'", nil

	case WatermarkInteger:
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			return "", fmt.Errorf("etl: watermark value %q is not a valid integer: %w", raw, err)
		}
		return raw, nil

	case WatermarkString:
		escaped := strings.ReplaceAll(raw, "'", "''")
		return "'" + escaped + "'", nil

	default:
		return "", fmt.Errorf("etl: unknown watermark type %q", t)
	}
}

// buildQuery substitutes every occurrence of the {{WATERMARK}} token in
// job.SourceQuery with lastWatermark, formatted per job.WatermarkType. For
// a full_refresh job (no watermark), the query is returned unchanged — it
// must not reference {{WATERMARK}} at all, which this checks.
func buildQuery(job Job, lastWatermark string) (string, error) {
	hasToken := strings.Contains(job.SourceQuery, watermarkToken)

	if job.Mode == ModeFullRefresh {
		if hasToken {
			return "", fmt.Errorf("etl: job %q is full_refresh but its source_query references %s", job.Name, watermarkToken)
		}
		return job.SourceQuery, nil
	}

	// Incremental.
	if job.WatermarkType == nil {
		return "", fmt.Errorf("etl: job %q is incremental but has no watermark_type", job.Name)
	}
	if !hasToken {
		return "", fmt.Errorf("etl: job %q is incremental but its source_query never references %s", job.Name, watermarkToken)
	}
	literal, err := formatWatermarkLiteral(lastWatermark, *job.WatermarkType)
	if err != nil {
		return "", fmt.Errorf("etl: job %q: %w", job.Name, err)
	}
	return strings.ReplaceAll(job.SourceQuery, watermarkToken, literal), nil
}
