package db

import (
	"database/sql"
	"fmt"
	"os"
)

// Migrate runs database migrations
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

	// Check if migration already applied
	var version string
	err = db.QueryRow("SELECT version FROM schema_migrations WHERE version = 'initial'").Scan(&version)
	if err == nil {
		// Migration already applied
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("failed to check migration status: %w", err)
	}

	// Read schema file
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("failed to read schema file: %w", err)
	}

	// Execute schema in a transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Execute schema
	if _, err := tx.Exec(string(schema)); err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}

	// Record migration
	if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES ('initial')"); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration: %w", err)
	}

	return nil
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
