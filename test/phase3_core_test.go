package test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gateway/internal/acl"
	"gateway/internal/adapter"
	"gateway/internal/binding"
	"gateway/internal/db"
	"gateway/internal/hashutil"
	"gateway/internal/httpx"
	"gateway/internal/inject"
	"gateway/internal/obsidian"
	"gateway/internal/retrieval"
	"gateway/internal/secrets"
	"gateway/internal/watchdog"
	"gateway/internal/worker"

	"github.com/gin-gonic/gin"
)

func phase3Database(t *testing.T) (*db.DB, string) {
	t.Helper()
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, ".runtime", "memory-gateway.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.Migrate(database.Global, "../schema/schema.sql"); err != nil {
		database.Close()
		t.Fatalf("migrate database: %v", err)
	}
	return database, root
}

func TestBindingResolverCreatesAndReusesBinding(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	_, err := database.Global.Exec(`
		INSERT INTO teams (id, name, slug) VALUES ('team-1', 'Team 1', 'team-1');
		INSERT INTO api_keys (id, team_id, key_hash, key_ref) VALUES ('key-1', 'team-1', 'hash', 'ref');
		INSERT INTO upstream_channels (id, team_id, name, protocol, base_url, model, api_key_ref)
		VALUES ('channel-1', 'team-1', 'default', 'chat_completions', 'http://upstream', 'model', 'ref');
	`)
	if err != nil {
		t.Fatalf("seed binding data: %v", err)
	}

	resolver := binding.NewResolver(database.Global)
	first, err := resolver.Resolve(context.Background(), binding.ResolveRequest{
		ConversationID:    "conversation-1",
		APIKeyID:          "key-1",
		UpstreamChannelID: "channel-1",
		BindingSource:     "service",
	})
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	if first.BindingVersion != 1 || first.State != binding.StateBound || first.TeamID != "team-1" {
		t.Fatalf("unexpected first binding: %+v", first)
	}

	second, err := resolver.Resolve(context.Background(), binding.ResolveRequest{
		ConversationID: "conversation-1",
		APIKeyID:       "key-1",
	})
	if err != nil {
		t.Fatalf("reuse binding: %v", err)
	}
	if second.ID != first.ID || second.BindingVersion != 1 {
		t.Fatalf("binding was recreated instead of reused: first=%+v second=%+v", first, second)
	}

	var sessions, bindings int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM sessions WHERE conversation_id = 'conversation-1'`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM session_bindings WHERE conversation_id = 'conversation-1'`).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || bindings != 1 {
		t.Fatalf("expected one session and one binding, got sessions=%d bindings=%d", sessions, bindings)
	}
}

func TestBindingResolverQuarantinesMissingBindingAndQueuesRepair(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	_, err := database.Global.Exec(`INSERT INTO teams (id, name, slug) VALUES ('team-1', 'Team 1', 'team-1')`)
	if err != nil {
		t.Fatal(err)
	}
	resolver := binding.NewResolver(database.Global)
	resolved, err := resolver.Resolve(context.Background(), binding.ResolveRequest{
		ConversationID: "conversation-missing",
		TeamID:         "team-1",
	})
	if err != nil {
		t.Fatalf("resolve missing binding: %v", err)
	}
	if resolved.State != binding.StateMissing {
		t.Fatalf("expected missing binding state, got %+v", resolved)
	}
	var repairs int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue = 'binding_fix' AND status = 'pending'`).Scan(&repairs); err != nil {
		t.Fatal(err)
	}
	if repairs != 1 {
		t.Fatalf("expected one binding repair job, got %d", repairs)
	}
	second, err := resolver.Resolve(context.Background(), binding.ResolveRequest{ConversationID: "conversation-missing", TeamID: "team-1"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != resolved.ID || second.BindingVersion != resolved.BindingVersion {
		t.Fatalf("missing binding should be refreshed, not duplicated: first=%+v second=%+v", resolved, second)
	}
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue='binding_fix' AND status='pending'`).Scan(&repairs); err != nil {
		t.Fatal(err)
	}
	if repairs != 1 {
		t.Fatalf("missing binding refresh duplicated repair job: %d", repairs)
	}
}

func TestRetrievalFiltersACLAndTokenBudget(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB, err := db.OpenTeamDB(filepath.Join(root, "90_运行数据", "teams"), "team-1")
	if err != nil {
		t.Fatal(err)
	}
	defer teamDB.Close()
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		t.Fatalf("ensure asset schema: %v", err)
	}
	_, err = teamDB.Exec(`
		INSERT INTO assets (id, team_id, identity_card_id, asset_type, name, slug, summary, source_event_ids, confidence, status, visibility)
		VALUES
		 ('asset-team', 'team-1', NULL, 'l2', 'Team memory', 'team-memory', 'sqlite cache concurrency', '["event-team"]', 0.9, 'approved', 'team'),
		 ('asset-private', 'team-1', 'card-1', 'l3', 'Private memory', 'private-memory', 'sqlite cache private', '["event-private"]', 0.9, 'approved', 'private'),
		 ('asset-other', 'team-2', NULL, 'l2', 'Other team', 'other-team', 'sqlite cache secret', '["event-other"]', 0.9, 'approved', 'team'),
		 ('asset-long', 'team-1', NULL, 'l1', 'Long memory', 'long-memory', ?, '["event-long"]', 0.9, 'approved', 'team')
	`, strings.Repeat("cache ", 200))
	if err != nil {
		t.Fatalf("seed assets: %v", err)
	}
	if _, err := teamDB.Exec(`INSERT INTO acl_entries (id, asset_id, grantee_type, grantee_id, permission) VALUES ('acl-1', 'asset-private', 'agent', 'agent-1', 'read')`); err != nil {
		t.Fatal(err)
	}

	pipeline := retrieval.NewPipeline(teamDB)
	result, err := pipeline.Search(context.Background(), retrieval.Request{
		TeamID:         "team-1",
		AgentID:        "agent-1",
		IdentityCardID: "card-1",
		Query:          "cache",
		TokenBudget:    30,
		Limit:          20,
	})
	if err != nil {
		t.Fatalf("retrieve memories: %v", err)
	}
	if !result.Truncated || result.UsedTokens > 30 {
		t.Fatalf("expected token truncation, got %+v", result)
	}
	for _, item := range result.Items {
		if item.TeamID != "team-1" || item.ID == "asset-other" {
			t.Fatalf("ACL/team isolation failed: %+v", item)
		}
	}
	if len(result.Items) == 0 {
		t.Fatal("expected at least one visible result")
	}
	if ok, err := acl.CanRead(context.Background(), teamDB, acl.Resource{ID: "asset-private", TeamID: "team-1", IdentityCardID: "card-1", Visibility: "private"}, acl.Subject{TeamID: "team-1", AgentID: "agent-2", IdentityCardID: "card-2"}); err != nil || ok {
		t.Fatalf("private ACL unexpectedly allowed: ok=%v err=%v", ok, err)
	}
	if ok, err := acl.CanRead(context.Background(), teamDB, acl.Resource{ID: "asset-agent", TeamID: "team-1", IdentityCardID: "card-1", Visibility: "agent"}, acl.Subject{TeamID: "team-1", AgentID: "agent-2", IdentityCardID: "card-1"}); err != nil || !ok {
		t.Fatalf("agent ACL should match identity card: ok=%v err=%v", ok, err)
	}
}

func TestInjectionPackageAndSnapshotAreAuditable(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB, err := db.OpenTeamDB(filepath.Join(root, "90_运行数据", "teams"), "team-1")
	if err != nil {
		t.Fatal(err)
	}
	defer teamDB.Close()
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		t.Fatal(err)
	}
	if _, err := teamDB.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, summary, source_event_ids, confidence, status, visibility) VALUES ('asset-1', 'team-1', 'l4', 'Rule', 'rule', 'always preserve sqlite writes', '["event-1"]', 1, 'approved', 'team')`); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "00_系统"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "00_系统", "路径注入清单.md"), []byte("# approved paths\n- L4_长期准则/"), 0644); err != nil {
		t.Fatal(err)
	}
	pkg, text, err := inject.Build(context.Background(), inject.Request{
		GlobalDB:       database.Global,
		TeamDB:         teamDB,
		MemoryRoot:     root,
		TeamID:         "team-1",
		AgentID:        "agent-1",
		IdentityCardID: "",
		Query:          "sqlite",
		TokenBudget:    100,
	})
	if err != nil {
		t.Fatalf("build injection package: %v", err)
	}
	if !strings.Contains(text, "event-1") || !strings.Contains(text, "manifest_version") || !strings.Contains(text, "approved paths") {
		t.Fatalf("injection text is not auditable: %q", text)
	}
	if len(pkg.SourceEventIDs) != 1 || pkg.ManifestVersion == "" {
		t.Fatalf("unexpected injection package: %+v", pkg)
	}
	if err := inject.RecordSnapshot(context.Background(), database.Global, inject.Snapshot{
		RequestID: "request-1", TurnID: "turn-1", Package: pkg, Text: text,
	}); err != nil {
		t.Fatalf("record snapshot: %v", err)
	}
	var sourceJSON string
	if err := database.Global.QueryRow(`SELECT source_ids_json FROM injection_snapshots WHERE request_id = 'request-1'`).Scan(&sourceJSON); err != nil {
		t.Fatal(err)
	}
	var sourceIDs []string
	if err := json.Unmarshal([]byte(sourceJSON), &sourceIDs); err != nil || len(sourceIDs) != 1 || sourceIDs[0] != "event-1" {
		t.Fatalf("snapshot source IDs missing: %s", sourceJSON)
	}
}

func TestWorkerQueuePriorityPartitionLeaseAndDeadLetter(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	queue := worker.NewQueue(database.Global, 10*time.Millisecond)
	low, err := queue.Enqueue(context.Background(), worker.Job{Queue: "git_commit", Priority: 20, Payload: map[string]string{"name": "low"}, PartitionKey: "asset-1"})
	if err != nil {
		t.Fatal(err)
	}
	high, err := queue.Enqueue(context.Background(), worker.Job{Queue: "compensation", Priority: 90, Payload: map[string]string{"name": "high"}, PartitionKey: "asset-2", MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := queue.Claim(context.Background(), "worker-1")
	if err != nil || claim == nil || claim.ID != high {
		t.Fatalf("priority claim failed: claim=%+v err=%v", claim, err)
	}
	blocked, err := queue.Claim(context.Background(), "worker-2")
	if err != nil {
		t.Fatal(err)
	}
	if blocked == nil || blocked.ID != low {
		t.Fatalf("different partition should be claimable: %+v", blocked)
	}
	if err := queue.Fail(context.Background(), claim, "transient"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`UPDATE jobs SET next_retry_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339Nano), high); err != nil {
		t.Fatal(err)
	}
	claim, err = queue.Claim(context.Background(), "worker-3")
	if err != nil || claim == nil || claim.ID != high {
		t.Fatalf("retry claim failed: claim=%+v err=%v", claim, err)
	}
	if err := queue.Fail(context.Background(), claim, "permanent"); err != nil {
		t.Fatal(err)
	}
	var dead int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM dead_letters WHERE job_id = ?`, high).Scan(&dead); err != nil {
		t.Fatal(err)
	}
	if dead != 1 {
		t.Fatalf("expected dead letter, got %d", dead)
	}

	if err := queue.Heartbeat(context.Background(), blocked); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	reclaimed, err := queue.ReclaimExpired(context.Background())
	if err != nil || reclaimed != 1 {
		t.Fatalf("expected one expired lease reclaimed, count=%d err=%v", reclaimed, err)
	}
}

func TestObsidianClientHealthAndWrite(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPut {
			gotPath = r.URL.EscapedPath()
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			gotBody = string(body)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client := obsidian.NewClient(server.URL, "token")
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("obsidian health: %v", err)
	}
	if err := client.PutMarkdown(context.Background(), "L1_任务纪要/test.md", []byte("hello")); err != nil {
		t.Fatalf("obsidian write: %v", err)
	}
	if gotPath != "/vault/L1_%E4%BB%BB%E5%8A%A1%E7%BA%AA%E8%A6%81/test.md" || gotBody != "hello" {
		t.Fatalf("unexpected obsidian write: path=%q body=%q", gotPath, gotBody)
	}
}

func TestRecordingHealthEndpointListsFilteredRows(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	if _, err := database.Global.Exec(`INSERT INTO teams (id, name, slug) VALUES ('team-1', 'Team 1', 'team-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`INSERT INTO api_keys (id, team_id, key_hash, key_ref) VALUES ('api-key', 'team-1', ?, 'ref')`, hashutil.SHA256("gateway-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`INSERT INTO sessions (id, team_id, kind, conversation_id) VALUES ('session-1', 'team-1', 'main', 'conversation-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`INSERT INTO recording_health (id, request_id, conversation_id, session_id, terminal_status, content_hash, compensation_status) VALUES ('h-1', 'request-1', 'conversation-1', 'session-1', 'complete', 'sha256:x', 'ok')`); err != nil {
		t.Fatal(err)
	}
	manager, err := secrets.NewManager(filepath.Join(root, "secrets"), true)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := httpx.SetupRouter(database.Global, manager, root)
	req := httptest.NewRequest(http.MethodGet, "/api/recording-health?limit=10&terminal_status=complete", nil)
	req.Header.Set("Authorization", "Bearer gateway-key")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("recording health status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0]["request_id"] != "request-1" {
		t.Fatalf("unexpected health rows: %+v", body.Items)
	}
}

func TestRecordingHealthProjectsCompletedLedgerRows(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	if _, err := database.Global.Exec(`INSERT INTO teams (id, name, slug) VALUES ('team-1', 'Team 1', 'team-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`INSERT INTO api_keys (id, team_id, key_hash, key_ref) VALUES ('api-key', 'team-1', ?, 'ref')`, hashutil.SHA256("gateway-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`INSERT INTO sessions (id, team_id, kind, conversation_id, binding_version) VALUES ('session-1', 'team-1', 'main', 'conversation-1', 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`INSERT INTO turn_ledger (turn_id, request_id, conversation_id, session_id, team_id, turn_seq, binding_version, inbound_event_id, final_event_id, final_status, recording_state, content_hash, l0_request_path, created_at, completed_at) VALUES ('turn-1', 'request-projected', 'conversation-1', 'session-1', 'team-1', 1, 2, 'event-in', 'event-out', 'complete', 'complete', 'sha256:body', 'L0_原始记录/request.jsonl', ?, ?)`, time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	manager, err := secrets.NewManager(filepath.Join(root, "secrets"), true)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := httpx.SetupRouter(database.Global, manager, root)
	req := httptest.NewRequest(http.MethodGet, "/api/recording-health?limit=10", nil)
	req.Header.Set("Authorization", "Bearer gateway-key")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0]["request_id"] != "request-projected" {
		t.Fatalf("ledger row was not projected: %+v", body.Items)
	}
}

func TestWorkerBackoffCapsAtThirtySeconds(t *testing.T) {
	if got := worker.RetryDelay(6); got != 30*time.Second {
		t.Fatalf("retry delay should cap at 30s, got %s", got)
	}
}

func TestRecoveryReclaimsLeasesAndReplaysDurableBuffers(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	old := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	if _, err := database.Global.Exec(`INSERT INTO jobs (id, queue, priority, payload_json, status, lease_token, lease_until, worker_id, partition_key, created_at) VALUES ('job-expired', 'git_commit', 20, '{}', 'processing', 'lease', ?, 'worker', 'asset-1', ?)`, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`INSERT INTO turn_events (id, request_id, turn_id, conversation_id, direction, sequence, event_type, status, content_hash) VALUES ('event-outbox', 'request-outbox', 'turn-outbox', 'conversation-outbox', 'outbound', 1, 'error', 'error', 'sha256:x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`INSERT INTO outbox (id, event_id, request_id, payload_json, status, retry_count, max_retries, next_retry_at) VALUES ('outbox-1', 'event-outbox', 'request-outbox', '{}', 'pending', 0, 3, ?)`, old); err != nil {
		t.Fatal(err)
	}
	// A realistic buffered inbound event: file + local_buffer ledger row.
	record, err := db.WriteLocalBuffer(context.Background(), database.Global, root, db.BufferEventPayload{
		BufferKey:      "buffer-1",
		EventID:        "event-buffer",
		TurnID:         "turn-buffer",
		RequestID:      "request-buffer",
		ConversationID: "conversation-buffer",
		Direction:      "inbound",
		Sequence:       1,
		EventType:      "inbound_persisted",
		Status:         "ok",
		RequestBody:    []byte(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Recovery without a BufferHandler must NOT mark the pending buffer as
	// replayed: that would silently drop the event (ALL-81). It stays pending
	// for a later pass that has a replay implementation.
	report, err := db.Recover(context.Background(), database.Global, root, db.RecoveryOptions{})
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.ReclaimedLeases != 1 || report.ReplayedOutbox != 1 || report.ReplayedBuffers != 0 || !report.IntegrityOK {
		t.Fatalf("unexpected recovery report: %+v", report)
	}
	var jobStatus, outboxStatus, bufferStatus string
	_ = database.Global.QueryRow(`SELECT status FROM jobs WHERE id='job-expired'`).Scan(&jobStatus)
	_ = database.Global.QueryRow(`SELECT status FROM outbox WHERE id='outbox-1'`).Scan(&outboxStatus)
	_ = database.Global.QueryRow(`SELECT status FROM local_buffer WHERE id=?`, record.ID).Scan(&bufferStatus)
	if jobStatus != "pending" || outboxStatus != "done" || bufferStatus != "pending" {
		t.Fatalf("recovery states not persisted: job=%s outbox=%s buffer=%s", jobStatus, outboxStatus, bufferStatus)
	}

	// A recovery pass with a real BufferHandler replays the pending row into
	// SQLite and marks it replayed.
	replayed := 0
	report, err = db.Recover(context.Background(), database.Global, root, db.RecoveryOptions{
		BufferHandler: func(ctx context.Context, entry db.BufferRecord) error {
			replayed++
			return worker.ReplayBufferEvent(ctx, database.Global, root, entry)
		},
	})
	if err != nil {
		t.Fatalf("recover with handler: %v", err)
	}
	if report.ReplayedBuffers != 1 || replayed != 1 {
		t.Fatalf("expected one buffered event replayed, report=%+v handler_calls=%d", report, replayed)
	}
	_ = database.Global.QueryRow(`SELECT status FROM local_buffer WHERE id=?`, record.ID).Scan(&bufferStatus)
	if bufferStatus != "replayed" {
		t.Fatalf("buffered row not marked replayed, got %q", bufferStatus)
	}
	var inboundEvents int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM turn_events WHERE id='event-buffer' AND event_type='inbound_persisted'`).Scan(&inboundEvents); err != nil {
		t.Fatal(err)
	}
	if inboundEvents != 1 {
		t.Fatalf("buffered inbound event was not replayed into turn_events, count=%d", inboundEvents)
	}
}

func TestWatchdogEnqueuesMissingResponseAndReclaimsLease(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	old := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	if _, err := database.Global.Exec(`INSERT INTO turn_ledger (turn_id, request_id, conversation_id, turn_seq, recording_state, created_at) VALUES ('turn-missing', 'request-missing', 'conversation-missing', 1, 'open', ?)`, old); err != nil {
		t.Fatal(err)
	}
	watch := watchdog.New(database.Global, 10*time.Second)
	report, err := watch.Scan(context.Background())
	if err != nil {
		t.Fatalf("watchdog scan: %v", err)
	}
	if report.MissingResponses != 1 {
		t.Fatalf("expected missing response alert, got %+v", report)
	}
	var jobs int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue='missing_response' AND status='pending'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("expected one missing response job, got %d", jobs)
	}
}

func TestWikiFTSResultsCarryTeamAndACLMetadata(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB, err := db.OpenTeamDB(filepath.Join(root, "90_运行数据", "teams"), "team-1")
	if err != nil {
		t.Fatal(err)
	}
	defer teamDB.Close()
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		t.Fatal(err)
	}
	_, err = teamDB.Exec(`INSERT INTO assets (id, team_id, identity_card_id, asset_type, name, slug, summary, status, visibility, source_event_ids) VALUES
		('wiki-asset', 'team-1', NULL, 'wiki', 'Allowed Wiki', 'allowed-wiki', 'isolated retrieval phrase', 'approved', 'team', '["wiki-event"]'),
		('private-wiki-asset', 'team-1', 'card-2', 'wiki', 'Private Wiki', 'private-wiki', 'isolated retrieval phrase', 'approved', 'private', '["private-event"]')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertWikiPage(context.Background(), teamDB, db.WikiPage{ID: "wiki-page", AssetID: "wiki-asset", Title: "Allowed Wiki", Slug: "allowed-wiki", ContentMD: "isolated retrieval phrase"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertWikiPage(context.Background(), teamDB, db.WikiPage{ID: "private-page", AssetID: "private-wiki-asset", Title: "Private Wiki", Slug: "private-wiki", ContentMD: "isolated retrieval phrase"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RebuildWikiFTS(context.Background(), teamDB); err != nil {
		t.Fatal(err)
	}
	result, err := retrieval.NewPipeline(teamDB).Search(context.Background(), retrieval.Request{TeamID: "team-1", IdentityCardID: "card-1", Query: "isolated retrieval phrase", Limit: 20, TokenBudget: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var allowed, private bool
	for _, item := range result.Items {
		if item.ID == "wiki-page" && item.TeamID == "team-1" && item.Visibility == "team" {
			allowed = true
		}
		if item.ID == "private-page" {
			private = true
		}
	}
	if !allowed || private {
		t.Fatalf("wiki ACL metadata/filtering failed: %+v", result.Items)
	}
}

func TestInjectionSnapshotIsIdempotentAndRecordsBudget(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	pkg := adapter.InjectionPackage{ManifestVersion: "phase3-v1", SourceEventIDs: []string{"event-1"}}
	snapshot := inject.Snapshot{RequestID: "request-snapshot", TurnID: "turn-snapshot", Package: pkg, Text: "four words", TokenBudget: 42}
	if err := inject.RecordSnapshot(context.Background(), database.Global, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := inject.RecordSnapshot(context.Background(), database.Global, snapshot); err != nil {
		t.Fatal(err)
	}
	var count, budget int
	if err := database.Global.QueryRow(`SELECT COUNT(*), MAX(token_budget) FROM injection_snapshots WHERE request_id='request-snapshot'`).Scan(&count, &budget); err != nil {
		t.Fatal(err)
	}
	if count != 1 || budget != 42 {
		t.Fatalf("snapshot was not idempotent: count=%d budget=%d", count, budget)
	}
}

func TestWatchdogDetectsSequenceGapsAndBindingRepairs(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	old := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	if _, err := database.Global.Exec(`INSERT INTO sessions (id, conversation_id, kind) VALUES ('session-gap', 'conversation-gap', 'main')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`INSERT INTO turn_ledger (turn_id, request_id, conversation_id, turn_seq, recording_state, created_at) VALUES ('turn-gap', 'request-gap', 'conversation-gap', 2, 'complete', ?)`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`INSERT INTO session_bindings (id, session_id, conversation_id, binding_state, binding_source, last_resolved_at) VALUES ('binding-gap', 'session-gap', 'conversation-gap', 'binding_missing', 'service', ?)`, old); err != nil {
		t.Fatal(err)
	}
	report, err := watchdog.New(database.Global, 10*time.Second).Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.SequenceGaps != 1 || report.BindingRepairs != 1 {
		t.Fatalf("watchdog did not report gap/repair: %+v", report)
	}
}

func TestRecoveryMarksUnsafeEventPathsInconsistent(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	if _, err := database.Global.Exec(`INSERT INTO turn_events (id, request_id, turn_id, conversation_id, direction, sequence, event_type, status, content_hash) VALUES ('event-unsafe', 'request-unsafe', 'turn-unsafe', 'conversation-unsafe', 'inbound', 1, 'inbound_persisted', 'ok', 'sha256:x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Global.Exec(`INSERT INTO event_files (event_id, file_path, file_hash, size_bytes) VALUES ('event-unsafe', '../outside.json', 'sha256:missing', 1)`); err != nil {
		t.Fatal(err)
	}
	report, err := db.Recover(context.Background(), database.Global, root, db.RecoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.InconsistentFiles) != 1 || report.InconsistentFiles[0] != "../outside.json" {
		t.Fatalf("unsafe event path was not reported: %+v", report.InconsistentFiles)
	}
}

func TestCodeRepoUpsertAndImpactDepth(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB, err := db.OpenTeamDB(filepath.Join(root, "90_运行数据", "teams"), "team-1")
	if err != nil {
		t.Fatal(err)
	}
	defer teamDB.Close()
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAssetSubTracksSchema(teamDB); err != nil {
		t.Fatal(err)
	}
	if _, err := teamDB.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, status, visibility) VALUES ('repo-asset', 'team-1', 'codegraph', 'Repo', 'repo', 'approved', 'team')`); err != nil {
		t.Fatal(err)
	}
	first, err := db.GetOrCreateCodeRepo(context.Background(), teamDB, db.CodeRepo{ID: "repo-1", AssetID: "repo-asset", RepoURL: "https://example.test/repo", LocalPath: "C:/repo"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.GetOrCreateCodeRepo(context.Background(), teamDB, db.CodeRepo{ID: "repo-other", AssetID: "repo-asset", LocalPath: "C:/repo", Branch: "main"})
	if err != nil || first.ID != second.ID || second.Branch != "main" {
		t.Fatalf("repo upsert failed: first=%+v second=%+v err=%v", first, second, err)
	}
	for _, symbol := range []struct{ id, name string }{{"s0", "root"}, {"s1", "middle"}, {"s2", "leaf"}} {
		if _, err := teamDB.Exec(`INSERT INTO code_files (id, repo_id, path, file_hash) VALUES (?, 'repo-1', ?, 'hash')`, "f"+symbol.id, symbol.name+".go"); err != nil {
			t.Fatal(err)
		}
		if _, err := teamDB.Exec(`INSERT INTO code_symbols (id, file_id, name, kind, symbol_hash) VALUES (?, ?, ?, 'function', 'hash')`, symbol.id, "f"+symbol.id, symbol.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := teamDB.Exec(`INSERT INTO code_edges (id, source_symbol_id, target_symbol_id, edge_type) VALUES ('e01','s0','s1','call'), ('e12','s1','s2','call')`); err != nil {
		t.Fatal(err)
	}
	neighbors, err := db.ImpactNeighbors(context.Background(), teamDB, "s0", "callees", 2)
	if err != nil || len(neighbors) != 2 {
		t.Fatalf("impact depth was not level based: neighbors=%+v err=%v", neighbors, err)
	}
}

var _ *sql.DB
