package test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gateway/internal/adapter"
	"gateway/internal/db"
	"gateway/internal/hashutil"
	"gateway/internal/httpx"
	"gateway/internal/secrets"

	"github.com/gin-gonic/gin"
)

type phase2Gateway struct {
	db         *sql.DB
	router     *gin.Engine
	apiKey     string
	memoryRoot string
	teamID     string
}

func newPhase2Gateway(t *testing.T, upstreamURL, channelProtocol, channelName string) *phase2Gateway {
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

	teamID := "team-phase2"
	apiKey := "gateway-test-key"
	if _, err := database.Global.Exec(`INSERT INTO teams (id, name, slug) VALUES (?, ?, ?)`, teamID, "Phase 2", "phase-2"); err != nil {
		t.Fatalf("insert team: %v", err)
	}
	if _, err := database.Global.Exec(`
		INSERT INTO api_keys (id, team_id, key_hash, key_ref, scopes, enabled)
		VALUES (?, ?, ?, ?, ?, 1)
	`, "api-phase2", teamID, hashutil.SHA256(apiKey), "api-key-ref", `["gateway"]`); err != nil {
		t.Fatalf("insert API key: %v", err)
	}
	if _, err := database.Global.Exec(`
		INSERT INTO upstream_channels (id, team_id, name, protocol, base_url, model, api_key_ref, enabled, priority)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, 100)
	`, "channel-phase2", teamID, channelName, channelProtocol, upstreamURL, "test-model", upstreamKeyRef); err != nil {
		t.Fatalf("insert upstream channel: %v", err)
	}

	return &phase2Gateway{
		db:         database.Global,
		router:     httpx.SetupRouter(database.Global, secretsManager, memoryRoot),
		apiKey:     apiKey,
		memoryRoot: memoryRoot,
		teamID:     teamID,
	}
}

func performGatewayRequest(t *testing.T, router *gin.Engine, path string, body []byte, apiKey string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func eventCount(t *testing.T, database *sql.DB, requestID, eventType string) int {
	t.Helper()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM turn_events WHERE request_id = ? AND event_type = ?`, requestID, eventType).Scan(&count); err != nil {
		t.Fatalf("count %s events: %v", eventType, err)
	}
	return count
}

func TestAnthropicInjectionPreservesClientFields(t *testing.T) {
	original := []byte(`{"model":"claude-test","max_tokens":32,"system":[{"type":"text","text":"client system"}],"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"u-1"}}`)
	forwarded, err := adapter.BuildAnthropicUpstreamRequest(original, "memory context")
	if err != nil {
		t.Fatalf("build upstream request: %v", err)
	}
	if !bytes.Equal(original, []byte(`{"model":"claude-test","max_tokens":32,"system":[{"type":"text","text":"client system"}],"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"u-1"}}`)) {
		t.Fatal("injection mutated the caller's request bytes")
	}

	var request map[string]json.RawMessage
	if err := json.Unmarshal(forwarded, &request); err != nil {
		t.Fatalf("decode forwarded request: %v", err)
	}
	var system []adapter.AnthropicSystemBlock
	if err := json.Unmarshal(request["system"], &system); err != nil {
		t.Fatalf("decode system blocks: %v", err)
	}
	if len(system) != 2 || system[0].Text != "memory context" || system[1].Text != "client system" {
		t.Fatalf("unexpected injected system blocks: %#v", system)
	}
	if string(request["metadata"]) != `{"user_id":"u-1"}` {
		t.Fatalf("unknown protocol field was lost: %s", request["metadata"])
	}
}

func TestAnthropicGatewayRecordsCompleteTurn(t *testing.T) {
	upstreamResponse := []byte(`{"id":"msg_1","type":"message","content":[{"type":"text","text":"hello"}]}`)
	var forwardedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
		forwardedBody = append([]byte(nil), mustReadBody(t, r)...)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(upstreamResponse)
	}))
	defer upstream.Close()

	gateway := newPhase2Gateway(t, upstream.URL, "anthropic_messages", "default")
	body := []byte(`{"model":"claude-test","max_tokens":32,"system":[{"type":"text","text":"client system"}],"messages":[{"role":"user","content":"hello"}]}`)
	response := performGatewayRequest(t, gateway.router, "/claude-code/default/v1/messages", body, gateway.apiKey, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if !bytes.Equal(response.Body.Bytes(), upstreamResponse) {
		t.Fatalf("response was not passed through exactly: %s", response.Body.String())
	}

	var upstreamRequest map[string]json.RawMessage
	if err := json.Unmarshal(forwardedBody, &upstreamRequest); err != nil {
		t.Fatalf("decode upstream request: %v", err)
	}
	var system []adapter.AnthropicSystemBlock
	if err := json.Unmarshal(upstreamRequest["system"], &system); err != nil {
		t.Fatalf("decode injected system: %v", err)
	}
	if len(system) != 2 || system[1].Text != "client system" {
		t.Fatalf("client system was not preserved: %#v", system)
	}

	requestID := response.Header().Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("response did not include request ID")
	}
	if eventCount(t, gateway.db, requestID, "inbound_persisted") != 1 || eventCount(t, gateway.db, requestID, "complete") != 1 {
		t.Fatal("expected exactly one inbound and complete event")
	}
	var snapshots int
	if err := gateway.db.QueryRow(`SELECT COUNT(*) FROM injection_snapshots WHERE request_id = ?`, requestID).Scan(&snapshots); err != nil {
		t.Fatalf("count injection snapshots: %v", err)
	}
	if snapshots != 1 {
		t.Fatalf("expected one injection snapshot, got %d", snapshots)
	}
}

func mustReadBody(t *testing.T, request *http.Request) []byte {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read upstream request body: %v", err)
	}
	return body
}
