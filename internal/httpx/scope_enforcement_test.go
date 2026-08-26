package httpx

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"gateway/internal/hashutil"
	"gateway/internal/secrets"

	"github.com/gin-gonic/gin"
)

func seedTeam(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO teams (id, name, slug) VALUES ('team-1', 'Team 1', 'team-1')`)
	if err != nil {
		t.Fatalf("seed team: %v", err)
	}
}

func seedKey(t *testing.T, db *sql.DB, id, token, scopes string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO api_keys (id, team_id, key_hash, key_ref, scopes) VALUES (?, 'team-1', ?, 'ref', ?)`,
		id, hashutil.SHA256(token), scopes,
	)
	if err != nil {
		t.Fatalf("seed key %s: %v", id, err)
	}
}

func scopeTestRouter(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	database := setupSystemTestDB(t)
	t.Cleanup(func() { _ = database.Close() })
	seedTeam(t, database)
	manager, err := secrets.NewManager(filepath.Join(t.TempDir(), "secrets"), true)
	if err != nil {
		t.Fatalf("create secrets manager: %v", err)
	}
	router := SetupRouter(database, manager, t.TempDir())
	return router, database
}

func doRequest(t *testing.T, handler http.Handler, method, path, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// TestScopeEnforcement_AdminRouteRejectsNonAdminKey proves the reviewer's
// acceptance case: a key carrying only the "mcp" scope is rejected (403) on an
// admin route, while an admin-scoped key is allowed. Also proves x-api-key is
// an accepted authentication transport through the same scope gate.
func TestScopeEnforcement_AdminRouteRejectsNonAdminKey(t *testing.T) {
	router, database := scopeTestRouter(t)
	seedKey(t, database, "key-admin", "admin-token", `["admin","gateway","mcp"]`)
	seedKey(t, database, "key-mcp", "mcp-token", `["mcp"]`)

	// mcp-only key -> admin route must be rejected
	w := doRequest(t, router, http.MethodGet, "/api/teams", "mcp-token", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("mcp-only key on admin route: got status %d, want 403; body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse 403 body: %v", err)
	}
	if body.Error.Type != "forbidden" {
		t.Fatalf("403 error type = %q, want forbidden", body.Error.Type)
	}
	if strings.Contains(w.Body.String(), "request_id") {
		t.Fatalf("admin error must not carry request_id, got %s", w.Body.String())
	}

	// admin key -> same route allowed
	w2 := doRequest(t, router, http.MethodGet, "/api/teams", "admin-token", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("admin key on admin route: got status %d, want 200; body=%s", w2.Code, w2.Body.String())
	}

	// x-api-key header transport must satisfy the same admin scope gate
	req := httptest.NewRequest(http.MethodGet, "/api/teams", nil)
	req.Header.Set("x-api-key", "admin-token")
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req)
	if w3.Code != http.StatusOK {
		t.Fatalf("x-api-key admin key on admin route: got status %d, want 200; body=%s", w3.Code, w3.Body.String())
	}
}

// TestScopeEnforcement_GatewayRouteRequiresGatewayScope proves gateway routes
// reject keys that lack the "gateway" scope before reaching the handler.
func TestScopeEnforcement_GatewayRouteRequiresGatewayScope(t *testing.T) {
	router, database := scopeTestRouter(t)
	seedKey(t, database, "key-mcp", "mcp-token", `["mcp"]`)
	seedKey(t, database, "key-gw", "gw-token", `["gateway","mcp"]`)

	reqBody := []byte(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`)

	w := doRequest(t, router, http.MethodPost, "/v1/messages", "mcp-token", reqBody)
	if w.Code != http.StatusForbidden {
		t.Fatalf("mcp-only key on gateway route: got status %d, want 403; body=%s", w.Code, w.Body.String())
	}

	w2 := doRequest(t, router, http.MethodPost, "/v1/messages", "gw-token", reqBody)
	if w2.Code == http.StatusForbidden {
		t.Fatalf("gateway-scoped key must pass the scope gate, got 403; body=%s", w2.Body.String())
	}
}

// TestScopeEnforcement_CreateAPIKeyPersistsRequestedScopes proves POST
// /api/api-keys stores the caller-supplied scopes (the previous implementation
// hardcoded ["gateway","mcp"] and ignored req.Scopes).
func TestScopeEnforcement_CreateAPIKeyPersistsRequestedScopes(t *testing.T) {
	router, database := scopeTestRouter(t)
	seedKey(t, database, "key-admin", "admin-token", `["admin"]`)

	create := func(scopesJSON string) *httptest.ResponseRecorder {
		body := []byte(`{"team_id":"team-1","scopes":` + scopesJSON + `}`)
		return doRequest(t, router, http.MethodPost, "/api/api-keys", "admin-token", body)
	}

	// Custom scopes must be persisted and echoed.
	w := create(`["mcp"]`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create key with scopes=[mcp]: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		ID     string `json:"id"`
		Scopes string `json:"scopes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse create response: %v", err)
	}
	if resp.Scopes != `["mcp"]` {
		t.Fatalf("create response scopes = %q, want [\"mcp\"]", resp.Scopes)
	}
	var stored string
	if err := database.QueryRow(`SELECT scopes FROM api_keys WHERE id = ?`, resp.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored scopes: %v", err)
	}
	if stored != `["mcp"]` {
		t.Fatalf("stored scopes = %q, want [\"mcp\"]", stored)
	}

	// Unknown scope must be rejected with 400.
	wBad := create(`["admin","root"]`)
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("unknown scope create: status=%d, want 400; body=%s", wBad.Code, wBad.Body.String())
	}

	// Omitted scopes must fall back to the documented default.
	wDefault := create(`null`)
	if wDefault.Code != http.StatusCreated {
		t.Fatalf("create key default scopes: status=%d body=%s", wDefault.Code, wDefault.Body.String())
	}
	if err := json.Unmarshal(wDefault.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse default create response: %v", err)
	}
	if resp.Scopes != `["gateway","mcp"]` {
		t.Fatalf("default scopes = %q, want [\"gateway\",\"mcp\"]", resp.Scopes)
	}
}

// TestDocsRouteServesOnlyEmbeddedResources proves /docs exposes only the
// OpenAPI spec and Swagger UI assets, and internal design/deployment documents
// are not reachable.
func TestDocsRouteServesOnlyEmbeddedResources(t *testing.T) {
	router, _ := scopeTestRouter(t)

	allowed := []string{
		"/docs/openapi.yaml",
		"/docs/swagger-ui/",
		"/docs/swagger-ui/index.html",
		"/docs/swagger-ui/swagger-ui-bundle.js",
		"/docs/swagger-ui/swagger-ui.css",
	}
	for _, p := range allowed {
		w := doRequest(t, router, http.MethodGet, p, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: got status %d, want 200; body=%s", p, w.Code, w.Body.String())
		}
	}

	denied := []string{
		"/docs/04_database_design.md",
		"/docs/API.md",
		"/docs/02_architecture_design.md",
		"/docs/delivery_report.md",
	}
	for _, p := range denied {
		w := doRequest(t, router, http.MethodGet, p, "", nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET %s: got status %d, want 404", p, w.Code)
		}
	}
}
