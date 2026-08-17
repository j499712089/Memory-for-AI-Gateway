package db_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
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

// TestDSNPragmasReachEveryPooledConnection guards the ALL-81 root cause: the
// busy_timeout and foreign_keys pragmas must be part of the connection DSN, not
// a single db.Exec. modernc.org/sqlite silently ignores mattn-style parameters
// such as `_busy_timeout`, so without `_pragma=` the extra pooled connections
// open with busy_timeout=0 and concurrent writers fail with SQLITE_BUSY.
func TestDSNPragmasReachEveryPooledConnection(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Hold several distinct pooled connections open at once and verify the DSN
	// pragmas are active on each. With the default unlimited pool this forces
	// the driver to create multiple underlying sqlite connections.
	const connections = 4
	conns := make([]*sql.Conn, 0, connections)
	defer func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()
	for i := 0; i < connections; i++ {
		conn, err := database.Global.Conn(context.Background())
		if err != nil {
			t.Fatalf("acquire pooled connection %d: %v", i, err)
		}
		conns = append(conns, conn)
	}
	var wg sync.WaitGroup
	timeouts := make([]int, connections)
	foreignKeys := make([]int, connections)
	for i, conn := range conns {
		wg.Add(1)
		go func(index int, c *sql.Conn) {
			defer wg.Done()
			if err := c.QueryRowContext(context.Background(), `PRAGMA busy_timeout`).Scan(&timeouts[index]); err != nil {
				t.Errorf("connection %d busy_timeout: %v", index, err)
			}
			if err := c.QueryRowContext(context.Background(), `PRAGMA foreign_keys`).Scan(&foreignKeys[index]); err != nil {
				t.Errorf("connection %d foreign_keys: %v", index, err)
			}
		}(i, conn)
	}
	wg.Wait()
	for i := 0; i < connections; i++ {
		if timeouts[i] != 5000 {
			t.Errorf("connection %d busy_timeout = %d, want 5000 (ALL-81 DSN pragma missing)", i, timeouts[i])
		}
		if foreignKeys[i] != 1 {
			t.Errorf("connection %d foreign_keys = %d, want 1", i, foreignKeys[i])
		}
	}
}
