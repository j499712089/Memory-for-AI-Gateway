package test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"gateway/internal/db"
	"gateway/internal/worker"
)

func TestL1RefineConcurrentEnqueueIsExactlyOnce(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	seedTeam(t, database)
	teamDB := newAssetTeamDB(t, database, root)

	queue := worker.NewQueue(database.Global, 0)
	payload := worker.L1RefinePayload{
		TeamID:         "team-1",
		AgentID:        "agent-1",
		TurnID:         "turn-concurrent",
		SourceEventIDs: []string{"evt-in-concurrent", "evt-out-concurrent"},
		Facts:          []worker.L1Fact{{Name: "Concurrent handoff", Slug: "concurrent-handoff", Summary: "one logical turn", Confidence: 0.8}},
	}
	const callers = 12
	start := make(chan struct{})
	ids := make(chan string, callers)
	errs := make(chan error, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer group.Done()
			<-start
			id, err := worker.EnqueueL1Refine(context.Background(), queue, payload)
			if err != nil {
				errs <- err
				return
			}
			ids <- id
		}()
	}
	close(start)
	group.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent enqueue failed: %v", err)
	}
	var jobs int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue='l1_refine' AND partition_key=?`, "l1:team-1:turn-concurrent").Scan(&jobs); err != nil {
		t.Fatalf("count concurrent refine jobs: %v", err)
	}
	if jobs != 1 {
		t.Fatalf("expected exactly one concurrent refine job, got %d", jobs)
	}
	var handoffs int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM l1_refine_handoffs WHERE team_id=? AND turn_id=?`, "team-1", "turn-concurrent").Scan(&handoffs); err != nil {
		t.Fatalf("count concurrent refine handoffs: %v", err)
	}
	if handoffs != 1 {
		t.Fatalf("expected exactly one durable refine handoff, got %d", handoffs)
	}
	var firstID string
	for id := range ids {
		if firstID == "" {
			firstID = id
			continue
		}
		if id != firstID {
			t.Fatalf("concurrent callers returned different job ids: %q and %q", firstID, id)
		}
	}

	processor := worker.NewProcessor(queue)
	if err := worker.RegisterAssetWorkers(processor, worker.AssetWorkerDeps{GlobalDB: database.Global, MemoryRoot: root}); err != nil {
		t.Fatalf("register asset workers: %v", err)
	}
	processed, err := processor.ProcessOnce(context.Background(), "concurrent-worker")
	if err != nil || !processed {
		t.Fatalf("process concurrent refine job: processed=%v err=%v", processed, err)
	}
	var assets int
	if err := teamDB.QueryRow(`SELECT COUNT(*) FROM assets WHERE asset_type='l1'`).Scan(&assets); err != nil {
		t.Fatalf("count concurrent refined assets: %v", err)
	}
	if assets != 1 {
		t.Fatalf("expected exactly one concurrent refined L1 asset, got %d", assets)
	}
	var routes int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue IN ('wiki_build','l2_promote','skill_review')`).Scan(&routes); err != nil {
		t.Fatalf("count concurrent derived routes: %v", err)
	}
	if routes != 3 {
		t.Fatalf("expected three concurrent derived routes, got %d", routes)
	}
}

func TestL1RefineRedactsCredentialsBeforePersistence(t *testing.T) {
	text := worker.RedactSensitiveText(`deploy with sk-live_1234567890 api_key="gw_mcp_test_key_123" and Bearer abcdefghijklmnop`)
	for _, secret := range []string{"sk-live_1234567890", "gw_mcp_test_key_123", "abcdefghijklmnop"} {
		if strings.Contains(text, secret) {
			t.Fatalf("secret leaked after redaction: %q", text)
		}
	}
	if !strings.Contains(text, "[REDACTED_SECRET]") {
		t.Fatalf("expected redaction marker, got %q", text)
	}
}

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

func TestL1RefineRoutesDerivedQueues(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	seedTeam(t, database)
	teamDB := newAssetTeamDB(t, database, root)

	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	if err := worker.RegisterAssetWorkers(processor, worker.AssetWorkerDeps{GlobalDB: database.Global, MemoryRoot: root}); err != nil {
		t.Fatal(err)
	}
	secret := "sk-live_1234567890"
	_, err := worker.EnqueueL1Refine(context.Background(), queue, worker.L1RefinePayload{
		TeamID: "team-1", AgentID: "agent-1", TurnID: "turn-route", SourceEventIDs: []string{"evt-in", "evt-out"},
		Facts: []worker.L1Fact{{Name: "Deploy credential", Slug: "deploy-credential", Summary: "remember " + secret, Confidence: 0.8}},
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := processor.ProcessOnce(context.Background(), "worker-route")
	if err != nil || !processed {
		t.Fatalf("process l1 refine: processed=%v err=%v", processed, err)
	}
	var summary string
	if err := teamDB.QueryRow(`SELECT summary FROM assets WHERE asset_type='l1' AND slug='deploy-credential'`).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(summary, secret) || !strings.Contains(summary, "[REDACTED_SECRET]") {
		t.Fatalf("redacted summary not persisted: %q", summary)
	}

	var queued int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue IN ('wiki_build','l2_promote','skill_review')`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 3 {
		t.Fatalf("expected wiki, promotion and skill jobs, got %d", queued)
	}
	var payloads string
	rows, err := database.Global.Query(`SELECT payload_json FROM jobs`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		payloads += payload
	}
	rows.Close()
	if strings.Contains(payloads, secret) {
		t.Fatalf("secret leaked into derived job payloads: %s", payloads)
	}

	// Drain the derived jobs to verify each registered handler accepts the
	// automatically generated payloads and leaves a candidate skill behind.
	for i := 0; i < 6; i++ {
		processed, err := processor.ProcessOnce(context.Background(), "worker-route")
		if err != nil {
			t.Fatalf("process derived job %d: %v", i, err)
		}
		if !processed {
			break
		}
	}
	var skills int
	if err := teamDB.QueryRow(`SELECT COUNT(*) FROM skills WHERE status='candidate'`).Scan(&skills); err != nil {
		t.Fatal(err)
	}
	if skills != 1 {
		t.Fatalf("expected one candidate skill, got %d", skills)
	}
}

func TestL1RefineRoutesCodeGraphSweepWithoutRegisteredRepo(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	seedTeam(t, database)
	teamDB := newAssetTeamDB(t, database, root)

	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	if err := worker.RegisterAssetWorkers(processor, worker.AssetWorkerDeps{
		GlobalDB:   database.Global,
		MemoryRoot: root,
	}); err != nil {
		t.Fatalf("register asset workers: %v", err)
	}
	if _, err := worker.EnqueueL1Refine(context.Background(), queue, worker.L1RefinePayload{
		TeamID:         "team-1",
		AgentID:        "agent-1",
		TurnID:         "turn-sweep",
		SourceEventIDs: []string{"evt-in", "evt-out"},
		Facts:          []worker.L1Fact{{Name: "No repository", Slug: "no-repository", Summary: "team has no registered repository", Confidence: 0.8}},
	}); err != nil {
		t.Fatal(err)
	}
	if processed, err := processor.ProcessOnce(context.Background(), "worker-sweep"); err != nil || !processed {
		t.Fatalf("process l1 refine: processed=%v err=%v", processed, err)
	}

	var count int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue='codegraph_incremental'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one team-level codegraph job, got %d", count)
	}
	var payloadJSON string
	if err := database.Global.QueryRow(`SELECT payload_json FROM jobs WHERE queue='codegraph_incremental'`).Scan(&payloadJSON); err != nil {
		t.Fatal(err)
	}
	var payload worker.CodeGraphPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TeamID != "team-1" || payload.RepoID != "" {
		t.Fatalf("expected empty-repo team sweep payload, got %+v", payload)
	}

	// Drain the queue until the sweep is consumed. An empty repository list is
	// a valid no-op and must not become a dead letter.
	for i := 0; i < 8; i++ {
		if _, err := processor.ProcessOnce(context.Background(), "worker-sweep"); err != nil {
			t.Fatalf("process downstream job: %v", err)
		}
	}
	var status string
	if err := database.Global.QueryRow(`SELECT status FROM jobs WHERE queue='codegraph_incremental'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "done" {
		t.Fatalf("expected codegraph sweep to complete, got %s", status)
	}
	_ = teamDB
}

func TestReplayBufferedCompleteTriggersL1Refine(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	seedTeam(t, database)
	if _, err := db.WriteLocalBuffer(context.Background(), database.Global, root, db.BufferEventPayload{
		BufferKey: "buffer-in", EventID: "evt-buffer-in", TurnID: "turn-buffer", RequestID: "req-buffer",
		ConversationID: "conv-buffer", TeamID: "team-1", Direction: "inbound", Sequence: 1,
		EventType: "inbound_persisted", Status: "ok", RequestBody: []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.WriteLocalBuffer(context.Background(), database.Global, root, db.BufferEventPayload{
		BufferKey: "buffer-out", EventID: "evt-buffer-out", TurnID: "turn-buffer", RequestID: "req-buffer",
		ConversationID: "conv-buffer", TeamID: "team-1", Direction: "outbound", Sequence: 1,
		EventType: "complete", Status: "ok", FactText: "remember api_key=gw_mcp_test_key_123",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ReplayPendingBuffers(context.Background(), database.Global, root); err != nil {
		t.Fatal(err)
	}
	var jobs int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue='l1_refine'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("expected one l1_refine replay job, got %d", jobs)
	}
	var payload string
	if err := database.Global.QueryRow(`SELECT payload_json FROM jobs WHERE queue='l1_refine'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "gw_mcp_test_key_123") {
		t.Fatalf("secret leaked into replayed refine payload: %s", payload)
	}
}
