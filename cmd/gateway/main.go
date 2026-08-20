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
	"gateway/internal/embedding"
	"gateway/internal/httpx"
	"gateway/internal/paths"
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
	// step is idempotent, so an interrupted boot can safely retry. The buffer
	// handler is wired so pending degraded events actually re-enter SQLite
	// instead of being marked 'replayed' and silently dropped (ALL-81).
	recoverCtx, recoverCancel := context.WithTimeout(context.Background(), 60*time.Second)
	recoveryReport, recoverErr := db.Recover(recoverCtx, database.Global, memoryRoot, db.RecoveryOptions{
		BufferHandler: func(ctx context.Context, record db.BufferRecord) error {
			return worker.ReplayBufferEvent(ctx, database.Global, memoryRoot, record)
		},
	})
	recoverCancel()
	if recoverErr != nil {
		log.Printf("Startup recovery failed (continuing): %v", recoverErr)
	} else {
		log.Printf("Startup recovery: outbox replayed=%d buffers replayed=%d leases reclaimed=%d gaps=%d integrity=%v",
			recoveryReport.ReplayedOutbox, recoveryReport.ReplayedBuffers, recoveryReport.ReclaimedLeases,
			recoveryReport.GapCount, recoveryReport.IntegrityOK)
	}

	// Periodic maintenance: drain outbox retry rows, replay pending local
	// buffers, and run the recording watchdog so pending rows never pile up as
	// a zombie retry queue.
	go runMaintenance(database.Global, memoryRoot)
	startEmbeddingWorker(database.Global, memoryRoot)

	// Setup HTTP router
	router := httpx.SetupRouter(database.Global, secretsManager, memoryRoot)

	// Start server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("Starting server on %s", addr)

	if err := router.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func startEmbeddingWorker(database *sql.DB, memoryRoot string) {
	modelPath := os.Getenv("EMBEDDING_MODEL_PATH")
	tokenizerPath := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if modelPath == "" {
		modelPath = `C:\f\memory_plus\models\all-MiniLM-L6-v2\model.onnx`
	}
	if tokenizerPath == "" {
		tokenizerPath = `C:\f\memory_plus\models\all-MiniLM-L6-v2\tokenizer.json`
	}
	service, err := embedding.NewService(modelPath, tokenizerPath)
	if err != nil {
		log.Printf("Embedding worker disabled: %v", err)
		return
	}
	queue := worker.NewQueue(database, 30*time.Second)
	processor := worker.NewProcessor(queue)
	embedding.RegisterWorker(processor, service, paths.TeamsDir(memoryRoot))
	go func() {
		log.Printf("Embedding worker started: model=%s dimensions=%d", modelPath, embedding.Dimensions)
		if err := processor.Run(context.Background(), "gateway-embedding", 100*time.Millisecond); err != nil {
			log.Printf("Embedding worker stopped: %v", err)
		}
	}()
}

// runMaintenance periodically drains the outbox, replays pending local durable
// buffers, consumes watchdog-enqueued compensation jobs, and runs the recording
// watchdog for the lifetime of the gateway process. Outbox rows whose
// referenced terminal event is already recorded are acknowledged (the failure
// was captured); rows with no terminal event are left for retry until they
// reach dead_letter. Expired worker leases are reclaimed by the watchdog.
// Buffer replay keeps the degraded write path honest: events that fell back to
// `90_运行数据/本地持久化缓冲` re-enter SQLite without needing a separate worker
// process (ALL-81). The missing_response handler closes ghost turns (open
// ledger rows that never received a terminal) with a cancelled terminal
// (ALL-84). The first pass runs immediately so leftover ghosts from a previous
// run converge without waiting out a full tick.
func runMaintenance(database *sql.DB, memoryRoot string) {
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
	// Consume watchdog-enqueued compensation jobs the gateway can handle in
	// process. Only registered queues are claimed, so foreign queues (asset
	// refine, wiki build, ...) stay pending for the dedicated worker.
	queue := worker.NewQueue(database, 30*time.Second)
	compensation := worker.NewProcessor(queue)
	if err := worker.RegisterMissingResponse(compensation, database); err != nil {
		log.Printf("register missing_response handler: %v", err)
	}

	pass := func() {
		if _, err := worker.ReplayOutbox(ctx, database, ack); err != nil {
			log.Printf("outbox drain: %v", err)
		}
		if _, err := worker.ReplayPendingBuffers(ctx, database, memoryRoot); err != nil {
			log.Printf("buffer replay: %v", err)
		}
		if _, err := worker.RecoverPendingL1RefineHandoffs(ctx, queue); err != nil {
			log.Printf("l1 refine handoff recovery: %v", err)
		}
		// Scan first so the missing_response jobs it enqueues for ghost turns
		// are claimed by the compensation processor in the same pass.
		if _, err := sweep.Scan(ctx); err != nil {
			log.Printf("watchdog scan: %v", err)
		}
		if _, err := compensation.ProcessOnce(ctx, "gateway-maintenance"); err != nil {
			log.Printf("compensation job: %v", err)
		}
	}
	pass()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		pass()
	}
}
