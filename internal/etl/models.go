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

// Source is one external database this engine can pull from. Password is
// deliberately not a field here — see PasswordEnvVar's comment on the SQL
// side (sql/etl_engine.sql): it's read from the server's own environment
// at connect time, never stored in this table.
type Source struct {
	bun.BaseModel `bun:"table:app.etl_sources,alias:src"`

	ID             string            `bun:"id,pk"`
	Name           string            `bun:"name"`
	Kind           SourceKind        `bun:"kind"`
	Host           string            `bun:"host"`
	Port           int               `bun:"port"`
	DatabaseName   string            `bun:"database_name"`
	Username       string            `bun:"username"`
	PasswordEnvVar string            `bun:"password_env_var"`
	ExtraParams    map[string]string `bun:"extra_params,type:jsonb"`
	Enabled        bool              `bun:"enabled"`
}

// Job is one (source query -> destination table) pull on its own schedule.
// See sql/etl_engine.sql's comment on SourceQuery for the {{WATERMARK}}
// substitution contract and on DestColumns for the positional (not
// by-name) column mapping.
type Job struct {
	bun.BaseModel `bun:"table:app.etl_jobs,alias:j"`

	ID              string         `bun:"id,pk"`
	Name            string         `bun:"name"`
	SourceID        string         `bun:"source_id"`
	SourceQuery     string         `bun:"source_query"`
	DestSchema      string         `bun:"dest_schema"`
	DestTable       string         `bun:"dest_table"`
	DestColumns     []string       `bun:"dest_columns,array"`
	Mode            JobMode        `bun:"mode"`
	WatermarkColumn *string        `bun:"watermark_column"`
	WatermarkType   *WatermarkType `bun:"watermark_type"`
	ConflictColumns []string       `bun:"conflict_columns,array"`
	TriggerTimes    []string       `bun:"trigger_times,array"`
	BatchSize       int            `bun:"batch_size"`
	TimeoutSeconds  int            `bun:"timeout_seconds"`
	Enabled         bool           `bun:"enabled"`
}

// JobState is the incremental watermark for a job — same "persist right
// before commit" contract as app.migration_checkpoints elsewhere in this
// repo, keyed per job instead of a single hardcoded procedure name.
type JobState struct {
	bun.BaseModel `bun:"table:app.etl_job_state,alias:st"`

	JobID         string    `bun:"job_id,pk"`
	LastWatermark *string   `bun:"last_watermark"`
	UpdatedAt     time.Time `bun:"updated_at"`
}

// JobRun is one execution attempt, for observability. Written 'running' at
// start, updated to 'success'/'failed' at the end — a row stuck on
// 'running' is itself the signal that a run crashed mid-flight.
type JobRun struct {
	bun.BaseModel `bun:"table:app.etl_job_runs,alias:run"`

	ID            int64      `bun:"id,pk,autoincrement"`
	JobID         string     `bun:"job_id"`
	StartedAt     time.Time  `bun:"started_at"`
	FinishedAt    *time.Time `bun:"finished_at"`
	Status        RunStatus  `bun:"status"`
	RowsExtracted int64      `bun:"rows_extracted"`
	RowsLoaded    int64      `bun:"rows_loaded"`
	ErrorMessage  *string    `bun:"error_message"`
}
