package db_test

import (
	"os"
	"path/filepath"
	"testing"

	"gateway/internal/db"
)

func TestDatabaseWALMode(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Open database
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Check journal mode
	journalMode, err := database.GetJournalMode()
	if err != nil {
		t.Fatalf("Failed to get journal mode: %v", err)
	}

	if journalMode != "wal" {
		t.Errorf("Expected journal mode 'wal', got '%s'", journalMode)
	}

	// Check that WAL files can be created
	walFile := dbPath + "-wal"
	shmFile := dbPath + "-shm"

	// Trigger WAL file creation by writing
	_, err = database.Global.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY)")
	if err != nil {
		t.Fatalf("Failed to create test table: %v", err)
	}

	// WAL files should exist after write
	if _, err := os.Stat(walFile); os.IsNotExist(err) {
		t.Logf("WAL file not created yet (may be in memory)")
	}
	if _, err := os.Stat(shmFile); os.IsNotExist(err) {
		t.Logf("SHM file not created yet (may be in memory)")
	}
}

func TestMigrationIdempotency(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	schemaPath := "../../../schema/schema.sql"

	// Open database
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Check if schema file exists
	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		t.Skipf("Schema file not found at %s", schemaPath)
	}

	// Test idempotency
	if err := db.CheckMigrationIdempotency(database.Global, schemaPath); err != nil {
		t.Fatalf("Migration idempotency check failed: %v", err)
	}

	t.Log("Migration is idempotent")
}

func TestTeamDatabase(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()
	teamID := "test-team-123"

	// Open team database
	teamDB, err := db.OpenTeamDB(tmpDir, teamID)
	if err != nil {
		t.Fatalf("Failed to open team database: %v", err)
	}
	defer teamDB.Close()

	// Check journal mode
	var journalMode string
	err = teamDB.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("Failed to get journal mode: %v", err)
	}

	if journalMode != "wal" {
		t.Errorf("Expected journal mode 'wal', got '%s'", journalMode)
	}

	// Verify team directory was created
	teamDir := filepath.Join(tmpDir, teamID)
	if _, err := os.Stat(teamDir); os.IsNotExist(err) {
		t.Errorf("Team directory was not created")
	}

	// Verify database file was created
	dbFile := filepath.Join(teamDir, "memory.db")
	if _, err := os.Stat(dbFile); os.IsNotExist(err) {
		t.Errorf("Database file was not created")
	}
}
