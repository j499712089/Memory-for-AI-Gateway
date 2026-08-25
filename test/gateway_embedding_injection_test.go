package test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"gateway/internal/auth"
	"gateway/internal/db"
	"gateway/internal/embedding"
	"gateway/internal/hashutil"
	"gateway/internal/httpx"
	"gateway/internal/secrets"

	"github.com/gin-gonic/gin"
)

// Approved multilingual embedding model (same fixture as the
// internal/embedding approved-asset gates; overridable through the
// EMBEDDING_MODEL_PATH / EMBEDDING_TOKENIZER_PATH environment variables).
var (
	embeddingFixtureModelPath     = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\model_quantized.onnx`
	embeddingFixtureTokenizerPath = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json`
)

// zhArchivalMemoryText / zhArchivalMemoryQuery is a Chinese synonymous pair
// with ZERO shared characters: the FTS/LIKE path cannot surface the memory,
// so only semantic (vector) retrieval can inject it (ALL-233 criterion 2).
const (
	zhArchivalMemoryText   = "会议纪要必须在会后一天内归档"
	zhArchivalMemoryQuery  = "碰头记录需于散场次日妥善保存"
	zhArchivalSourceEventID = "evt-archival"
)

func approvedEmbeddingFixtureService(t *testing.T) *embedding.Service {
	t.Helper()
	model := os.Getenv("EMBEDDING_MODEL_PATH")
	tokenizer := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if model == "" {
		model = embeddingFixtureModelPath
	}
	if tokenizer == "" {
		tokenizer = embeddingFixtureTokenizerPath
	}
	service, err := embedding.NewService(model, tokenizer)
	if err != nil {
		t.Fatalf("approved embedding gate failed: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

// embeddingInjectionGateway is a gateway fixture that wires an optional
// embedding service into the LLM injection chain and accepts a custom user
// message, so semantic retrieval over a seeded vector can be asserted.
type embeddingInjectionGateway struct {
	db         *sql.DB
	router     *gin.Engine
	apiKey     string
	memoryRoot string
	teamID     string
}

func newEmbeddingInjectionGateway(t *testing.T, upstreamURL string, service *embedding.Service) *embeddingInjectionGateway {
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

	handler := httpx.NewGatewayHandler(database.Global, secretsManager, memoryRoot, service)
	authMgr := auth.NewMiddleware(database.Global)
	router := gin.New()
	router.POST("/codebuddy/:channel/v1/chat/completions",
		httpx.AuthMiddleware(authMgr),
		httpx.IdempotencyMiddleware(database.Global),
		handler.HandleChatCompletions,
	)
	return &embeddingInjectionGateway{
		db:         database.Global,
		router:     router,
		apiKey:     apiKey,
		memoryRoot: memoryRoot,
		teamID:     teamID,
	}
}

func (g *embeddingInjectionGateway) openTeamDB(t *testing.T) *sql.DB {
	t.Helper()
	teamDB, err := db.OpenTeamDB(filepath.Join(g.memoryRoot, "90_运行数据", "teams"), g.teamID)
	if err != nil {
		t.Fatalf("open team db: %v", err)
	}
	t.Cleanup(func() { _ = teamDB.Close() })
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		t.Fatalf("ensure team asset schema: %v", err)
	}
	return teamDB
}

func (g *embeddingInjectionGateway) postChat(t *testing.T, userMessage string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"model":"chat-test","messages":[{"role":"user","content":` + strconv.Quote(userMessage) + `}]}`
	req := httptest.NewRequest(http.MethodPost, "/codebuddy/default/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Conversation-ID", "conv-embedding-1")
	w := httptest.NewRecorder()
	g.router.ServeHTTP(w, req)
	return w
}

// seedSemanticRetrievalAssets writes one Chinese memory with a real 384-d
// vector (older) plus two guards:
//   - a more recent unrelated decoy WITHOUT an embedding. If the injection
//     chain degrades to FTS and falls back to recent assets, the decoy is
//     injected too, so asserting its absence proves the vector path ran.
//   - a private asset scoped to another identity card WITH an embedding.
//     Asserting its absence proves ACL filtering still applies on the
//     embedding path.
func seedSemanticRetrievalAssets(t *testing.T, teamDB *sql.DB, teamID string, service *embedding.Service) {
	t.Helper()
	memoryVector, err := service.EncodeDocument(zhArchivalMemoryText)
	if err != nil {
		t.Fatalf("encode memory asset: %v", err)
	}
	privateVector, err := service.EncodeDocument("量子色动力学探讨夸克胶子等离子体")
	if err != nil {
		t.Fatalf("encode private asset: %v", err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{
			`INSERT INTO assets (id, team_id, identity_card_id, asset_type, name, slug, summary, source_event_ids, confidence, status, visibility, embedding, embedding_model_version, updated_at)
			 VALUES (?, ?, NULL, 'l2', ?, ?, ?, ?, 0.9, 'approved', 'team', ?, ?, ?)`,
			[]any{"asset-memory", teamID, zhArchivalMemoryText, "meeting-minutes-archival", zhArchivalMemoryText,
				`["` + zhArchivalSourceEventID + `"]`, embedding.Float32ToBytes(memoryVector), embedding.ModelVersion, "2026-01-01T00:00:00Z"},
		},
		{
			`INSERT INTO assets (id, team_id, identity_card_id, asset_type, name, slug, summary, source_event_ids, confidence, status, visibility, embedding, embedding_model_version, updated_at)
			 VALUES (?, ?, 'card-other', 'l3', ?, ?, ?, ?, 0.9, 'approved', 'private', ?, ?, ?)`,
			[]any{"asset-private", teamID, "Private Memory", "private-memory", "量子色动力学探讨夸克胶子等离子体",
				`["evt-private"]`, embedding.Float32ToBytes(privateVector), embedding.ModelVersion, "2026-05-01T00:00:00Z"},
		},
		{
			`INSERT INTO assets (id, team_id, asset_type, name, slug, summary, source_event_ids, confidence, status, visibility, updated_at)
			 VALUES (?, ?, 'l2', ?, ?, ?, ?, 0.9, 'approved', 'team', ?)`,
			[]any{"asset-decoy", teamID, "Decoy Memory", "decoy-memory", "量子色动力学探讨夸克胶子等离子体",
				`["evt-decoy"]`, "2026-06-01T00:00:00Z"},
		},
	}
	for _, stmt := range statements {
		if _, err := teamDB.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed semantic asset: %v", err)
		}
	}
}

// TestGatewayEmbeddingInjectionSurfacesSemanticMemory is the ALL-233 gateway
// injection regression: a Chinese memory with a 384-d vector is retrievable
// through a synonym query that shares no characters with it, and the upstream
// payload carries the memory text and its source_id. The decoy/private guards
// pin the vector path (not the recency fallback) and ACL filtering.
func TestGatewayEmbeddingInjectionSurfacesSemanticMemory(t *testing.T) {
	var upstreamCalls atomic.Int32
	var lastRequestBody atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		lastRequestBody.Store(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	service := approvedEmbeddingFixtureService(t)
	gateway := newEmbeddingInjectionGateway(t, upstream.URL, service)
	seedSemanticRetrievalAssets(t, gateway.openTeamDB(t), gateway.teamID, service)

	response := gateway.postChat(t, zhArchivalMemoryQuery)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("expected upstream to be called once, got %d", upstreamCalls.Load())
	}
	payload, ok := lastRequestBody.Load().([]byte)
	if !ok || len(payload) == 0 {
		t.Fatal("upstream payload not captured")
	}
	injected := string(payload)
	t.Logf("upstream payload contains memory=%t source_id=%t\n%s", strings.Contains(injected, zhArchivalMemoryText), strings.Contains(injected, zhArchivalSourceEventID), injected)

	// The synonym memory and its source_id must reach the upstream payload.
	if !strings.Contains(injected, zhArchivalMemoryText) {
		t.Fatalf("upstream payload missing semantic memory: %.400s", injected)
	}
	if !strings.Contains(injected, zhArchivalSourceEventID) {
		t.Fatalf("upstream payload missing source_id: %.400s", injected)
	}
	// The recent decoy would be injected by the recency fallback, so its
	// absence proves the vector path produced the candidates.
	if strings.Contains(injected, "量子色动力学") {
		t.Fatalf("recency fallback leaked a non-vector decoy into the payload: %.400s", injected)
	}
	// The private asset is embedded but scoped to another identity card; ACL
	// must filter it out of the embedding path too.
	if strings.Contains(injected, "evt-private") {
		t.Fatalf("private asset leaked through the embedding path: %.400s", injected)
	}

	// The injection snapshot records the same source id for auditability.
	requestID := response.Header().Get("X-Request-ID")
	var sourceJSON string
	if err := gateway.db.QueryRow(`SELECT source_ids_json FROM injection_snapshots WHERE request_id = ?`, requestID).Scan(&sourceJSON); err != nil {
		t.Fatalf("read injection snapshot: %v", err)
	}
	if !strings.Contains(sourceJSON, zhArchivalSourceEventID) {
		t.Fatalf("injection snapshot missing source_id: %s", sourceJSON)
	}
}

// TestGatewayEmbeddingInjectionDegradesToFTSWhenEmbeddingFails verifies
// ALL-233 criterion 1: a configured-but-failing embedding service must not
// break the LLM path. SearchHybrid logs the degradation and falls back to the
// FTS results; the upstream payload still receives the lexical memory.
func TestGatewayEmbeddingInjectionDegradesToFTSWhenEmbeddingFails(t *testing.T) {
	var upstreamCalls atomic.Int32
	var lastRequestBody atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		lastRequestBody.Store(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	// A zero-value Service is configured but every Encode fails, exactly like
	// an unavailable model at startup.
	broken := &embedding.Service{}
	gateway := newEmbeddingInjectionGateway(t, upstream.URL, broken)
	seedLLMTeamAssets(t, gateway.openTeamDB(t), gateway.teamID)

	response := gateway.postChat(t, "sqlite concurrency")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	payload, ok := lastRequestBody.Load().([]byte)
	if !ok || len(payload) == 0 {
		t.Fatal("upstream payload not captured")
	}
	injected := string(payload)
	if !strings.Contains(injected, "sqlite concurrency checkpoint") || !strings.Contains(injected, "evt-own") {
		t.Fatalf("FTS fallback did not inject the lexical memory: %.400s", injected)
	}
}
