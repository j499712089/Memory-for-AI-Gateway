package db

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

// Migrate runs database migrations.
//
// schema.sql is fully idempotent (every statement is CREATE TABLE/INDEX IF NOT
// EXISTS), so it is applied on every startup. This backfills tables added after
// a database was first created — an older database that already recorded the
// 'initial' version still picks up new tables (e.g. channel_aliases) on its
// next boot without a destructive re-migration.
func Migrate(db *sql.DB, schemaPath string) error {
	// Check if schema_migrations table exists
	var tableName string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&tableName)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to check migrations table: %w", err)
	}

	// Create schema_migrations table if it doesn't exist
	if err == sql.ErrNoRows {
		_, err = db.Exec(`
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version TEXT PRIMARY KEY,
				applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
			)
		`)
		if err != nil {
			return fmt.Errorf("failed to create migrations table: %w", err)
		}
	}

	// Read schema file
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("failed to read schema file: %w", err)
	}

	// Execute the schema transaction with a short retry on transient
	// SQLITE_BUSY. A concurrent writer — for example the worker replaying
	// buffers while the gateway restarts mid-turn — can hold the write lock
	// longer than busy_timeout; a few bounded retries let the gateway boot
	// instead of aborting startup on a transient lock.
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		lastErr = runMigrationTx(db, schema)
		if lastErr == nil {
			return nil
		}
		if !isBusyError(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

// runMigrationTx applies the idempotent schema and records the migration in a
// single transaction. Idempotent by design (see Migrate docs).
func runMigrationTx(db *sql.DB, schema []byte) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Execute schema
	if _, err := tx.Exec(string(schema)); err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}

	// Record the initial migration idempotently. A database that already has
	// the 'initial' version simply ignores this insert.
	if _, err := tx.Exec("INSERT OR IGNORE INTO schema_migrations (version) VALUES ('initial')"); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration: %w", err)
	}

	return nil
}

// isBusyError reports whether err is a transient SQLite write-lock contention
// (SQLITE_BUSY / SQLITE_LOCKED). Keep the match narrow so real failures still
// abort startup.
func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "database is busy")
}

// CheckMigrationIdempotency verifies that running migrations twice doesn't cause errors
func CheckMigrationIdempotency(db *sql.DB, schemaPath string) error {
	// Run migration first time
	if err := Migrate(db, schemaPath); err != nil {
		return fmt.Errorf("first migration failed: %w", err)
	}

	// Run migration second time (should be idempotent)
	if err := Migrate(db, schemaPath); err != nil {
		return fmt.Errorf("second migration failed (not idempotent): %w", err)
	}

	// Check that only one migration record exists
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 'initial'").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to count migrations: %w", err)
	}

	if count != 1 {
		return fmt.Errorf("expected 1 migration record, got %d", count)
	}

	return nil
}
