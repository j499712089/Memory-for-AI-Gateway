package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gateway/internal/db"

	"github.com/gin-gonic/gin"
)

func setupIdentityTestDB(t *testing.T) *db.DB {
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, ".runtime", "memory-gateway.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.Migrate(database.Global, filepath.Join("..", "..", "schema", "schema.sql")); err != nil {
		database.Global.Close()
		t.Fatalf("migrate database: %v", err)
	}

	// Seed test data
	if _, err := database.Global.Exec(`
		INSERT INTO teams (id, name, slug) VALUES ('team-1', 'Test Team', 'test-team')
	`); err != nil {
		database.Global.Close()
		t.Fatalf("seed team: %v", err)
	}

	if _, err := database.Global.Exec(`
		INSERT INTO agents (id, team_id, name) VALUES ('agent-123', 'team-1', 'Test Agent')
	`); err != nil {
		database.Global.Close()
		t.Fatalf("seed agent: %v", err)
	}

	if _, err := database.Global.Exec(`
		INSERT INTO identity_cards (id, name, role, status)
		VALUES ('card-1', 'Test Card', 'test-role', 'active')
	`); err != nil {
		database.Global.Close()
		t.Fatalf("seed identity card: %v", err)
	}

	return database
}

func TestHandleBindAgent(t *testing.T) {
	database := setupIdentityTestDB(t)
	defer database.Global.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewIdentityHandler(database.Global)
	router.POST("/identity-cards/:id/bind-agent", handler.HandleBindAgent)

	payload := map[string]interface{}{
		"agent_id":   "agent-123",
		"agent_name": "Test Agent",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/identity-cards/card-1/bind-agent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp BindAgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.AgentID != "agent-123" {
		t.Fatalf("expected agent_id agent-123, got %s", resp.AgentID)
	}
	if resp.IdentityCardID != "card-1" {
		t.Fatalf("expected identity_card_id card-1, got %s", resp.IdentityCardID)
	}

	// Verify binding in database
	var agentID string
	err := database.Global.QueryRow(`SELECT agent_id FROM identity_cards WHERE id=?`, "card-1").Scan(&agentID)
	if err != nil {
		t.Fatalf("query bound agent: %v", err)
	}
	if agentID != "agent-123" {
		t.Fatalf("expected agent-123, got %s", agentID)
	}
}

func TestHandleBindAgentInvalidCard(t *testing.T) {
	database := setupIdentityTestDB(t)
	defer database.Global.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewIdentityHandler(database.Global)
	router.POST("/identity-cards/:id/bind-agent", handler.HandleBindAgent)

	payload := map[string]interface{}{
		"agent_id":   "agent-123",
		"agent_name": "Test Agent",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/identity-cards/non-existent/bind-agent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleTestConnection(t *testing.T) {
	database := setupIdentityTestDB(t)
	defer database.Global.Close()

	// Bind an agent first
	_, err := database.Global.Exec(`UPDATE identity_cards SET agent_id = 'agent-123' WHERE id = 'card-1'`)
	if err != nil {
		t.Fatalf("bind agent: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewIdentityHandler(database.Global)
	router.POST("/identity-cards/:id/test-connection", handler.HandleTestConnection)

	req := httptest.NewRequest(http.MethodPost, "/identity-cards/card-1/test-connection", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("expected success true, got false: %s", resp.Message)
	}
	if resp.AgentID != "agent-123" {
		t.Fatalf("expected agent_id agent-123, got %s", resp.AgentID)
	}
}

func TestHandleTestConnectionInvalidCard(t *testing.T) {
	database := setupIdentityTestDB(t)
	defer database.Global.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewIdentityHandler(database.Global)
	router.POST("/identity-cards/:id/test-connection", handler.HandleTestConnection)

	req := httptest.NewRequest(http.MethodPost, "/identity-cards/non-existent/test-connection", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
