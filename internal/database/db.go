package database

import (
	"bknd-3/internal/config"
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/extra/bundebug"
)

// New connects to Postgres and returns a Bun DB handle.
func New(dsn string, cfg *config.Config) (*bun.DB, error) {
	// ✅ Create connector with increased timeouts
	connector := pgdriver.NewConnector(
		pgdriver.WithDSN(dsn),
		pgdriver.WithTimeout(120*time.Second),     // Overall connection timeout (2 min)
		pgdriver.WithDialTimeout(15*time.Second),  // Connection establishment timeout
		pgdriver.WithReadTimeout(120*time.Second), // Read operation timeout (2 min)
		pgdriver.WithWriteTimeout(30*time.Second), // Write operation timeout
	)

	sqldb := sql.OpenDB(connector)
	db := bun.NewDB(sqldb, pgdialect.New())

	// Configure connection pool
	sqldb.SetMaxOpenConns(25)                  // Max concurrent connections
	sqldb.SetMaxIdleConns(10)                  // ✅ Increased from 5 to 10
	sqldb.SetConnMaxLifetime(5 * time.Minute)  // Max connection lifetime
	sqldb.SetConnMaxIdleTime(10 * time.Minute) // Max idle time

	// Optional query logging
	if cfg.BunDebug {
		db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(true)))
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Verify connection first
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// ✅ Set search path and statement timeout
	_, err := db.ExecContext(ctx, `
		SET search_path TO app, public;
		SET statement_timeout = '120s';
		SET idle_in_transaction_session_timeout = '180s';
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to set database configuration: %w", err)
	}

	return db, nil
}
