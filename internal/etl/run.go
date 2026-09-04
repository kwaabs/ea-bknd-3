package etl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// runResult summarizes one job execution for the run-history row and logs.
type runResult struct {
	RowsExtracted int64
	RowsLoaded    int64
}

// runJob executes one job end-to-end: opens a run-history row, extracts +
// batch-loads (see extractAndLoad), and closes out that row as
// success/failed. sourceDB is a pooled connection the caller (Engine) owns
// and reuses across runs of the same source — runJob never closes it.
func runJob(ctx context.Context, destDB *bun.DB, sourceDB *sql.DB, job Job) (runResult, error) {
	var result runResult

	runID, err := insertRunStarted(ctx, destDB, job.ID)
	if err != nil {
		return result, fmt.Errorf("etl: record run start for job %q: %w", job.Name, err)
	}

	result, runErr := extractAndLoad(ctx, destDB, sourceDB, job)

	if runErr != nil {
		if finishErr := finishRun(ctx, destDB, runID, RunStatusFailed, result, runErr.Error()); finishErr != nil {
			return result, fmt.Errorf("%w (also failed to record failure: %v)", runErr, finishErr)
		}
		return result, runErr
	}
	if err := finishRun(ctx, destDB, runID, RunStatusSuccess, result, ""); err != nil {
		return result, fmt.Errorf("etl: record run success for job %q: %w", job.Name, err)
	}
	return result, nil
}

// extractAndLoad decides how job.SourceQuery gets run — once (the plain
// case) or once per chunk of job.FilterQuery's results (see
// extractAndLoadFiltered) — and delegates the actual streaming
// extract+batch+load work to runExtractQuery either way.
func extractAndLoad(ctx context.Context, destDB *bun.DB, sourceDB *sql.DB, job Job) (runResult, error) {
	var result runResult

	wmIdx := -1
	if job.Mode == ModeIncremental {
		wmIdx = indexOf(job.DestColumns, *job.WatermarkColumn)
		if wmIdx == -1 {
			return result, fmt.Errorf("etl: job %q: watermark_column %q not found in dest_columns", job.Name, *job.WatermarkColumn)
		}
	}

	if job.FilterQuery != nil && strings.TrimSpace(*job.FilterQuery) != "" {
		// JobInput.validate requires mode=full_refresh whenever
		// filter_query is set, so wmIdx is always -1 here — see
		// extractAndLoadFiltered's comment for why the two don't mix.
		return extractAndLoadFiltered(ctx, destDB, sourceDB, job)
	}

	lastWatermark := defaultWatermarkFor(job)
	if job.Mode == ModeIncremental {
		state, err := loadJobState(ctx, destDB, job.ID)
		if err != nil {
			return result, err
		}
		if state != nil && state.LastWatermark != nil {
			lastWatermark = *state.LastWatermark
		}
	}

	query, err := buildQuery(job, lastWatermark)
	if err != nil {
		return result, err
	}

	return runExtractQuery(ctx, destDB, sourceDB, job, query, wmIdx)
}

const defaultFilterBatchSize = 1000

// extractAndLoadFiltered runs job.FilterQuery once against destDB — this
// app database, not the external source — to get the values that seed
// {{FILTER}}, chunks them into job.FilterBatchSize-sized groups, and runs
// job.SourceQuery (with {{FILTER}} substituted per chunk) through the same
// runExtractQuery loop as a plain job, once per chunk, summing the totals.
// This is how a job pulls only the rows matching a list of keys that live
// in this app database (e.g. app.meters) without a live cross-database
// join, which this engine's single-source-per-job connectors can't do —
// see ETL.md.
//
// Restricted to mode=full_refresh (enforced in JobInput.validate): the
// "advance the watermark using the last row of each flushed batch"
// contract loadBatch relies on assumes one continuous, ORDER-BY-watermark
// result set. Filter chunks are independent, arbitrarily-ordered result
// sets (different key subsets, no relative ordering between them), so
// there's no sound way to compute one true watermark across all of
// them — advancing per-chunk risks skipping rows a later chunk should
// still have picked up. Rather than get that subtly wrong, incremental +
// filtered just isn't supported.
func extractAndLoadFiltered(ctx context.Context, destDB *bun.DB, sourceDB *sql.DB, job Job) (runResult, error) {
	var total runResult

	values, err := loadFilterValues(ctx, destDB, *job.FilterQuery)
	if err != nil {
		return total, fmt.Errorf("etl: job %q: run filter_query: %w", job.Name, err)
	}
	if len(values) == 0 {
		return total, nil // nothing matches the filter this run — not an error
	}

	batchSize := defaultFilterBatchSize
	if job.FilterBatchSize != nil && *job.FilterBatchSize > 0 {
		batchSize = *job.FilterBatchSize
	}

	for start := 0; start < len(values); start += batchSize {
		end := start + batchSize
		if end > len(values) {
			end = len(values)
		}
		literal, err := formatFilterLiteral(values[start:end])
		if err != nil {
			return total, fmt.Errorf("etl: job %q: %w", job.Name, err)
		}
		query, err := buildFilteredQuery(job, literal)
		if err != nil {
			return total, err
		}
		chunkResult, err := runExtractQuery(ctx, destDB, sourceDB, job, query, -1)
		total.RowsExtracted += chunkResult.RowsExtracted
		total.RowsLoaded += chunkResult.RowsLoaded
		if err != nil {
			return total, fmt.Errorf("etl: job %q: filter batch [%d,%d): %w", job.Name, start, end, err)
		}
	}
	return total, nil
}

// loadFilterValues runs a job's filter_query against destDB (this app
// database) and returns its single result column's values, in row order —
// dedup/ordering, if wanted, is filter_query's own job (e.g. SELECT
// DISTINCT meter_number FROM app.meters ORDER BY meter_number). Rejects a
// query returning more than one column up front, rather than letting
// rows.Scan fail on the first row with a less useful error.
func loadFilterValues(ctx context.Context, destDB *bun.DB, filterQuery string) ([]interface{}, error) {
	rows, err := destDB.QueryContext(ctx, filterQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if len(cols) != 1 {
		return nil, fmt.Errorf("filter_query must return exactly one column, got %d", len(cols))
	}

	var values []interface{}
	for rows.Next() {
		var v interface{}
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if v == nil {
			continue // a NULL key can never match anything — skip rather than error
		}
		values = append(values, normalizeValue(v))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

// runExtractQuery streams the given (already-built) query's result set
// from sourceDB, batches rows into groups of job.BatchSize, and loads each
// batch into the destination table in its own transaction (batch insert +
// watermark update together — same "commit the checkpoint with the data
// it describes" contract as app.migration_checkpoints elsewhere in this
// repo). A failure partway through leaves every already-committed batch in
// place and the watermark (if any) advanced up to it. Shared by the plain
// single-query path and the filtered path (extractAndLoadFiltered calls
// this once per filter chunk) — everything past "here is the query text
// to run" is identical either way.
func runExtractQuery(ctx context.Context, destDB *bun.DB, sourceDB *sql.DB, job Job, query string, wmIdx int) (runResult, error) {
	var result runResult

	rows, err := sourceDB.QueryContext(ctx, query)
	if err != nil {
		return result, fmt.Errorf("etl: query source for job %q: %w", job.Name, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return result, fmt.Errorf("etl: read result columns for job %q: %w", job.Name, err)
	}
	if len(cols) != len(job.DestColumns) {
		return result, fmt.Errorf(
			"etl: job %q: source_query returns %d columns but dest_columns has %d",
			job.Name, len(cols), len(job.DestColumns),
		)
	}

	batch := make([][]interface{}, 0, job.BatchSize)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		var newWatermark *string
		if job.Mode == ModeIncremental {
			// The last row in the batch, not a computed max — incremental
			// source_query is required to ORDER BY the watermark column
			// ascending (same expectation the keyset-pagination procedures
			// elsewhere in this repo place on their own ORDER BY), so the
			// last row processed is the furthest one reached so far.
			wm, err := watermarkToString(batch[len(batch)-1][wmIdx])
			if err != nil {
				return fmt.Errorf("etl: job %q: %w", job.Name, err)
			}
			newWatermark = &wm
		}
		if err := loadBatch(ctx, destDB, job, batch, newWatermark); err != nil {
			return err
		}
		result.RowsLoaded += int64(len(batch))
		batch = batch[:0]
		return nil
	}

	for rows.Next() {
		values := make([]interface{}, len(cols))
		scanArgs := make([]interface{}, len(cols))
		for i := range values {
			scanArgs[i] = &values[i]
		}
		if err := rows.Scan(scanArgs...); err != nil {
			return result, fmt.Errorf("etl: scan row for job %q: %w", job.Name, err)
		}
		for i := range values {
			values[i] = normalizeValue(values[i])
		}
		batch = append(batch, values)
		result.RowsExtracted++

		if len(batch) >= job.BatchSize {
			if err := flush(); err != nil {
				return result, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("etl: reading rows for job %q: %w", job.Name, err)
	}
	if err := flush(); err != nil {
		return result, err
	}

	return result, nil
}

// loadBatch inserts one batch into the destination table and (for an
// incremental job) advances its watermark, in the same transaction —
// committed together or not at all.
func loadBatch(ctx context.Context, destDB *bun.DB, job Job, batch [][]interface{}, newWatermark *string) error {
	query, args, err := buildInsertSQL(job, batch)
	if err != nil {
		return fmt.Errorf("etl: build insert for job %q: %w", job.Name, err)
	}

	tx, err := destDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("etl: begin dest transaction for job %q: %w", job.Name, err)
	}
	defer func() { _ = tx.Rollback() }()

	// bun.Tx (and bun.DB)'s ExecContext implements the stdlib sql.Tx
	// signature for drop-in compatibility, but doesn't actually bind
	// literal Postgres "$1"-style placeholders the way sql.Tx.ExecContext
	// does — it expects bun's own "?" placeholder convention (used by
	// every other raw query in this package, e.g. crypto.go's
	// pgp_sym_encrypt/decrypt calls) and rewrites those into positional
	// params itself. A hand-built "$1, $2, ..." query run through it comes
	// back "ERROR: there is no parameter $1" (confirmed against a real
	// Postgres 16 while building this) — NewRaw is bun's own raw-query
	// path and handles "?" correctly, so that's what buildInsertSQL now
	// emits.
	if _, err := tx.NewRaw(query, args...).Exec(ctx); err != nil {
		return fmt.Errorf("etl: insert batch for job %q: %w", job.Name, err)
	}

	if newWatermark != nil {
		if _, err := tx.NewInsert().
			Model(&JobState{JobID: job.ID, LastWatermark: newWatermark}).
			On("CONFLICT (job_id) DO UPDATE").
			Set("last_watermark = EXCLUDED.last_watermark").
			Set("updated_at = now()").
			Exec(ctx); err != nil {
			return fmt.Errorf("etl: save watermark for job %q: %w", job.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("etl: commit batch for job %q: %w", job.Name, err)
	}
	return nil
}

var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// quoteIdent validates and double-quotes a Postgres identifier.
// dest_schema/dest_table/dest_columns/conflict_columns come from
// app.etl_jobs, not end-user request input, but this still refuses
// anything that isn't a plain identifier rather than trusting a
// misconfigured (or malicious) job row to be well-formed SQL.
func quoteIdent(s string) (string, error) {
	if !identPattern.MatchString(s) {
		return "", fmt.Errorf("invalid identifier %q", s)
	}
	return `"` + s + `"`, nil
}

// buildInsertSQL builds a single parameterized multi-row INSERT for one
// batch, with an optional ON CONFLICT ... DO UPDATE upsert when
// job.ConflictColumns is set (see its comment in sql/etl_engine.sql).
func buildInsertSQL(job Job, batch [][]interface{}) (string, []interface{}, error) {
	schemaIdent, err := quoteIdent(job.DestSchema)
	if err != nil {
		return "", nil, err
	}
	tableIdent, err := quoteIdent(job.DestTable)
	if err != nil {
		return "", nil, err
	}
	colIdents := make([]string, len(job.DestColumns))
	for i, c := range job.DestColumns {
		ident, err := quoteIdent(c)
		if err != nil {
			return "", nil, err
		}
		colIdents[i] = ident
	}

	var sb strings.Builder
	sb.WriteString("INSERT INTO ")
	sb.WriteString(schemaIdent)
	sb.WriteString(".")
	sb.WriteString(tableIdent)
	sb.WriteString(" (")
	sb.WriteString(strings.Join(colIdents, ", "))
	sb.WriteString(") VALUES ")

	// "?" (bun's own placeholder convention), not hand-numbered "$1, $2,
	// ...": bun's NewRaw (which loadBatch executes this through) rewrites
	// "?" into the driver's real positional params itself — see
	// loadBatch's comment for why a literal "$1"-style placeholder run
	// through bun's query methods doesn't actually bind.
	args := make([]interface{}, 0, len(batch)*len(job.DestColumns))
	for rowIdx, row := range batch {
		if rowIdx > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(")
		for i, v := range row {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("?")
			args = append(args, v)
		}
		sb.WriteString(")")
	}

	if len(job.ConflictColumns) > 0 {
		conflictIdents := make([]string, len(job.ConflictColumns))
		conflictSet := make(map[string]bool, len(job.ConflictColumns))
		for i, c := range job.ConflictColumns {
			ident, err := quoteIdent(c)
			if err != nil {
				return "", nil, err
			}
			conflictIdents[i] = ident
			conflictSet[c] = true
		}
		sb.WriteString(" ON CONFLICT (")
		sb.WriteString(strings.Join(conflictIdents, ", "))
		sb.WriteString(") DO UPDATE SET ")
		setClauses := make([]string, 0, len(job.DestColumns))
		for i, c := range job.DestColumns {
			if conflictSet[c] {
				continue
			}
			setClauses = append(setClauses, fmt.Sprintf("%s = EXCLUDED.%s", colIdents[i], colIdents[i]))
		}
		if len(setClauses) == 0 {
			// Every dest column is part of the conflict key — nothing to
			// update, fall back to a no-op so a duplicate key doesn't error.
			sb.WriteString(colIdents[0])
			sb.WriteString(" = ")
			sb.WriteString(colIdents[0])
		} else {
			sb.WriteString(strings.Join(setClauses, ", "))
		}
	}

	return sb.String(), args, nil
}

// normalizeValue converts a scanned driver value into a form the
// destination Postgres driver encodes correctly. Drivers commonly return
// []byte for text-typed source columns (VARCHAR/TEXT/CLOB) rather than
// string; encoded verbatim, most Postgres drivers would write a []byte as
// bytea, not text, silently corrupting a text destination column. This
// engine targets ordinary business data (numbers, text, dates), not
// binary/blob source columns, so []byte is always treated as text — a
// genuinely binary source column is out of scope for v1.
func normalizeValue(v interface{}) interface{} {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// watermarkToString renders a scanned watermark column value as the text
// form persisted in app.etl_job_state and re-substituted (via
// formatWatermarkLiteral) into the next run's query.
func watermarkToString(v interface{}) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", errors.New("watermark column value is NULL")
	case string:
		return t, nil
	case time.Time:
		return t.Format(time.RFC3339Nano), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case int32:
		return strconv.FormatInt(int64(t), 10), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	default:
		return fmt.Sprintf("%v", t), nil
	}
}

func defaultWatermarkFor(job Job) string {
	if job.WatermarkType == nil {
		return ""
	}
	return defaultWatermark(*job.WatermarkType)
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

func insertRunStarted(ctx context.Context, destDB *bun.DB, jobID string) (int64, error) {
	run := &JobRun{
		JobID:     jobID,
		StartedAt: time.Now().UTC(),
		Status:    RunStatusRunning,
	}
	if _, err := destDB.NewInsert().Model(run).Returning("id").Exec(ctx); err != nil {
		return 0, err
	}
	return run.ID, nil
}

func finishRun(ctx context.Context, destDB *bun.DB, runID int64, status RunStatus, result runResult, errMsg string) error {
	now := time.Now().UTC()
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	_, err := destDB.NewUpdate().
		Model((*JobRun)(nil)).
		Set("finished_at = ?", now).
		Set("status = ?", status).
		Set("rows_extracted = ?", result.RowsExtracted).
		Set("rows_loaded = ?", result.RowsLoaded).
		Set("error_message = ?", errPtr).
		Where("id = ?", runID).
		Exec(ctx)
	return err
}

func loadJobState(ctx context.Context, destDB *bun.DB, jobID string) (*JobState, error) {
	state := new(JobState)
	err := destDB.NewSelect().Model(state).Where("job_id = ?", jobID).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("etl: load job state for job %s: %w", jobID, err)
	}
	return state, nil
}
