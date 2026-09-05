// Package etl is a small in-process data-loading engine: it pulls rows out
// of external databases (Oracle, MSSQL, Postgres today) on a schedule and
// lands them into a table in this app's own `app` schema. It intentionally
// stops there — no transform-on-the-way-in. A landing table this engine
// populates is meant to be read by a separate PL/pgSQL procedure (the same
// pattern already used for MMS/Zeus — see
// sql/populate_mms_customer_sales_from_raw.sql) that merges/transforms it
// into the real app-facing table.
//
// Sources and jobs are rows in Postgres (sql/etl_engine.sql), not Go code —
// adding a new nightly pull is an INSERT, not a redeploy. Engine.reload
// (engine.go) re-reads those tables every few minutes so new/edited rows
// take effect without a restart.
package etl

import (
	"time"

	"github.com/uptrace/bun"
)

type SourceKind string

const (
	KindOracle   SourceKind = "oracle"
	KindMSSQL    SourceKind = "mssql"
	KindPostgres SourceKind = "postgres"
	// KindHTTPAPI pulls paginated JSON out of an HTTP API instead of a SQL
	// database — see httpsource.go. Reuses Source's Host/Username/
	// PasswordEncrypted fields as base URL / api-id / api-key respectively
	// (see sql/etl_http_api_sources.sql's comment); Port/DatabaseName are
	// unused for this kind.
	KindHTTPAPI SourceKind = "http_api"
)

type JobMode string

const (
	ModeFullRefresh JobMode = "full_refresh"
	ModeIncremental JobMode = "incremental"
)

type WatermarkType string

const (
	WatermarkTimestamp WatermarkType = "timestamp"
	WatermarkInteger   WatermarkType = "integer"
	WatermarkString    WatermarkType = "string"
)

type RunStatus string

const (
	RunStatusRunning RunStatus = "running"
	RunStatusSuccess RunStatus = "success"
	RunStatusFailed  RunStatus = "failed"
)

// Source is one external database this engine can pull from. The password
// is stored encrypted (pgcrypto PGP symmetric, key in
// config.ETLCredentialsKey — see sql/etl_sources_password_encryption.sql)
// rather than plaintext; PasswordEncrypted is deliberately tagged
// json:"-" so the ciphertext blob never reaches an HTTP response, even
// though it isn't the plaintext password.
type Source struct {
	bun.BaseModel `bun:"table:app.etl_sources,alias:src"`

	ID                string            `bun:"id,pk"                          json:"id"`
	Name              string            `bun:"name"                           json:"name"`
	Kind              SourceKind        `bun:"kind"                           json:"kind"`
	Host              string            `bun:"host"                           json:"host"`
	Port              int               `bun:"port"                           json:"port"`
	DatabaseName      string            `bun:"database_name"                  json:"database_name"`
	Username          string            `bun:"username"                       json:"username"`
	PasswordEncrypted []byte            `bun:"password_encrypted"             json:"-"`
	ExtraParams       map[string]string `bun:"extra_params,type:jsonb"        json:"extra_params"`
	Enabled           bool              `bun:"enabled"                        json:"enabled"`

	// HasPassword is computed after every read (see service.go), never
	// stored — lets the UI show "password set" / "no password" without
	// ever exposing PasswordEncrypted itself.
	HasPassword bool `bun:"-" json:"has_password"`
}

// Job is one (source query -> destination table) pull on its own schedule.
// See sql/etl_engine.sql's comment on SourceQuery for the {{WATERMARK}}
// substitution contract and on DestColumns for the positional (not
// by-name) column mapping.
type Job struct {
	bun.BaseModel `bun:"table:app.etl_jobs,alias:j"`

	ID              string         `bun:"id,pk"                 json:"id"`
	Name            string         `bun:"name"                  json:"name"`
	SourceID        string         `bun:"source_id"             json:"source_id"`
	SourceQuery     string         `bun:"source_query"          json:"source_query"`
	DestSchema      string         `bun:"dest_schema"           json:"dest_schema"`
	DestTable       string         `bun:"dest_table"            json:"dest_table"`
	DestColumns     []string       `bun:"dest_columns,array"    json:"dest_columns"`
	Mode            JobMode        `bun:"mode"                  json:"mode"`
	WatermarkColumn *string        `bun:"watermark_column"      json:"watermark_column"`
	WatermarkType   *WatermarkType `bun:"watermark_type"        json:"watermark_type"`
	ConflictColumns []string       `bun:"conflict_columns,array" json:"conflict_columns"`
	TriggerTimes    []string       `bun:"trigger_times,array"   json:"trigger_times"`
	BatchSize       int            `bun:"batch_size"            json:"batch_size"`
	TimeoutSeconds  int            `bun:"timeout_seconds"       json:"timeout_seconds"`
	Enabled         bool           `bun:"enabled"                json:"enabled"`

	// FilterQuery, when set, is a SELECT run against THIS app database
	// (never the external source — see loadFilterValues in run.go) whose
	// single result column seeds the {{FILTER}} token in SourceQuery,
	// chunked into FilterBatchSize-sized groups (one source_query run per
	// chunk). This is how a job pulls only the rows matching a list of
	// keys that live in this database (e.g. app.meters) — the engine has
	// no other way to reach across a job's one external source and this
	// app database in a single query. Only valid for mode=full_refresh
	// (see extractAndLoadFiltered's comment for why).
	FilterQuery     *string `bun:"filter_query"      json:"filter_query"`
	FilterBatchSize *int    `bun:"filter_batch_size" json:"filter_batch_size"`

	// SourceFields, RecordsPath, PageSize are only meaningful when this
	// job's source is Kind == KindHTTPAPI — see httpsource.go.
	//
	// SourceFields is the http_api analog of "the order source_query's
	// SELECT list returns columns in" for a SQL job: a JSON object has no
	// reliable field order of its own, so unlike the SQL kinds (where
	// dest_columns[i] implicitly names column i of the SELECT list),
	// http_api jobs must say explicitly which JSON field (dot-path, e.g.
	// "region.name" for a nested field) fills dest_columns[i]. Always the
	// same length as DestColumns.
	SourceFields []string `bun:"source_fields,array" json:"source_fields"`
	// RecordsPath is the dot-path to the JSON array of records within each
	// page's response body, e.g. "rows" or "data.items". Defaults to
	// "data" (see applyDefaults) if left blank.
	RecordsPath string `bun:"records_path" json:"records_path"`
	// PageSize is the "limit" value requested per HTTP page — a page
	// shorter than this ends pagination. Independent of BatchSize, which
	// governs how many extracted rows accumulate before one INSERT into
	// the destination.
	PageSize int `bun:"page_size" json:"page_size"`
}

// JobState is the incremental watermark for a job — same "persist right
// before commit" contract as app.migration_checkpoints elsewhere in this
// repo, keyed per job instead of a single hardcoded procedure name.
type JobState struct {
	bun.BaseModel `bun:"table:app.etl_job_state,alias:st"`

	JobID         string    `bun:"job_id,pk"      json:"job_id"`
	LastWatermark *string   `bun:"last_watermark" json:"last_watermark"`
	UpdatedAt     time.Time `bun:"updated_at"     json:"updated_at"`
}

// JobRun is one execution attempt, for observability. Written 'running' at
// start, updated to 'success'/'failed' at the end — a row stuck on
// 'running' is itself the signal that a run crashed mid-flight.
type JobRun struct {
	bun.BaseModel `bun:"table:app.etl_job_runs,alias:run"`

	ID            int64      `bun:"id,pk,autoincrement" json:"id"`
	JobID         string     `bun:"job_id"              json:"job_id"`
	StartedAt     time.Time  `bun:"started_at"          json:"started_at"`
	FinishedAt    *time.Time `bun:"finished_at"         json:"finished_at"`
	Status        RunStatus  `bun:"status"              json:"status"`
	RowsExtracted int64      `bun:"rows_extracted"      json:"rows_extracted"`
	RowsLoaded    int64      `bun:"rows_loaded"         json:"rows_loaded"`
	ErrorMessage  *string    `bun:"error_message"       json:"error_message"`
}

// DestColumnInfo describes one column of a candidate destination table —
// read straight from information_schema.columns (see
// Service.ListDestTableColumns), not a bun model of its own table, so it's
// just a plain scan target rather than a BaseModel-backed struct.
type DestColumnInfo struct {
	Name     string `bun:"name"      json:"name"`
	DataType string `bun:"data_type" json:"data_type"`
}
