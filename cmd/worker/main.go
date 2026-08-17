package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"gateway/internal/config"
	"gateway/internal/db"
	"gateway/internal/watchdog"
	"gateway/internal/worker"
)

const (
	workerLeaseDuration = 30 * time.Second
	workerPollInterval  = 500 * time.Millisecond
	workerSweepEvery    = 30 * time.Second
	gitCommitCheckEvery = time.Minute
)

// main runs the standalone Memory Gateway worker. It claims jobs from the
// global jobs table (L1-L4 refinement, wiki build, codegraph, skill review,
// git batch commit), reclaims leases abandoned by crashed workers, and
// schedules the 10-minute vault batch commit.
func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Memory Gateway Worker...")

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	database, err := db.Open(cfg.Database.GlobalDBPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	schemaPath := filepath.Join(filepath.Dir(cfg.Database.GlobalDBPath), "..", "gateway", "schema", "schema.sql")
	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		// Fall back to the canonical checkout path on this machine.
		schemaPath = "F:\\memory_plus\\gateway\\schema\\schema.sql"
	}
	if err := db.Migrate(database.Global, schemaPath); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// The vault is the memory root itself: L1_任务纪要 / L2_知识经验 / L3_团队身份
	// / L4_长期准则 / 02_Wiki知识库 / 04_技能库 live directly under it, while
	// .runtime/ and L0_原始记录 are gitignored and never committed.
	memoryRoot := filepath.Dir(filepath.Dir(cfg.Database.GlobalDBPath))

	// A worker may be started without the gateway (for example after a
	// gateway crash or during an isolated recovery run), so it must perform
	// the same idempotent startup recovery pass. In particular, pending local
	// buffer files are the source of truth for degraded writes and need to be
	// replayed before the worker starts claiming new jobs.
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
			recoveryReport.ReplayedOutbox, recoveryReport.ReplayedBuffers,
			recoveryReport.ReclaimedLeases, recoveryReport.GapCount,
			recoveryReport.IntegrityOK)
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "worker"
	}
	workerID := fmt.Sprintf("%s:%d", hostname, os.Getpid())

	queue := worker.NewQueue(database.Global, workerLeaseDuration)
	processor := worker.NewProcessor(queue)
	if err := worker.RegisterAssetWorkers(processor, worker.AssetWorkerDeps{
		GlobalDB:   database.Global,
		MemoryRoot: memoryRoot,
		VaultPath:  memoryRoot,
	}); err != nil {
		log.Fatalf("Failed to register asset workers: %v", err)
	}

	// buffer_replay: replays events that the gateway durably buffered under
	// `90_运行数据/本地持久化缓冲` when SQLite was busy (ALL-81). The gateway
	// enqueues one of these per degraded write; without a handler the job would
	// sit pending forever and the event would only ever exist as a buffer file.
	processor.Register("buffer_replay", func(ctx context.Context, claim *worker.Claim) error {
		var payload struct {
			BufferID string `json:"buffer_id"`
		}
		if err := json.Unmarshal([]byte(claim.PayloadJSON), &payload); err != nil {
			return fmt.Errorf("buffer_replay payload: %w", err)
		}
		return worker.ReplayBufferByID(ctx, database.Global, memoryRoot, payload.BufferID)
	})

	// missing_response: the watchdog enqueues one of these for every open turn
	// that never received a terminal event. The handler closes the ghost turn
	// with a 'cancelled' terminal (ALL-84) so per-turn terminal audits converge.
	if err := worker.RegisterMissingResponse(processor, database.Global); err != nil {
		log.Fatalf("Failed to register missing_response handler: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Startup lease reclaim: jobs left 'processing' by a crashed worker go
	// straight back to 'pending' so they can be claimed again (ALL-44 test 6).
	reclaimCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	reclaimed, reclaimErr := queue.ReclaimExpired(reclaimCtx)
	cancel()
	if reclaimErr != nil {
		log.Printf("Startup lease reclaim failed (continuing): %v", reclaimErr)
	} else if reclaimed > 0 {
		log.Printf("Startup lease reclaim: %d stale jobs returned to pending", reclaimed)
	}

	// Periodic vault batch commit: checks every minute and enqueues a
	// git_commit job once the 10-minute interval has elapsed (ALL-44 test 7).
	// The first check runs immediately so a fresh vault gets its initial
	// commit without waiting out a full tick.
	go worker.RunGitCommitScheduler(ctx, queue, database.Global, gitCommitCheckEvery, nil)

	// Missing-response detection, expired-lease reclaim and binding repair
	// enqueues, mirroring the gateway's own maintenance loop. Compensation
	// queues the worker has no handler for stay pending instead of being
	// dead-lettered.
	go runWatchdog(ctx, database.Global)

	log.Printf("Worker %s ready: lease_duration=%s poll=%s vault=%s", workerID, workerLeaseDuration, workerPollInterval, memoryRoot)
	if err := processor.Run(ctx, workerID, workerPollInterval); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("Worker processor stopped: %v", err)
	}
	log.Println("Worker stopped cleanly")
}

// runWatchdog periodically scans for missing terminal events and expired
// worker leases, enqueuing compensation jobs for the gateway's maintenance
// loop to observe. It is idempotent and safe to run alongside the gateway.
func runWatchdog(ctx context.Context, database *sql.DB) {
	sweep := watchdog.New(database, workerSweepEvery)
	ticker := time.NewTicker(workerSweepEvery)
	defer ticker.Stop()
	for {
		report, err := sweep.Scan(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("watchdog scan: %v", err)
		} else if report.ReclaimedLeases > 0 || report.MissingResponses > 0 || report.BindingRepairs > 0 {
			log.Printf("watchdog: reclaimed=%d missing=%d binding_repairs=%d buffered=%d",
				report.ReclaimedLeases, report.MissingResponses, report.BindingRepairs, report.BufferedEvents)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
