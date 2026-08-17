package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	Global *sql.DB
}

// Open opens the global database and ensures WAL mode is enabled
func Open(globalDBPath string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(globalDBPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	// Open database
	db, err := sql.Open("sqlite", globalDBPath+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set WAL mode and other pragmas
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA wal_autocheckpoint = 1000",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to set pragma: %w", err)
		}
	}

	return &DB{Global: db}, nil
}

// Close closes the database connection
func (d *DB) Close() error {
	if d.Global != nil {
		return d.Global.Close()
	}
	return nil
}

// GetJournalMode returns the current journal mode
func (d *DB) GetJournalMode() (string, error) {
	var mode string
	err := d.Global.QueryRow("PRAGMA journal_mode").Scan(&mode)
	return mode, err
}

// OpenTeamDB opens a team-specific database
func OpenTeamDB(teamsDir, teamID string) (*sql.DB, error) {
	teamDir := filepath.Join(teamsDir, teamID)
	if err := os.MkdirAll(teamDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create team directory: %w", err)
	}

	dbPath := filepath.Join(teamDir, "memory.db")
	db, err := sql.Open("sqlite", dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open team database: %w", err)
	}

	// Set WAL mode
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to set pragma: %w", err)
		}
	}

	return db, nil
}
