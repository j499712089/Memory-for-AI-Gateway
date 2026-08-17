package test

import (
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

	"github.com/gin-gonic/gin"
)

// llmInjectionGateway is the same test fixture as the recording gate tests but
// with a team asset DB seeded so the LLM path has retrievable memories.
func newLLMInjectionGateway(t *testing.T, upstreamURL string, tokenBudgetEnv string) *recordingGateGateway {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if tokenBudgetEnv != "" {
		t.Setenv("MEMORY_TOKEN_BUDGET", tokenBudgetEnv)
	}

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

	// Seed a team memory DB with assets so retrieval has something to return.
	teamDB, err := db.OpenTeamDB(filepath.Join(memoryRoot, "teams"), teamID)
	if err != nil {
		t.Fatalf("open team db: %v", err)
	}
	t.Cleanup(func() { _ = teamDB.Close() })
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		t.Fatalf("ensure team asset schema: %v", err)
	}
	seedLLMTeamAssets(t, teamDB, teamID)

	handler := httpx.NewGatewayHandler(database.Global, secretsManager, memoryRoot)
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

func seedLLMTeamAssets(t *testing.T, teamDB *sql.DB, teamID string) {
	t.Helper()
	_, err := teamDB.Exec(`
		INSERT INTO assets (id, team_id, identity_card_id, asset_type, name, slug, summary, source_event_ids, confidence, status, visibility)
		VALUES
		 ('asset-own', ?, NULL, 'l2', 'Gateway Memory', 'gateway-memory', 'sqlite concurrency checkpoint', '["evt-own"]', 0.9, 'approved', 'team'),
		 ('asset-long', ?, NULL, 'l1', 'Long Memory', 'long-memory', 'sqlite concurrency checkpoint with a much longer summary body so this candidate consumes several times more tokens', '["evt-long"]', 0.9, 'approved', 'team'),
		 ('asset-other', 'team-foreign', NULL, 'l2', 'Foreign Memory', 'foreign-memory', 'sqlite concurrency checkpoint', '["evt-foreign"]', 0.9, 'approved', 'team')
	`, teamID, teamID)
	if err != nil {
		t.Fatalf("seed team assets: %v", err)
	}
}

// TestGatewayLLMInjectionWiresRetrieval verifies the LLM path no longer uses the
// phase2 placeholder: snapshots carry manifest_version phase3, non-empty
// source_ids and a real token budget, and the upstream body receives the
// retrieved context (V2.1/V4.1-V4.3).
func TestGatewayLLMInjectionWiresRetrieval(t *testing.T) {
	var upstreamCalls atomic.Int32
	var lastRequestBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		lastRequestBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	gateway := newLLMInjectionGateway(t, upstream.URL, "")
	response := gateway.postChat(t, map[string]string{"X-Conversation-ID": "conv-inject-1"})
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("expected upstream to be called once, got %d", upstreamCalls.Load())
	}

	// Snapshot must be phase3 with real sources and budget.
	requestID := response.Header().Get("X-Request-ID")
	var manifestVersion string
	var sourceJSON string
	var tokenBudget, truncated int
	if err := gateway.db.QueryRow(`SELECT manifest_version, source_ids_json, token_budget, truncated FROM injection_snapshots WHERE request_id = ?`, requestID).
		Scan(&manifestVersion, &sourceJSON, &tokenBudget, &truncated); err != nil {
		t.Fatalf("read injection snapshot: %v", err)
	}
	if manifestVersion == "phase2-placeholder" || manifestVersion == "" {
		t.Fatalf("LLM path still uses placeholder manifest_version=%q", manifestVersion)
	}
	var sources []string
	if err := json.Unmarshal([]byte(sourceJSON), &sources); err != nil {
		t.Fatalf("unmarshal source ids: %v", err)
	}
	if len(sources) == 0 {
		t.Fatalf("expected non-empty source_ids, got %q", sourceJSON)
	}
	if tokenBudget <= 0 {
		t.Fatalf("expected a real token budget, got %d", tokenBudget)
	}

	// Upstream body must contain the injected retrieved context and a source
	// reference comment (V4.3).
	injected := string(lastRequestBody)
	if !strings.Contains(injected, "sqlite concurrency checkpoint") {
		t.Fatalf("upstream body missing retrieved context: %.400s", injected)
	}
	if !strings.Contains(injected, "evt-own") {
		t.Fatalf("upstream body missing source id reference: %.400s", injected)
	}
}

// TestGatewayLLMInjectionEnforcesACLAndBudget verifies cross-team memory is not
// injected (V2.2) and that a small token budget produces truncated=true (V2.3).
func TestGatewayLLMInjectionEnforcesACLAndBudget(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	gateway := newLLMInjectionGateway(t, upstream.URL, "10")
	response := gateway.postChat(t, map[string]string{"X-Conversation-ID": "conv-acl-1"})
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	requestID := response.Header().Get("X-Request-ID")
	var sourceJSON string
	var tokenBudget, truncated int
	if err := gateway.db.QueryRow(`SELECT source_ids_json, token_budget, truncated FROM injection_snapshots WHERE request_id = ?`, requestID).
		Scan(&sourceJSON, &tokenBudget, &truncated); err != nil {
		t.Fatalf("read injection snapshot: %v", err)
	}
	var sources []string
	if err := json.Unmarshal([]byte(sourceJSON), &sources); err != nil {
		t.Fatalf("unmarshal source ids: %v", err)
	}
	for _, id := range sources {
		if id == "evt-foreign" {
			t.Fatalf("cross-team memory leaked into injection: %v", sources)
		}
	}
	if len(sources) == 0 {
		t.Fatalf("expected own-team source ids under budget, got %q", sourceJSON)
	}
	if tokenBudget != 10 {
		t.Fatalf("expected token budget 10, got %d", tokenBudget)
	}
	if truncated != 1 {
		t.Fatalf("expected truncated=1 when budget 5 is exceeded, got %d (sources=%v)", truncated, sources)
	}
}
