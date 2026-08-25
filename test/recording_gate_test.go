package test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gateway/internal/auth"
	"gateway/internal/db"
	"gateway/internal/hashutil"
	"gateway/internal/httpx"
	"gateway/internal/secrets"
	"gateway/internal/worker"

	"github.com/gin-gonic/gin"
)

// recordingGateGateway is a gateway wired with injectable writability probes
// for the dual-write degradation gate (Constitution §1.5).
type recordingGateGateway struct {
	db         *sql.DB
	router     *gin.Engine
	apiKey     string
	memoryRoot string
	teamID     string
}

func newRecordingGateGateway(t *testing.T, upstreamURL string, sqliteProbe, bufferProbe func() error) *recordingGateGateway {
	t.Helper()
	gin.SetMode(gin.TestMode)

	memoryRoot := t.TempDir()
	database, err := db.Open(filepath.Join(memoryRoot, "gateway.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database.Global, filepath.Join("..", "schema", "schema.sql")); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	secretsManager, err := secrets.NewManager(filepath.Join(memoryRoot, "secrets"), true)
	if err != nil {
		t.Fatalf("create secrets manager: %v", err)
	}
	_, upstreamKeyRef, err := secretsManager.StoreSecret("upstream-test-key")
	if err != nil {
		t.Fatalf("store upstream secret: %v", err)
	}

	teamID := "team-gate"
	apiKey := "gateway-gate-key"
	if _, err := database.Global.Exec(`INSERT INTO teams (id, name, slug) VALUES (?, ?, ?)`, teamID, "Gate", "gate"); err != nil {
		t.Fatalf("insert team: %v", err)
	}
	if _, err := database.Global.Exec(`
		INSERT INTO api_keys (id, team_id, key_hash, key_ref, scopes, enabled)
		VALUES (?, ?, ?, ?, ?, 1)
	`, "api-gate", teamID, hashutil.SHA256(apiKey), "api-key-ref", `["gateway"]`); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if _, err := database.Global.Exec(`
		INSERT INTO upstream_channels (id, team_id, name, protocol, base_url, model, api_key_ref, enabled, priority)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, 100)
	`, "channel-gate", teamID, "default", "chat_completions", upstreamURL, "test-model", upstreamKeyRef); err != nil {
		t.Fatalf("insert upstream channel: %v", err)
	}

	handler := httpx.NewGatewayHandler(database.Global, secretsManager, memoryRoot)
	handler.SetRecordingProbes(sqliteProbe, bufferProbe)
	authMgr := auth.NewMiddleware(database.Global)
	router := gin.New()
	router.POST("/codebuddy/:channel/v1/chat/completions",
		httpx.AuthMiddleware(authMgr),
		httpx.IdempotencyMiddleware(database.Global),
		handler.HandleChatCompletions,
	)
	return &recordingGateGateway{
		db:         database.Global,
		router:     router,
		apiKey:     apiKey,
		memoryRoot: memoryRoot,
		teamID:     teamID,
	}
}

func (g *recordingGateGateway) postChat(t *testing.T, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body := []byte(`{"model":"chat-test","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/codebuddy/default/v1/chat/completions", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	g.router.ServeHTTP(w, req)
	return w
}

func countBufferRows(t *testing.T, database *sql.DB, direction, eventType string) int {
	t.Helper()
	var count int
	query := `SELECT COUNT(*) FROM local_buffer WHERE json_extract(payload_json, '$.direction') = ?`
	args := []any{direction}
	if eventType != "" {
		query += ` AND json_extract(payload_json, '$.event_type') = ?`
		args = append(args, eventType)
	}
	if err := database.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count buffer rows: %v", err)
	}
	return count
}

func countJobs(t *testing.T, database *sql.DB, queue string) int {
	t.Helper()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue = ?`, queue).Scan(&count); err != nil {
		t.Fatalf("count %s jobs: %v", queue, err)
	}
	return count
}

func countL0RequestFiles(t *testing.T, memoryRoot string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(memoryRoot, "L0_原始记录", "*", "*-request-*.jsonl"))
	if err != nil {
		t.Fatalf("glob L0 request files: %v", err)
	}
	return len(matches)
}

// Test 1: SQLite temporarily unwritable → inbound event lands in local_buffer,
// a buffer_replay job is dispatched, and the request is still forwarded
// upstream with a normal 200 (degradation must not lose memory).
func TestRecordingGateDegradesInboundToLocalBuffer(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"degraded ok"}}]}`))
	}))
	defer upstream.Close()

	blockSQLite := func() error { return errSimulatedUnwritable }
	gateway := newRecordingGateGateway(t, upstream.URL, blockSQLite, nil)
	response := gateway.postChat(t, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 after degraded recording, got %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get(httpx.RecordingDegradedHeader) != "buffer" {
		t.Fatalf("expected %s: buffer on degraded response, got %q", httpx.RecordingDegradedHeader, response.Header().Get(httpx.RecordingDegradedHeader))
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("expected upstream to be called once, got %d", upstreamCalls.Load())
	}

	if count := countBufferRows(t, gateway.db, "inbound", "inbound_persisted"); count != 1 {
		t.Fatalf("expected one buffered inbound event, got %d", count)
	}
	if count := countJobs(t, gateway.db, "buffer_replay"); count == 0 {
		t.Fatal("expected at least one buffer_replay compensation job")
	}
	files, err := filepath.Glob(filepath.Join(gateway.memoryRoot, "90_运行数据", "本地持久化缓冲", "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("expected buffered event files, matches=%v err=%v", files, err)
	}
	// The degraded path must not write the inbound event straight to SQLite.
	if eventCount(t, gateway.db, response.Header().Get("X-Request-ID"), "inbound_persisted") != 0 {
		t.Fatal("inbound event should be buffered, not written to SQLite")
	}
}

// Test 2: SQLite and local buffer both unwritable → HTTP 500
// recording_unavailable and the request is NOT forwarded upstream.
func TestRecordingGateBlocksForwardWhenBothSinksUnavailable(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	blockSQLite := func() error { return errSimulatedUnwritable }
	blockBuffer := func() error { return errSimulatedUnwritable }
	gateway := newRecordingGateGateway(t, upstream.URL, blockSQLite, blockBuffer)
	response := gateway.postChat(t, nil)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 recording_unavailable, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Type != "recording_unavailable" {
		t.Fatalf("expected error.type=recording_unavailable, got %q", body.Error.Type)
	}
	if header := response.Header().Get(httpx.RecordingDegradedHeader); header != "" {
		t.Fatalf("expected no %s header on the 500 path, got %q", httpx.RecordingDegradedHeader, header)
	}
	if upstreamCalls.Load() != 0 {
		t.Fatalf("request must NOT be forwarded upstream when both sinks are unwritable, got %d calls", upstreamCalls.Load())
	}
	if count := countBufferRows(t, gateway.db, "inbound", "inbound_persisted"); count != 0 {
		t.Fatalf("no event should be buffered when the buffer is unwritable, got %d", count)
	}
}

// Test 3: idempotency — retrying the same idempotency key on the degraded
// path keeps exactly one buffered inbound record and one L0 file after replay.
func TestRecordingGateIdempotentRetryKeepsSingleBufferAndL0(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	blockSQLite := func() error { return errSimulatedUnwritable }
	gateway := newRecordingGateGateway(t, upstream.URL, blockSQLite, nil)
	headers := map[string]string{"Idempotency-Key": "retry-key-1"}
	first := gateway.postChat(t, headers)
	if first.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d: %s", first.Code, first.Body.String())
	}
	second := gateway.postChat(t, headers)
	if second.Code != http.StatusOK {
		t.Fatalf("retry: expected 200, got %d: %s", second.Code, second.Body.String())
	}

	// Exactly one buffered inbound record for the same idempotency key.
	var inboundID, requestID, payloadJSON, filePath, sha string
	err := gateway.db.QueryRow(`
		SELECT id, COALESCE(request_id,''), payload_json, file_path, sha256
		FROM local_buffer
		WHERE json_extract(payload_json, '$.direction') = 'inbound'
		  AND json_extract(payload_json, '$.buffer_key') = 'retry-key-1'
	`).Scan(&inboundID, &requestID, &payloadJSON, &filePath, &sha)
	if err != nil {
		t.Fatalf("query buffered inbound record: %v", err)
	}
	if count := countBufferRows(t, gateway.db, "inbound", "inbound_persisted"); count != 1 {
		t.Fatalf("retry must not duplicate the buffered inbound record, got %d", count)
	}
	inboundFiles, err := filepath.Glob(filepath.Join(gateway.memoryRoot, "90_运行数据", "本地持久化缓冲", "retry-key-1-inbound-*.jsonl"))
	if err != nil || len(inboundFiles) != 1 {
		t.Fatalf("expected exactly one buffered inbound file, matches=%v err=%v", inboundFiles, err)
	}

	// Replaying the single buffered record must write exactly one L0 request
	// file, and a second replay must not duplicate it.
	record := db.BufferRecord{ID: inboundID, RequestID: requestID, PayloadJSON: payloadJSON, FilePath: filePath, SHA256: sha}
	for attempt := 0; attempt < 2; attempt++ {
		if err := worker.ReplayBufferEvent(context.Background(), gateway.db, gateway.memoryRoot, record); err != nil {
			t.Fatalf("replay attempt %d: %v", attempt+1, err)
		}
	}
	if files := countL0RequestFiles(t, gateway.memoryRoot); files != 1 {
		t.Fatalf("expected exactly one L0 request file after idempotent replay, got %d", files)
	}
}

// Streaming requests pass through the same degradation gate: with both sinks
// unwritable the request is rejected with recording_unavailable before any SSE
// bytes are forwarded upstream.
func TestRecordingGateBlocksStreamingRequestWhenBothSinksUnavailable(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"x\":1}\n\n"))
	}))
	defer upstream.Close()

	gateway := newRecordingGateGateway(t, upstream.URL,
		func() error { return errSimulatedUnwritable },
		func() error { return errSimulatedUnwritable })
	body := []byte(`{"model":"chat-test","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/codebuddy/default/v1/chat/completions", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+gateway.apiKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	gateway.router.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 recording_unavailable for streaming request, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"recording_unavailable"`) {
		t.Fatalf("expected recording_unavailable error body, got %s", w.Body.String())
	}
	if header := w.Header().Get(httpx.RecordingDegradedHeader); header != "" {
		t.Fatalf("expected no %s header on the streaming 500 path, got %q", httpx.RecordingDegradedHeader, header)
	}
	if upstreamCalls.Load() != 0 {
		t.Fatalf("streaming request must not be forwarded when recording is unavailable, got %d calls", upstreamCalls.Load())
	}
}

// Replay helper sanity: a buffered inbound event replays into SQLite as a
// full ledger turn (inbound event + L0 file + injection snapshot).
func TestReplayBufferEventReconstructsSQLiteTurn(t *testing.T) {
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database.Global, filepath.Join("..", "schema", "schema.sql")); err != nil {
		t.Fatal(err)
	}

	payload := db.BufferEventPayload{
		BufferKey:                "replay-key",
		EventID:                  "event-replay",
		TurnID:                   "turn-replay",
		RequestID:                "request-replay",
		ConversationID:           "conversation-replay",
		Direction:                "inbound",
		Sequence:                 1,
		EventType:                "inbound_persisted",
		Status:                   "ok",
		RequestBody:              []byte(`{"model":"chat-test","messages":[]}`),
		InjectionManifestVersion: "phase3-v1",
		InjectionText:            "memory context",
		InjectionSources:         []string{"event-1"},
	}
	record, err := db.WriteLocalBuffer(context.Background(), database.Global, root, payload)
	if err != nil {
		t.Fatalf("write local buffer: %v", err)
	}
	if err := worker.ReplayBufferEvent(context.Background(), database.Global, root, record); err != nil {
		t.Fatalf("replay buffered event: %v", err)
	}
	var eventCount int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM turn_events WHERE id = 'event-replay' AND event_type = 'inbound_persisted'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("inbound event was not replayed, count=%d", eventCount)
	}
	var snapshots int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM injection_snapshots WHERE request_id = 'request-replay'`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 {
		t.Fatalf("injection snapshot was not replayed, count=%d", snapshots)
	}
	// Replaying again must be a no-op (no duplicate L0).
	if err := worker.ReplayBufferEvent(context.Background(), database.Global, root, record); err != nil {
		t.Fatalf("second replay: %v", err)
	}
	if files := countL0RequestFiles(t, root); files != 1 {
		t.Fatalf("expected one L0 request file after double replay, got %d", files)
	}
}

// TestReplayPendingBuffersDrainsDegradedEvents verifies the gateway maintenance
// sweep replays every pending local_buffer row back into SQLite and moves each
// row to 'replayed' (ALL-81: degraded writes must not silently stay buffered).
func TestReplayPendingBuffersDrainsDegradedEvents(t *testing.T) {
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database.Global, filepath.Join("..", "schema", "schema.sql")); err != nil {
		t.Fatal(err)
	}

	if _, err := db.WriteLocalBuffer(context.Background(), database.Global, root, db.BufferEventPayload{
		BufferKey:      "sweep-in",
		EventID:        "event-sweep-in",
		TurnID:         "turn-sweep-in",
		RequestID:      "request-sweep-in",
		ConversationID: "conversation-sweep-in",
		Direction:      "inbound",
		Sequence:       1,
		EventType:      "inbound_persisted",
		Status:         "ok",
		RequestBody:    []byte(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.WriteLocalBuffer(context.Background(), database.Global, root, db.BufferEventPayload{
		BufferKey:      "sweep-out",
		EventID:        "event-sweep-out",
		TurnID:         "turn-sweep-out",
		RequestID:      "request-sweep-out",
		ConversationID: "conversation-sweep-out",
		Direction:      "outbound",
		Sequence:       1,
		EventType:      "complete",
		Status:         "ok",
	}); err != nil {
		t.Fatal(err)
	}

	replayed, err := worker.ReplayPendingBuffers(context.Background(), database.Global, root)
	if err != nil {
		t.Fatalf("replay pending buffers: %v", err)
	}
	if replayed != 2 {
		t.Fatalf("expected 2 buffered events replayed, got %d", replayed)
	}
	var inboundCount, terminalCount int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM turn_events WHERE event_type='inbound_persisted'`).Scan(&inboundCount); err != nil {
		t.Fatal(err)
	}
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM turn_events WHERE event_type='complete'`).Scan(&terminalCount); err != nil {
		t.Fatal(err)
	}
	if inboundCount != 1 || terminalCount != 1 {
		t.Fatalf("buffered events not fully replayed: inbound=%d terminal=%d", inboundCount, terminalCount)
	}
	var pending int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM local_buffer WHERE status='pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("expected no pending buffer rows after replay, got %d", pending)
	}
}

// TestReplayBufferByIDReplaysSingleBufferedEvent covers the buffer_replay job
// handler body: a targeted replay of one buffered event plus the row status
// transition. Missing rows and already-replayed rows are no-ops.
func TestReplayBufferByIDReplaysSingleBufferedEvent(t *testing.T) {
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database.Global, filepath.Join("..", "schema", "schema.sql")); err != nil {
		t.Fatal(err)
	}
	record, err := db.WriteLocalBuffer(context.Background(), database.Global, root, db.BufferEventPayload{
		BufferKey:      "job-in",
		EventID:        "event-job-in",
		TurnID:         "turn-job-in",
		RequestID:      "request-job-in",
		ConversationID: "conversation-job-in",
		Direction:      "inbound",
		Sequence:       1,
		EventType:      "inbound_persisted",
		Status:         "ok",
		RequestBody:    []byte(`{"model":"test","messages":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := worker.ReplayBufferByID(context.Background(), database.Global, root, record.ID); err != nil {
		t.Fatalf("replay by id: %v", err)
	}
	var status string
	if err := database.Global.QueryRow(`SELECT status FROM local_buffer WHERE id=?`, record.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "replayed" {
		t.Fatalf("row status = %q, want replayed", status)
	}
	var events int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM turn_events WHERE id='event-job-in'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("buffered event was not replayed into turn_events, count=%d", events)
	}
	// Replaying again must be a no-op, and a missing id must not error.
	if err := worker.ReplayBufferByID(context.Background(), database.Global, root, record.ID); err != nil {
		t.Fatalf("second replay by id: %v", err)
	}
	if err := worker.ReplayBufferByID(context.Background(), database.Global, root, "does-not-exist"); err != nil {
		t.Fatalf("replay missing id: %v", err)
	}
}

// TestWorkerBufferReplayJobProcessesDegradedEvent wires the buffer_replay
// handler exactly as cmd/worker/main.go does and verifies a degraded request's
// compensation job replays the buffered event back into SQLite.
func TestWorkerBufferReplayJobProcessesDegradedEvent(t *testing.T) {
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database.Global, filepath.Join("..", "schema", "schema.sql")); err != nil {
		t.Fatal(err)
	}
	record, err := db.WriteLocalBuffer(context.Background(), database.Global, root, db.BufferEventPayload{
		BufferKey:      "worker-job",
		EventID:        "event-worker-job",
		TurnID:         "turn-worker-job",
		RequestID:      "request-worker-job",
		ConversationID: "conversation-worker-job",
		Direction:      "inbound",
		Sequence:       1,
		EventType:      "inbound_persisted",
		Status:         "ok",
		RequestBody:    []byte(`{"model":"test","messages":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	processor.Register("buffer_replay", func(ctx context.Context, claim *worker.Claim) error {
		var payload struct {
			BufferID string `json:"buffer_id"`
		}
		if err := json.Unmarshal([]byte(claim.PayloadJSON), &payload); err != nil {
			return err
		}
		return worker.ReplayBufferByID(ctx, database.Global, root, payload.BufferID)
	})
	if _, err := queue.Enqueue(context.Background(), worker.Job{
		Queue:        "buffer_replay",
		Priority:     90,
		Payload:      map[string]string{"buffer_id": record.ID, "request_id": record.RequestID},
		PartitionKey: "buffer:" + record.ID,
	}); err != nil {
		t.Fatal(err)
	}
	processed, err := processor.ProcessOnce(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("process buffer_replay job: %v", err)
	}
	if !processed {
		t.Fatal("expected the buffer_replay job to be claimed and processed")
	}
	var events, replayed int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM turn_events WHERE id='event-worker-job'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("buffered event was not replayed by worker job, count=%d", events)
	}
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM local_buffer WHERE id=? AND status='replayed'`, record.ID).Scan(&replayed); err != nil {
		t.Fatal(err)
	}
	if replayed != 1 {
		t.Fatalf("local_buffer row not marked replayed by worker job, count=%d", replayed)
	}
	var jobStatus string
	if err := database.Global.QueryRow(`SELECT status FROM jobs WHERE queue='buffer_replay'`).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "done" {
		t.Fatalf("buffer_replay job status = %q, want done", jobStatus)
	}
}

// TestConcurrentStreamingRequestsAllRecordedToSQLite is the ALL-53 Round 2
// regression: 5 sub-agents × 10 rounds of concurrent streaming requests must
// all land in turn_ledger / turn_events with complete terminal pairing. Before
// the ALL-81 DSN fix, every pooled connection opened with busy_timeout=0, so
// concurrent writers hit SQLITE_BUSY and ~42/50 events silently degraded to
// the local buffer with no replay.
func TestConcurrentStreamingRequestsAllRecordedToSQLite(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	// No writability probes: both sinks are healthy, so every request must be
	// recorded to SQLite synchronously (never degraded).
	gateway := newRecordingGateGateway(t, upstream.URL, nil, nil)
	const agents = 5
	const rounds = 10
	const total = agents * rounds

	var wg sync.WaitGroup
	var failed atomic.Int32
	for agent := 0; agent < agents; agent++ {
		for round := 0; round < rounds; round++ {
			wg.Add(1)
			go func(agent, round int) {
				defer wg.Done()
				body := []byte(fmt.Sprintf(`{"model":"chat-test","messages":[{"role":"user","content":"hello %d-%d"}],"stream":true}`, agent, round))
				req := httptest.NewRequest(http.MethodPost, "/codebuddy/default/v1/chat/completions", strings.NewReader(string(body)))
				req.Header.Set("Authorization", "Bearer "+gateway.apiKey)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Conversation-ID", fmt.Sprintf("e2e-r2-sub%d-%d", agent, round))
				w := httptest.NewRecorder()
				gateway.router.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					failed.Add(1)
					t.Errorf("agent %d round %d: expected 200, got %d: %s", agent, round, w.Code, w.Body.String())
					return
				}
				if header := w.Header().Get(httpx.RecordingDegradedHeader); header != "" {
					failed.Add(1)
					t.Errorf("agent %d round %d: unexpectedly degraded to buffer (%s), SQLite must accept the write", agent, round, header)
				}
			}(agent, round)
		}
	}
	wg.Wait()
	if failed.Load() != 0 {
		t.Fatalf("%d concurrent requests degraded or failed; all must record to SQLite", failed.Load())
	}

	var ledgers, inboundEvents, completeEvents, openTurns int
	if err := gateway.db.QueryRow(`SELECT COUNT(*) FROM turn_ledger`).Scan(&ledgers); err != nil {
		t.Fatal(err)
	}
	if err := gateway.db.QueryRow(`SELECT COUNT(*) FROM turn_events WHERE event_type='inbound_persisted'`).Scan(&inboundEvents); err != nil {
		t.Fatal(err)
	}
	if err := gateway.db.QueryRow(`SELECT COUNT(*) FROM turn_events WHERE event_type='complete'`).Scan(&completeEvents); err != nil {
		t.Fatal(err)
	}
	if err := gateway.db.QueryRow(`SELECT COUNT(*) FROM turn_ledger WHERE final_event_id IS NULL`).Scan(&openTurns); err != nil {
		t.Fatal(err)
	}
	if ledgers != total || inboundEvents != total || completeEvents != total {
		t.Fatalf("ALL-81 regression: turn_ledger=%d inbound=%d complete=%d, want all %d", ledgers, inboundEvents, completeEvents, total)
	}
	if openTurns != 0 {
		t.Fatalf("ALL-81 regression: %d turns lack a terminal event (incomplete pairing)", openTurns)
	}
	var buffered int
	if err := gateway.db.QueryRow(`SELECT COUNT(*) FROM local_buffer WHERE status='pending'`).Scan(&buffered); err != nil {
		t.Fatal(err)
	}
	if buffered != 0 {
		t.Fatalf("ALL-81 regression: %d events still pending in the local buffer", buffered)
	}
}

// TestReconcileBufferFilesIndexesOrphanedEvents covers the ALL-81 gap where the
// best-effort local_buffer ledger INSERT is dropped under write contention: the
// buffer file exists but has no ledger row, so the replay chain would never see
// it. Reconciliation must index the file and the sweep must replay it.
func TestReconcileBufferFilesIndexesOrphanedEvents(t *testing.T) {
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database.Global, filepath.Join("..", "schema", "schema.sql")); err != nil {
		t.Fatal(err)
	}

	payload := db.BufferEventPayload{
		BufferKey:      "orphan-in",
		EventID:        "event-orphan-in",
		TurnID:         "turn-orphan-in",
		RequestID:      "request-orphan-in",
		ConversationID: "conversation-orphan-in",
		Direction:      "inbound",
		Sequence:       1,
		EventType:      "inbound_persisted",
		Status:         "ok",
		RequestBody:    []byte(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	dir := db.LocalBufferDir(root)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write the file directly: no ledger row (simulates the dropped best-effort
	// INSERT under contention).
	if err := os.WriteFile(filepath.Join(dir, db.BufferFileName(payload)), data, 0644); err != nil {
		t.Fatal(err)
	}
	var ledgerBefore int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM local_buffer`).Scan(&ledgerBefore); err != nil {
		t.Fatal(err)
	}
	if ledgerBefore != 0 {
		t.Fatalf("expected empty local_buffer ledger, got %d rows", ledgerBefore)
	}

	indexed, err := db.ReconcileBufferFiles(context.Background(), database.Global, root)
	if err != nil {
		t.Fatalf("reconcile buffer files: %v", err)
	}
	if indexed != 1 {
		t.Fatalf("expected 1 orphaned file indexed, got %d", indexed)
	}
	replayed, err := worker.ReplayPendingBuffers(context.Background(), database.Global, root)
	if err != nil {
		t.Fatalf("replay pending buffers: %v", err)
	}
	if replayed != 1 {
		t.Fatalf("expected 1 event replayed, got %d", replayed)
	}
	var events int
	if err := database.Global.QueryRow(`SELECT COUNT(*) FROM turn_events WHERE id='event-orphan-in'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("orphaned buffered event was not replayed into turn_events, count=%d", events)
	}
	// A second reconcile+sweep must be a no-op (no duplicate replay).
	indexed, err = db.ReconcileBufferFiles(context.Background(), database.Global, root)
	if err != nil {
		t.Fatal(err)
	}
	if indexed != 0 {
		t.Fatalf("second reconcile must be a no-op, indexed=%d", indexed)
	}
}

var errSimulatedUnwritable = &simulatedError{message: "simulated unwritable sink"}

type simulatedError struct {
	message string
}

func (e *simulatedError) Error() string { return e.message }
