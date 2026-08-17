package test

import (
	"context"
	"testing"
	"time"

	"gateway/internal/db"
	"gateway/internal/worker"
)

// TestL1RefineCompleteTurnTriggersJob verifies the trigger path: a completed
// turn with facts enqueues an l1_refine job on the Phase 3a queue.
func TestL1RefineCompleteTurnTriggersJob(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	seedTeam(t, database)

	queue := worker.NewQueue(database.Global, 0)
	id, err := worker.EnqueueL1Refine(context.Background(), queue, worker.L1RefinePayload{
		TeamID:         "team-1",
		AgentID:        "agent-1",
		TurnID:         "turn-1",
		SourceEventIDs: []string{"evt-in", "evt-out"},
		CompletedAt:    time.Now().UTC(),
		Facts: []worker.L1Fact{
			{Name: "SQLite WAL", Slug: "sqlite-wal", Summary: "gateway uses WAL for concurrent writes", Confidence: 0.9},
		},
	})
	if err != nil {
		t.Fatalf("enqueue l1_refine: %v", err)
	}
	if id == "" {
		t.Fatal("expected a job id")
	}
	var queueName, status string
	if err := database.Global.QueryRow(`SELECT queue, status FROM jobs WHERE id=?`, id).Scan(&queueName, &status); err != nil {
		t.Fatal(err)
	}
	if queueName != "l1_refine" || status != "pending" {
		t.Fatalf("unexpected job: queue=%s status=%s", queueName, status)
	}
}

// TestL1RefineCreatesCandidateAssets verifies the refiner turns facts into
// candidate L1 assets and deduplicates identical facts.
func TestL1RefineCreatesCandidateAssets(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB := newAssetTeamDB(t, database, root)

	payload := worker.L1RefinePayload{
		TeamID:         "team-1",
		AgentID:        "agent-1",
		SourceEventIDs: []string{"evt-1", "evt-2"},
		Facts: []worker.L1Fact{
			{Name: "WAL Concurrency", Slug: "wal-concurrency", Summary: "sqlite wal enables concurrent readers", Confidence: 0.9},
			{Name: "WAL Concurrency", Slug: "wal-concurrency", Summary: "sqlite wal enables concurrent readers", Confidence: 0.9}, // duplicate
		},
	}
	created, err := worker.RefineL1(context.Background(), teamDB, payload)
	if err != nil {
		t.Fatalf("refine l1: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected 1 new candidate, got %d", created)
	}
	assets, err := db.ListAssets(context.Background(), teamDB, "team-1", db.AssetFilter{AssetType: "l1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets[0].Status != "candidate" || assets[0].Slug != "wal-concurrency" {
		t.Fatalf("unexpected l1 assets: %+v", assets)
	}
	if len(assets[0].SourceEventIDs) != 2 {
		t.Fatalf("expected source event ids to be attached: %+v", assets[0].SourceEventIDs)
	}
}

// TestL1RefineProcessorRunsRegisteredHandler verifies the worker is wired
// into the Phase 3a processor framework end to end.
func TestL1RefineProcessorRunsRegisteredHandler(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	seedTeam(t, database)
	teamDB := newAssetTeamDB(t, database, root)

	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	deps := worker.AssetWorkerDeps{GlobalDB: database.Global, MemoryRoot: root}
	if err := worker.RegisterAssetWorkers(processor, deps); err != nil {
		t.Fatalf("register asset workers: %v", err)
	}
	if _, err := worker.EnqueueL1Refine(context.Background(), queue, worker.L1RefinePayload{
		TeamID: "team-1", AgentID: "agent-1", TurnID: "turn-1",
		SourceEventIDs: []string{"evt-1", "evt-2"},
		Facts:          []worker.L1Fact{{Name: "Facts", Slug: "facts", Summary: "fact summary", Confidence: 0.8}},
	}); err != nil {
		t.Fatal(err)
	}
	processed, err := processor.ProcessOnce(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("process once: %v", err)
	}
	if !processed {
		t.Fatal("expected a job to be processed")
	}
	var done int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue='l1_refine' AND status='done'`).Scan(&done); err != nil {
		t.Fatal(err)
	}
	if done != 1 {
		t.Fatalf("l1_refine job was not completed: done=%d", done)
	}
	var assetCount int
	if err := teamDB.QueryRow(`SELECT COUNT(*) FROM assets WHERE asset_type='l1'`).Scan(&assetCount); err != nil {
		t.Fatal(err)
	}
	if assetCount != 1 {
		t.Fatalf("expected 1 l1 asset, got %d", assetCount)
	}
}
