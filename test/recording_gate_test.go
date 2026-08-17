package test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

var errSimulatedUnwritable = &simulatedError{message: "simulated unwritable sink"}

type simulatedError struct {
	message string
}

func (e *simulatedError) Error() string { return e.message }
