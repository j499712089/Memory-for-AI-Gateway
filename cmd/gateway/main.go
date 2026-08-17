package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"gateway/internal/config"
	"gateway/internal/db"
	"gateway/internal/httpx"
	"gateway/internal/secrets"
	"gateway/internal/watchdog"
	"gateway/internal/worker"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Memory Gateway...")

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Config loaded: host=%s port=%d", cfg.Server.Host, cfg.Server.Port)

	// Open database
	database, err := db.Open(cfg.Database.GlobalDBPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	log.Printf("Database opened: %s", cfg.Database.GlobalDBPath)

	// Run migrations
	schemaPath := filepath.Join(filepath.Dir(cfg.Database.GlobalDBPath), "..", "gateway", "schema", "schema.sql")
	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		// Try alternative path
		schemaPath = "F:\\memory_plus\\gateway\\schema\\schema.sql"
	}

	log.Printf("Running migrations from: %s", schemaPath)
	if err := db.Migrate(database.Global, schemaPath); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	log.Println("Migrations completed successfully")

	// Check journal mode
	journalMode, err := database.GetJournalMode()
	if err != nil {
		log.Fatalf("Failed to get journal mode: %v", err)
	}
	log.Printf("Database journal mode: %s", journalMode)

	if journalMode != "wal" {
		log.Println("WARNING: Journal mode is not WAL")
	}

	// Initialize secrets manager
	secretsManager, err := secrets.NewManager(cfg.Secrets.SecretsDir, cfg.Secrets.UseStub)
	if err != nil {
		log.Fatalf("Failed to initialize secrets manager: %v", err)
	}

	log.Println("Secrets manager initialized")

	// Load models
	models, err := config.LoadModels(cfg.Models.ModelsJSONPath)
	if err != nil {
		log.Fatalf("Failed to load models: %v", err)
	}

	log.Printf("Loaded %d models from %s", len(models), cfg.Models.ModelsJSONPath)

	// TODO: Import models into upstream_channels table
	// This will be implemented in the admin handler

	// Determine memory root from database path
	memoryRoot := filepath.Dir(filepath.Dir(cfg.Database.GlobalDBPath))

	// Run the startup recovery pass: replay due outbox rows and local durable
	// buffers, reclaim expired worker leases and remove stale temp files. Every
	// step is idempotent, so an interrupted boot can safely retry.
	recoverCtx, recoverCancel := context.WithTimeout(context.Background(), 60*time.Second)
	recoveryReport, recoverErr := db.Recover(recoverCtx, database.Global, memoryRoot, db.RecoveryOptions{})
	recoverCancel()
	if recoverErr != nil {
		log.Printf("Startup recovery failed (continuing): %v", recoverErr)
	} else {
		log.Printf("Startup recovery: outbox replayed=%d buffers replayed=%d leases reclaimed=%d gaps=%d integrity=%v",
			recoveryReport.ReplayedOutbox, recoveryReport.ReplayedBuffers, recoveryReport.ReclaimedLeases,
			recoveryReport.GapCount, recoveryReport.IntegrityOK)
	}

	// Periodic maintenance: drain outbox retry rows and run the recording
	// watchdog so pending rows never pile up as a zombie retry queue.
	go runMaintenance(database.Global)

	// Setup HTTP router
	router := httpx.SetupRouter(database.Global, secretsManager, memoryRoot)

	// Start server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("Starting server on %s", addr)

	if err := router.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// runMaintenance periodically drains the outbox and runs the recording
// watchdog for the lifetime of the gateway process. Outbox rows whose
// referenced terminal event is already recorded are acknowledged (the failure
// was captured); rows with no terminal event are left for retry until they
// reach dead_letter. Expired worker leases are reclaimed by the watchdog.
func runMaintenance(database *sql.DB) {
	ctx := context.Background()
	sweep := watchdog.New(database, 30*time.Second)
	ack := func(ctx context.Context, entry worker.OutboxEntry) error {
		var exists int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM turn_events WHERE id = ?`, entry.EventID).Scan(&exists); err != nil {
			return fmt.Errorf("check outbox terminal event: %w", err)
		}
		if exists == 0 {
			return fmt.Errorf("referenced terminal event is missing")
		}
		return nil
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if _, err := worker.ReplayOutbox(ctx, database, ack); err != nil {
			log.Printf("outbox drain: %v", err)
		}
		if _, err := sweep.Scan(ctx); err != nil {
			log.Printf("watchdog scan: %v", err)
		}
	}
}
