package test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gateway/internal/worker"
)

// TestWorkerClaimFilterSkipsUnregisteredQueues verifies the worker only claims
// jobs whose queues have a registered handler. Compensation/replay jobs the
// asset worker has no handler for must stay pending instead of being
// dead-lettered by a generic "no handler" failure.
func TestWorkerClaimFilterSkipsUnregisteredQueues(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	if err := worker.RegisterAssetWorkers(processor, worker.AssetWorkerDeps{GlobalDB: database.Global}); err != nil {
		t.Fatalf("register asset workers: %v", err)
	}
	if _, err := queue.Enqueue(context.Background(), worker.Job{
		Queue: "compensation", Priority: 90, Payload: map[string]string{"k": "v"}, PartitionKey: "request:r1",
	}); err != nil {
		t.Fatal(err)
	}
	processed, err := processor.ProcessOnce(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("process once: %v", err)
	}
	if processed {
		t.Fatal("worker must not claim jobs it has no handler for")
	}
	var status string
	if err := database.Global.QueryRow(`SELECT status FROM jobs WHERE queue='compensation'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("compensation job must stay pending, got %s", status)
	}
}

// TestWorkerLeaseRecoveryAfterCrash simulates ALL-44 test 6: a worker claims a
// job and dies without completing or heartbeating. Once the lease expires, the
// job must return to 'pending' so a surviving worker can claim it again.
func TestWorkerLeaseRecoveryAfterCrash(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	queue := worker.NewQueue(database.Global, 20*time.Millisecond)

	if _, err := queue.Enqueue(context.Background(), worker.Job{
		Queue: "git_commit", Priority: 20,
		Payload: worker.GitCommitPayload{Reason: "lease recovery test"}, PartitionKey: "git:vault",
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := queue.Claim(context.Background(), "crashed-worker")
	if err != nil || claim == nil {
		t.Fatalf("claim: err=%v claim=%+v", err, claim)
	}
	// The worker "crashes": no heartbeat, no Complete, no Fail.
	time.Sleep(60 * time.Millisecond)
	reclaimed, err := queue.ReclaimExpired(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed != 1 {
		t.Fatalf("expected 1 expired lease reclaimed, got %d", reclaimed)
	}
	var status string
	if err := database.Global.QueryRow(`SELECT status FROM jobs WHERE id=?`, claim.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("job should be back to pending after lease expiry, got %s", status)
	}
}

// TestWorkerGitSchedulerEnqueuesWhenDue verifies the scheduler enqueues a
// git_commit job for a fresh vault (first commit always due) and never stacks
// a duplicate while one is already pending.
func TestWorkerGitSchedulerEnqueuesWhenDue(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	queue := worker.NewQueue(database.Global, 0)

	enqueued, err := worker.EnqueueScheduledGitCommit(context.Background(), queue, database.Global, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !enqueued {
		t.Fatal("fresh vault should be due for its initial commit")
	}
	var count int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue='git_commit' AND status='pending'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 git_commit job, got %d", count)
	}
	again, err := worker.EnqueueScheduledGitCommit(context.Background(), queue, database.Global, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if again {
		t.Fatal("must not enqueue a second git_commit job while one is pending")
	}
}

// TestWorkerGitSchedulerRespectsInterval verifies the scheduler skips the
// enqueue when the last committed batch is more recent than 10 minutes.
func TestWorkerGitSchedulerRespectsInterval(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	queue := worker.NewQueue(database.Global, 0)
	recent := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	if _, err := database.Global.Exec(
		`INSERT INTO git_batches (id, status, commit_sha, message, created_at, committed_at)
		 VALUES ('batch-recent', 'committed', 'sha', 'memory: x', ?, ?)`,
		recent, recent); err != nil {
		t.Fatal(err)
	}
	enqueued, err := worker.EnqueueScheduledGitCommit(context.Background(), queue, database.Global, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if enqueued {
		t.Fatal("commit must not be due before the 10-minute interval elapses")
	}
}

// TestWorkerGitSchedulerLoopEnqueuesInitialCommit verifies RunGitCommitScheduler
// performs an immediate first check instead of waiting for the first tick.
func TestWorkerGitSchedulerLoopEnqueuesInitialCommit(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	queue := worker.NewQueue(database.Global, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A huge interval guarantees only the immediate first check can fire.
	go worker.RunGitCommitScheduler(ctx, queue, database.Global, time.Hour, nil)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue='git_commit' AND status='pending'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("scheduler did not enqueue the initial commit on its first check")
}

// TestWorkerGitCommitRetryReusesBatchRow verifies a retried git_commit job
// (same batch id) does not trip the git_batches primary key: the batch row
// created by the first attempt is reused idempotently.
func TestWorkerGitCommitRetryReusesBatchRow(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	vault := initGitRepo(t, vaultGitignore)
	if err := os.MkdirAll(filepath.Join(vault, "L1_任务纪要"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "L1_任务纪要", "fact.md"), []byte("# fact"), 0644); err != nil {
		t.Fatal(err)
	}

	payload := worker.GitCommitPayload{BatchID: "batch-retry-same", Reason: "retry test"}
	if _, err := worker.RunGitBatchCommit(context.Background(), database.Global, vault, payload); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	// Second attempt simulates the worker retrying the same job.
	if _, err := worker.RunGitBatchCommit(context.Background(), database.Global, vault, payload); err != nil {
		t.Fatalf("retry must reuse the batch row without a constraint error: %v", err)
	}
	var rows int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM git_batches WHERE id=?`, payload.BatchID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("expected exactly one git_batches row, got %d", rows)
	}
	var status string
	if err := database.Global.QueryRow(`SELECT status FROM git_batches WHERE id=?`, payload.BatchID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "committed" {
		t.Fatalf("expected committed status after retry, got %s", status)
	}
}

// TestWorkerGitCommitUntrackedFilesIsNoOp verifies a vault with only untracked
// files (nothing in eligible asset dirs) is a clean no-op, not a git commit
// failure — a stray file outside the allowed dirs must never break batching.
func TestWorkerGitCommitUntrackedFilesIsNoOp(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	vault := initGitRepo(t, vaultGitignore)
	// Stray untracked file that lives outside the allowed asset dirs.
	if err := os.WriteFile(filepath.Join(vault, "stray.log"), []byte("noise"), 0644); err != nil {
		t.Fatal(err)
	}
	sha, err := worker.RunGitBatchCommit(context.Background(), database.Global, vault, worker.GitCommitPayload{BatchID: "batch-stray", Reason: "idle"})
	if err != nil {
		t.Fatalf("untracked-only vault must not error: %v", err)
	}
	if sha != "" {
		t.Fatalf("expected no commit, got sha %s", sha)
	}
	var status string
	if err := database.Global.QueryRow(`SELECT status FROM git_batches WHERE id='batch-stray'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "committed" {
		t.Fatalf("expected committed no-op batch, got %s", status)
	}
}

// TestWorkerPromotionTriggersGitCommit verifies a successful L2 promotion with
// a vault configured enqueues a git_commit job so the new asset markdown is
// persisted to git (ALL-44 test 7, "资产晋升后批量 Git 提交").
func TestWorkerPromotionTriggersGitCommit(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	seedTeam(t, database)
	teamDB := newAssetTeamDB(t, database, root)
	vault := initGitRepo(t, vaultGitignore)

	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	if err := worker.RegisterAssetWorkers(processor, worker.AssetWorkerDeps{
		GlobalDB: database.Global, MemoryRoot: root, VaultPath: vault,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.EnqueuePromotion(context.Background(), queue, worker.PromotePayload{
		TeamID: "team-1", AgentID: "agent-1", Layer: "l2",
		Slug: "retro", Name: "Retro", Summary: "retro findings", Confidence: 0.8,
		SourceEventIDs: []string{"evt-1", "evt-2"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessOnce(context.Background(), "worker-1"); err != nil {
		t.Fatalf("process promotion: %v", err)
	}
	var count int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue='git_commit' AND status='pending'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("promotion should enqueue a git_commit job, got %d", count)
	}
	_ = teamDB
}

// TestWorkerPromotionWithoutVaultSkipsGitCommit verifies a DB-only promotion
// (no vault path) does not enqueue a git_commit job that would then fail for
// lack of a vault.
func TestWorkerPromotionWithoutVaultSkipsGitCommit(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	seedTeam(t, database)
	_ = newAssetTeamDB(t, database, root)

	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	if err := worker.RegisterAssetWorkers(processor, worker.AssetWorkerDeps{GlobalDB: database.Global}); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.EnqueuePromotion(context.Background(), queue, worker.PromotePayload{
		TeamID: "team-1", AgentID: "agent-1", Layer: "l2",
		Slug: "db-only", Name: "DB Only", Summary: "db only", Confidence: 0.8,
		SourceEventIDs: []string{"evt-1", "evt-2"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessOnce(context.Background(), "worker-1"); err != nil {
		t.Fatalf("process promotion: %v", err)
	}
	var count int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue='git_commit'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("DB-only promotion must not enqueue git_commit, got %d", count)
	}
}
