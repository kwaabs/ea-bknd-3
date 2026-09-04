package etl

import (
	"bknd-3/internal/models"
	"bknd-3/internal/notifyemail"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var (
	// ErrForbidden means the authenticated caller isn't in the notify-emails
	// allowlist — same convention as meters.ErrForbidden.
	ErrForbidden = errors.New("forbidden")
	// ErrNotFound means the given source/job id doesn't exist.
	ErrNotFound = errors.New("not found")
)

// Service is the CRUD + test-run backend for the ETL admin API. Engine is
// used only for TriggerNow (Run now) and its shared tryLock/overlap-guard
// state — every read/write here otherwise talks to Postgres directly, the
// same as the Engine's own reload/runJob path, so a change made through
// this Service is visible to the Engine within one reload cycle without
// any direct coupling beyond that one call. credentialsKey is
// config.ETLCredentialsKey — see crypto.go and
// sql/etl_sources_password_encryption.sql.
type Service struct {
	db             *bun.DB
	notifyEmails   *notifyemail.Service
	engine         *Engine
	credentialsKey string
}

func NewService(db *bun.DB, notifyEmails *notifyemail.Service, engine *Engine, credentialsKey string) *Service {
	return &Service{db: db, notifyEmails: notifyEmails, engine: engine, credentialsKey: credentialsKey}
}

// ResolveNotifyEmail mirrors meters.Service.ResolveNotifyEmail exactly —
// same allowlist-by-email, session-derived-identity pattern, duplicated
// per package rather than shared, matching this codebase's existing
// convention (notifyemail.Handler does the same).
func (s *Service) ResolveNotifyEmail(ctx context.Context, userID string) (string, error) {
	var u models.User
	if err := s.db.NewSelect().Model(&u).Where("id = ?", userID).Scan(ctx); err != nil {
		return "", err
	}
	allowed, err := s.notifyEmails.IsAllowed(ctx, u.Email)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", ErrForbidden
	}
	return u.Email, nil
}

// ---------------------------------------------------------------------
// Sources
// ---------------------------------------------------------------------

type SourceInput struct {
	Name         string     `json:"name"`
	Kind         SourceKind `json:"kind"`
	Host         string     `json:"host"`
	Port         int        `json:"port"`
	DatabaseName string     `json:"database_name"`
	Username     string     `json:"username"`
	// Password is write-only and optional on update: nil or empty means
	// "leave the currently-stored password as-is" (the Edit Source form
	// never receives the plaintext back to prefill, so it always submits
	// this empty unless the operator is actively rotating it). Required
	// (non-empty) on create. See UpdateSource/CreateSource.
	Password    *string           `json:"password"`
	ExtraParams map[string]string `json:"extra_params"`
	Enabled     bool              `json:"enabled"`
}

func (in SourceInput) validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("name is required")
	}
	switch in.Kind {
	case KindOracle, KindMSSQL, KindPostgres:
	default:
		return fmt.Errorf("kind must be one of %q, %q, %q", KindOracle, KindMSSQL, KindPostgres)
	}
	if strings.TrimSpace(in.Host) == "" {
		return errors.New("host is required")
	}
	if in.Port <= 0 || in.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if strings.TrimSpace(in.DatabaseName) == "" {
		return errors.New("database_name is required")
	}
	if strings.TrimSpace(in.Username) == "" {
		return errors.New("username is required")
	}
	return nil
}

func (s *Service) ListSources(ctx context.Context) ([]Source, error) {
	var sources []Source
	if err := s.db.NewSelect().Model(&sources).OrderExpr("name ASC").Scan(ctx); err != nil {
		return nil, err
	}
	for i := range sources {
		sources[i].HasPassword = len(sources[i].PasswordEncrypted) > 0
	}
	return sources, nil
}

func (s *Service) CreateSource(ctx context.Context, in SourceInput) (*Source, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.Password == nil || strings.TrimSpace(*in.Password) == "" {
		return nil, errors.New("password is required")
	}
	if in.ExtraParams == nil {
		in.ExtraParams = map[string]string{}
	}
	encrypted, err := encryptPassword(ctx, s.db, s.credentialsKey, *in.Password)
	if err != nil {
		return nil, err
	}
	src := &Source{
		ID:                uuid.New().String(),
		Name:              in.Name,
		Kind:              in.Kind,
		Host:              in.Host,
		Port:              in.Port,
		DatabaseName:      in.DatabaseName,
		Username:          in.Username,
		PasswordEncrypted: encrypted,
		ExtraParams:       in.ExtraParams,
		Enabled:           in.Enabled,
	}
	if _, err := s.db.NewInsert().Model(src).Exec(ctx); err != nil {
		return nil, err
	}
	src.HasPassword = true
	return src, nil
}

// UpdateSource rotates the stored password only when in.Password is
// provided (non-empty) — the column simply isn't included in the UPDATE
// otherwise, so an edit that only changes e.g. enabled/host never touches
// the existing encrypted password.
func (s *Service) UpdateSource(ctx context.Context, id string, in SourceInput) (*Source, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.ExtraParams == nil {
		in.ExtraParams = map[string]string{}
	}
	src := &Source{
		ID:           id,
		Name:         in.Name,
		Kind:         in.Kind,
		Host:         in.Host,
		Port:         in.Port,
		DatabaseName: in.DatabaseName,
		Username:     in.Username,
		ExtraParams:  in.ExtraParams,
		Enabled:      in.Enabled,
	}
	columns := []string{"name", "kind", "host", "port", "database_name", "username", "extra_params", "enabled"}
	if in.Password != nil && strings.TrimSpace(*in.Password) != "" {
		encrypted, err := encryptPassword(ctx, s.db, s.credentialsKey, *in.Password)
		if err != nil {
			return nil, err
		}
		src.PasswordEncrypted = encrypted
		columns = append(columns, "password_encrypted")
	}

	res, err := s.db.NewUpdate().Model(src).
		Column(columns...).
		Set("updated_at = now()").
		WherePK().
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	if len(src.PasswordEncrypted) == 0 {
		// Password wasn't part of this update — report the existing
		// stored state rather than a misleading "no password" from the
		// zero-value we never fetched.
		var existing Source
		if err := s.db.NewSelect().Model(&existing).Column("password_encrypted").Where("id = ?", id).Scan(ctx); err == nil {
			src.HasPassword = len(existing.PasswordEncrypted) > 0
		}
	} else {
		src.HasPassword = true
	}
	return src, nil
}

// DeleteSource fails with the database's own foreign-key error (surfaced
// as-is by the handler) if any app.etl_jobs row still references it —
// app.etl_sources.id is ON DELETE RESTRICT on purpose, so a source can't
// be pulled out from under a job that still expects it to exist.
func (s *Service) DeleteSource(ctx context.Context, id string) error {
	res, err := s.db.NewDelete().Model((*Source)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TestConnection opens a connection to a saved source and pings it —
// nothing more. Fetches the source fresh (not through Engine's cache) so
// it always reflects the current row, including one not yet enabled.
func (s *Service) TestConnection(ctx context.Context, sourceID string) (time.Duration, error) {
	src := new(Source)
	if err := s.db.NewSelect().Model(src).Where("id = ?", sourceID).Scan(ctx); err != nil {
		return 0, ErrNotFound
	}
	password, err := decryptPassword(ctx, s.db, s.credentialsKey, src.PasswordEncrypted)
	if err != nil {
		return 0, err
	}
	return pingSource(ctx, *src, password)
}

// TestConnectionDraft is the same connect-and-ping check as TestConnection,
// but against connection details that haven't been saved as a source yet —
// the "does this work" check while filling out the Add Source form, before
// committing to a row. The password is used directly (no encrypt/decrypt
// round trip needed for a check that never touches app.etl_sources).
func (s *Service) TestConnectionDraft(ctx context.Context, in SourceInput) (time.Duration, error) {
	if err := in.validate(); err != nil {
		return 0, err
	}
	if in.Password == nil || strings.TrimSpace(*in.Password) == "" {
		return 0, errors.New("password is required to test a connection")
	}
	src := Source{
		Kind:         in.Kind,
		Host:         in.Host,
		Port:         in.Port,
		DatabaseName: in.DatabaseName,
		Username:     in.Username,
		ExtraParams:  in.ExtraParams,
	}
	return pingSource(ctx, src, *in.Password)
}

func pingSource(ctx context.Context, src Source, password string) (time.Duration, error) {
	db, err := openSource(src, password)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()

	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	started := time.Now()
	if err := db.PingContext(pingCtx); err != nil {
		return time.Since(started), err
	}
	return time.Since(started), nil
}

// ---------------------------------------------------------------------
// Jobs
// ---------------------------------------------------------------------

type JobInput struct {
	Name            string         `json:"name"`
	SourceID        string         `json:"source_id"`
	SourceQuery     string         `json:"source_query"`
	DestSchema      string         `json:"dest_schema"`
	DestTable       string         `json:"dest_table"`
	DestColumns     []string       `json:"dest_columns"`
	Mode            JobMode        `json:"mode"`
	WatermarkColumn *string        `json:"watermark_column"`
	WatermarkType   *WatermarkType `json:"watermark_type"`
	ConflictColumns []string       `json:"conflict_columns"`
	TriggerTimes    []string       `json:"trigger_times"`
	BatchSize       int            `json:"batch_size"`
	TimeoutSeconds  int            `json:"timeout_seconds"`
	Enabled         bool           `json:"enabled"`
}

func (in *JobInput) applyDefaults() {
	if in.DestSchema == "" {
		in.DestSchema = "app"
	}
	if in.BatchSize <= 0 {
		in.BatchSize = 5000
	}
	if in.TimeoutSeconds <= 0 {
		in.TimeoutSeconds = 3600
	}
}

func (in JobInput) validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(in.SourceID) == "" {
		return errors.New("source_id is required")
	}
	if err := isReadOnlyQuery(in.SourceQuery); err != nil {
		return fmt.Errorf("source_query: %w", err)
	}
	if len(in.DestColumns) == 0 {
		return errors.New("dest_columns must have at least one column")
	}
	switch in.Mode {
	case ModeFullRefresh, ModeIncremental:
	default:
		return fmt.Errorf("mode must be %q or %q", ModeFullRefresh, ModeIncremental)
	}
	if in.Mode == ModeIncremental {
		if in.WatermarkColumn == nil || strings.TrimSpace(*in.WatermarkColumn) == "" {
			return errors.New("watermark_column is required for an incremental job")
		}
		if in.WatermarkType == nil {
			return errors.New("watermark_type is required for an incremental job")
		}
		switch *in.WatermarkType {
		case WatermarkTimestamp, WatermarkInteger, WatermarkString:
		default:
			return fmt.Errorf("watermark_type must be one of %q, %q, %q", WatermarkTimestamp, WatermarkInteger, WatermarkString)
		}
		if indexOf(in.DestColumns, *in.WatermarkColumn) == -1 {
			return errors.New("watermark_column must also appear in dest_columns")
		}
	}
	if len(in.TriggerTimes) == 0 {
		return errors.New("trigger_times must have at least one entry")
	}
	for _, t := range in.TriggerTimes {
		if _, err := time.Parse("15:04", t); err != nil {
			return fmt.Errorf("invalid trigger time %q, expected HH:MM (24h)", t)
		}
	}

	// Reuse the same identifier and query-shape validation the engine
	// itself relies on at run time, so a bad job is caught at save time
	// rather than at 2am.
	if _, err := quoteIdent(in.DestSchema); err != nil {
		return fmt.Errorf("dest_schema: %w", err)
	}
	if _, err := quoteIdent(in.DestTable); err != nil {
		return fmt.Errorf("dest_table: %w", err)
	}
	for _, c := range in.DestColumns {
		if _, err := quoteIdent(c); err != nil {
			return fmt.Errorf("dest_columns: %w", err)
		}
	}
	for _, c := range in.ConflictColumns {
		if _, err := quoteIdent(c); err != nil {
			return fmt.Errorf("conflict_columns: %w", err)
		}
		if indexOf(in.DestColumns, c) == -1 {
			return fmt.Errorf("conflict_columns: %q is not in dest_columns", c)
		}
	}
	probeJob := Job{
		Name:          "(validation)",
		Mode:          in.Mode,
		WatermarkType: in.WatermarkType,
		SourceQuery:   in.SourceQuery,
	}
	if _, err := buildQuery(probeJob, defaultWatermarkFor(probeJob)); err != nil {
		return err
	}
	return nil
}

var disallowedSQLKeyword = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|ALTER|TRUNCATE|CREATE|GRANT|REVOKE|EXEC|EXECUTE|CALL|MERGE|REPLACE|VACUUM)\b`)

// isReadOnlyQuery is a best-effort guard, not a SQL parser: it rejects
// anything that isn't a single SELECT/WITH statement and any of a
// denylist of mutating/DDL keywords appearing anywhere in it. This is
// defense in depth for an admin-gated feature that can reach arbitrary
// external databases — it is NOT a substitute for the source's own
// database user actually being read-only, which is the real protection
// and worth calling out to whoever registers a source.
func isReadOnlyQuery(q string) error {
	trimmed := strings.TrimSpace(q)
	if trimmed == "" {
		return errors.New("query is empty")
	}
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return errors.New("only a SELECT (or WITH ... SELECT) statement is allowed")
	}
	body := strings.TrimRight(trimmed, "; \t\n\r")
	if strings.Contains(body, ";") {
		return errors.New("multiple statements are not allowed")
	}
	if disallowedSQLKeyword.MatchString(body) {
		return errors.New("query must be read-only — no INSERT/UPDATE/DELETE/DDL/EXEC/etc.")
	}
	return nil
}

func (s *Service) ListJobs(ctx context.Context) ([]Job, error) {
	var jobs []Job
	err := s.db.NewSelect().Model(&jobs).OrderExpr("name ASC").Scan(ctx)
	return jobs, err
}

func jobFromInput(id string, in JobInput) *Job {
	return &Job{
		ID:              id,
		Name:            in.Name,
		SourceID:        in.SourceID,
		SourceQuery:     in.SourceQuery,
		DestSchema:      in.DestSchema,
		DestTable:       in.DestTable,
		DestColumns:     in.DestColumns,
		Mode:            in.Mode,
		WatermarkColumn: in.WatermarkColumn,
		WatermarkType:   in.WatermarkType,
		ConflictColumns: in.ConflictColumns,
		TriggerTimes:    in.TriggerTimes,
		BatchSize:       in.BatchSize,
		TimeoutSeconds:  in.TimeoutSeconds,
		Enabled:         in.Enabled,
	}
}

func (s *Service) CreateJob(ctx context.Context, in JobInput) (*Job, error) {
	in.applyDefaults()
	if err := in.validate(); err != nil {
		return nil, err
	}
	job := jobFromInput(uuid.New().String(), in)
	if _, err := s.db.NewInsert().Model(job).Exec(ctx); err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Service) UpdateJob(ctx context.Context, id string, in JobInput) (*Job, error) {
	in.applyDefaults()
	if err := in.validate(); err != nil {
		return nil, err
	}
	job := jobFromInput(id, in)
	res, err := s.db.NewUpdate().Model(job).
		Column("name", "source_id", "source_query", "dest_schema", "dest_table", "dest_columns",
			"mode", "watermark_column", "watermark_type", "conflict_columns", "trigger_times",
			"batch_size", "timeout_seconds", "enabled").
		Set("updated_at = now()").
		WherePK().
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return job, nil
}

func (s *Service) DeleteJob(ctx context.Context, id string) error {
	res, err := s.db.NewDelete().Model((*Job)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RunNow triggers a job outside its schedule via the Engine — see
// Engine.TriggerNow for the full contract (async, returns a run id
// immediately, works even if the job/source is disabled).
func (s *Service) RunNow(ctx context.Context, jobID string) (int64, error) {
	if s.engine == nil {
		return 0, errors.New("etl engine is not available")
	}
	return s.engine.TriggerNow(ctx, jobID)
}

func (s *Service) ListRuns(ctx context.Context, jobID string, limit int) ([]JobRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	var runs []JobRun
	err := s.db.NewSelect().Model(&runs).
		Where("job_id = ?", jobID).
		OrderExpr("started_at DESC").
		Limit(limit).
		Scan(ctx)
	return runs, err
}

func (s *Service) GetJobState(ctx context.Context, jobID string) (*JobState, error) {
	state, err := loadJobState(ctx, s.db, jobID)
	if err != nil {
		return nil, err
	}
	return state, nil // nil, nil if the job has never run yet — not an error
}

// ---------------------------------------------------------------------
// Ad-hoc test query
// ---------------------------------------------------------------------

const testQueryMaxRows = 200

type TestQueryResult struct {
	Columns   []string        `json:"columns"`
	Rows      [][]interface{} `json:"rows"`
	Truncated bool            `json:"truncated"`
	ElapsedMs int64           `json:"elapsed_ms"`
}

// TestQuery runs an arbitrary read-only query against a source and returns
// a preview — a handful of sample rows, a `SELECT COUNT(*) ...`, a
// `SELECT COUNT(DISTINCT col) ...`, whatever the caller wants to check
// before committing a job's source_query. It never touches a destination
// table or app.etl_job_state; this is purely "does this query work and
// what does it return," the planning/validation step before a job is
// created or its schedule is trusted.
//
// Capped at testQueryMaxRows regardless of what the query would otherwise
// return, and run under a short fixed timeout — this is an interactive
// check, not a batch pull (a job's real run uses its own timeout_seconds
// and has no row cap).
func (s *Service) TestQuery(ctx context.Context, sourceID, query string) (*TestQueryResult, error) {
	if err := isReadOnlyQuery(query); err != nil {
		return nil, err
	}

	src := new(Source)
	if err := s.db.NewSelect().Model(src).Where("id = ?", sourceID).Scan(ctx); err != nil {
		return nil, ErrNotFound
	}
	password, err := decryptPassword(ctx, s.db, s.credentialsKey, src.PasswordEncrypted)
	if err != nil {
		return nil, err
	}
	db, err := openSource(*src, password)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	started := time.Now()
	rows, err := db.QueryContext(queryCtx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	result := &TestQueryResult{Columns: cols, Rows: make([][]interface{}, 0, testQueryMaxRows)}
	for rows.Next() {
		if len(result.Rows) >= testQueryMaxRows {
			result.Truncated = true
			break
		}
		values := make([]interface{}, len(cols))
		scanArgs := make([]interface{}, len(cols))
		for i := range values {
			scanArgs[i] = &values[i]
		}
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}
		for i := range values {
			values[i] = normalizeValue(values[i])
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result.ElapsedMs = time.Since(started).Milliseconds()
	return result, nil
}
