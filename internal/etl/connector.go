package etl

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"time"

	// Blank-imported for their database/sql driver registration side effect
	// only (go-ora is also called directly below for BuildUrl, so it's a
	// normal import; go-mssqldb is referenced only by its driver name
	// string "sqlserver" in sql.Open below, so it MUST be imported here
	// even though nothing in this file calls it directly — without this,
	// `go mod tidy` drops it as unused and sql.Open("sqlserver", ...)
	// fails at runtime with "unknown driver").
	_ "github.com/microsoft/go-mssqldb"
	go_ora "github.com/sijms/go-ora/v2"
	"github.com/uptrace/bun/driver/pgdriver"
)

// openSource opens a *sql.DB for one Source, per its Kind. The returned DB
// is a real connection pool (not a single connection) — callers should keep
// it open for reuse across runs of jobs against the same source rather than
// opening a fresh one per run; see Engine's sourcePool cache in engine.go.
func openSource(src Source) (*sql.DB, error) {
	password := os.Getenv(src.PasswordEnvVar)
	if password == "" {
		return nil, fmt.Errorf("etl: env var %q (source %q password) is unset or empty", src.PasswordEnvVar, src.Name)
	}

	switch src.Kind {
	case KindOracle:
		dsn := go_ora.BuildUrl(src.Host, src.Port, src.DatabaseName, src.Username, password, src.ExtraParams)
		db, err := sql.Open("oracle", dsn)
		if err != nil {
			return nil, fmt.Errorf("etl: open oracle source %q: %w", src.Name, err)
		}
		configurePool(db)
		return db, nil

	case KindMSSQL:
		q := url.Values{}
		q.Set("database", src.DatabaseName)
		for k, v := range src.ExtraParams {
			q.Set(k, v)
		}
		u := url.URL{
			Scheme:   "sqlserver",
			User:     url.UserPassword(src.Username, password),
			Host:     fmt.Sprintf("%s:%d", src.Host, src.Port),
			RawQuery: q.Encode(),
		}
		db, err := sql.Open("sqlserver", u.String())
		if err != nil {
			return nil, fmt.Errorf("etl: open mssql source %q: %w", src.Name, err)
		}
		configurePool(db)
		return db, nil

	case KindPostgres:
		// Same connector construction as internal/database.New, kept
		// consistent rather than introducing a second way to build a
		// Postgres DSN in this codebase.
		dsn := fmt.Sprintf(
			"postgres://%s:%s@%s:%d/%s",
			url.QueryEscape(src.Username), url.QueryEscape(password),
			src.Host, src.Port, src.DatabaseName,
		)
		if sslmode, ok := src.ExtraParams["sslmode"]; ok {
			dsn += "?sslmode=" + url.QueryEscape(sslmode)
		}
		connector := pgdriver.NewConnector(
			pgdriver.WithDSN(dsn),
			pgdriver.WithTimeout(60*time.Second),
			pgdriver.WithDialTimeout(15*time.Second),
			pgdriver.WithReadTimeout(60*time.Second),
			pgdriver.WithWriteTimeout(30*time.Second),
		)
		db := sql.OpenDB(connector)
		configurePool(db)
		return db, nil

	default:
		return nil, fmt.Errorf("etl: unknown source kind %q for source %q", src.Kind, src.Name)
	}
}

// configurePool keeps source-side pools small and short-lived — these are
// nightly batch pulls against systems this app doesn't own, not the
// app's own high-throughput Postgres pool (see internal/database.New for
// that one's much larger settings).
func configurePool(db *sql.DB) {
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
}
