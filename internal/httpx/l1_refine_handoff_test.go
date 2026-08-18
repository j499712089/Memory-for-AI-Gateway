package httpx

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gateway/internal/db"
	"gateway/internal/turn"
	"gateway/internal/worker"
)

func TestCompleteTerminalQueueFailureRecoversL1Refine(t *testing.T) {
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, ".runtime", "memory-gateway.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database.Global, filepath.Join("..", "..", "schema", "schema.sql")); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	if _, err := database.Global.Exec(`
		INSERT INTO teams (id, name, slug) VALUES ('team-1', 'Team 1', 'team-1');
		INSERT INTO agents (id, name, team_id) VALUES ('agent-1', 'Agent 1', 'team-1');
	`); err != nil {
		t.Fatalf("seed team: %v", err)
	}

	const turnID = "turn-handoff"
	if err := turn.WriteTurnLedger(database.Global, turnID, "req-handoff", "conv-handoff", "", "team-1", 1); err != nil {
		t.Fatalf("write turn ledger: %v", err)
	}
	if _, err := turn.WriteInboundEventWithID(database.Global, "evt-in-handoff", turnID, "req-handoff", "conv-handoff", "", "team-1", "hash-in", ""); err != nil {
		t.Fatalf("write inbound event: %v", err)
	}

	handler := NewGatewayHandler(database.Global, nil, root)
	if _, err := database.Global.Exec(`
		CREATE TRIGGER fail_l1_refine_enqueue
		BEFORE INSERT ON jobs
		WHEN NEW.queue = 'l1_refine'
		BEGIN
			SELECT RAISE(ABORT, 'injected l1 queue failure');
		END;
	`); err != nil {
		t.Fatalf("install queue failure trigger: %v", err)
	}

	eventID, degraded, err := handler.recordTerminalResult(context.Background(), terminalRecord{
		EventID:        "evt-out-handoff",
		TurnID:         turnID,
		RequestID:      "req-handoff",
		ConversationID: "conv-handoff",
		TeamID:         "team-1",
		AgentID:        "agent-1",
		InboundEventID: "evt-in-handoff",
		FactText:       "remember api_key=gw_mcp_test_key_123",
		Status:         turn.StatusComplete,
	})
	if err != nil {
		t.Fatalf("terminal persistence should survive queue failure: %v", err)
	}
	if degraded {
		t.Fatal("terminal should remain in SQLite when only the queue insert fails")
	}
	if eventID == "" {
		t.Fatal("expected terminal event id")
	}

	var finalStatus string
	if err := database.Global.QueryRow(`SELECT final_status FROM turn_ledger WHERE turn_id=?`, turnID).Scan(&finalStatus); err != nil {
		t.Fatalf("read terminal status: %v", err)
	}
	if finalStatus != string(turn.StatusComplete) {
		t.Fatalf("expected complete terminal, got %q", finalStatus)
	}
	var pending int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM local_buffer WHERE turn_id=? AND json_extract(payload_json, '$.event_type')='l1_refine_pending' AND status='pending'`, turnID).Scan(&pending); err != nil {
		t.Fatalf("read pending refine handoff: %v", err)
	}
	if pending != 1 {
		t.Fatalf("expected one durable pending refine handoff, got %d", pending)
	}

	if _, err := database.Global.Exec(`DROP TRIGGER fail_l1_refine_enqueue`); err != nil {
		t.Fatalf("remove queue failure trigger: %v", err)
	}
	replayed, err := worker.ReplayPendingBuffers(context.Background(), database.Global, root)
	if err != nil {
		t.Fatalf("replay pending refine handoff: %v", err)
	}
	if replayed != 1 {
		t.Fatalf("expected one replayed refine handoff, got %d", replayed)
	}

	var jobs int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue='l1_refine'`).Scan(&jobs); err != nil {
		t.Fatalf("count refine jobs: %v", err)
	}
	if jobs != 1 {
		t.Fatalf("expected one recovered refine job, got %d", jobs)
	}
	var payload string
	if err := database.Global.QueryRow(`SELECT payload_json FROM jobs WHERE queue='l1_refine'`).Scan(&payload); err != nil {
		t.Fatalf("read refine payload: %v", err)
	}
	if strings.Contains(payload, "gw_mcp_test_key_123") {
		t.Fatalf("raw credential leaked into recovered refine payload: %s", payload)
	}

	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	if err := worker.RegisterAssetWorkers(processor, worker.AssetWorkerDeps{GlobalDB: database.Global, MemoryRoot: root}); err != nil {
		t.Fatalf("register asset workers: %v", err)
	}
	processed, err := processor.ProcessOnce(context.Background(), "handoff-worker")
	if err != nil || !processed {
		t.Fatalf("process recovered refine job: processed=%v err=%v", processed, err)
	}
	var assets int
	teamDB, err := db.OpenTeamDB(filepath.Join(root, "teams"), "team-1")
	if err != nil {
		t.Fatalf("open team database: %v", err)
	}
	defer teamDB.Close()
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		t.Fatalf("ensure team assets schema: %v", err)
	}
	if err := teamDB.QueryRow(`SELECT COUNT(*) FROM assets WHERE asset_type='l1'`).Scan(&assets); err != nil {
		t.Fatalf("count refined assets: %v", err)
	}
	if assets != 1 {
		t.Fatalf("expected exactly one refined L1 asset, got %d", assets)
	}
	var routes int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue IN ('wiki_build','l2_promote','skill_review')`).Scan(&routes); err != nil {
		t.Fatalf("count derived routes: %v", err)
	}
	if routes != 3 {
		t.Fatalf("expected three derived routes, got %d", routes)
	}
}
