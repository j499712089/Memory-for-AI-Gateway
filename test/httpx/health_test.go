package httpx_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gateway/internal/db"
	"gateway/internal/httpx"
	"gateway/internal/secrets"

	"github.com/gin-gonic/gin"
)

func setupTestDB(t *testing.T) (*db.DB, string) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Find schema file
	schemaPath := "../../schema/schema.sql"
	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		// Try alternative path
		schemaPath = "../schema/schema.sql"
	}

	if err := db.Migrate(database.Global, schemaPath); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	return database, tmpDir
}

func TestHealthEndpoint(t *testing.T) {
	database, _ := setupTestDB(t)
	defer database.Close()

	handler := httpx.NewHealthHandler(database.Global)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/health", handler.HandleHealth)

	req, _ := http.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %v", response["status"])
	}

	if response["journal_mode"] != "wal" {
		t.Errorf("Expected journal_mode 'wal', got %v", response["journal_mode"])
	}
}

func TestCreateTeam(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer database.Close()

	secretsManager, err := secrets.NewManager(filepath.Join(tmpDir, "secrets"), true)
	if err != nil {
		t.Fatalf("Failed to create secrets manager: %v", err)
	}

	handler := httpx.NewAdminHandler(database.Global, secretsManager)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/teams", handler.HandleCreateTeam)

	// Create team request
	teamReq := map[string]interface{}{
		"name":        "Test Team",
		"slug":        "test-team",
		"description": "A test team",
		"visibility":  "private",
	}

	body, _ := json.Marshal(teamReq)
	req, _ := http.NewRequest("POST", "/api/teams", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d. Body: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response["slug"] != "test-team" {
		t.Errorf("Expected slug 'test-team', got %v", response["slug"])
	}

	if response["status"] != "active" {
		t.Errorf("Expected status 'active', got %v", response["status"])
	}
}

func TestCreateTeamDuplicateSlug(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer database.Close()

	secretsManager, err := secrets.NewManager(filepath.Join(tmpDir, "secrets"), true)
	if err != nil {
		t.Fatalf("Failed to create secrets manager: %v", err)
	}

	handler := httpx.NewAdminHandler(database.Global, secretsManager)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/teams", handler.HandleCreateTeam)

	// Create first team
	teamReq := map[string]interface{}{
		"name": "Test Team 1",
		"slug": "duplicate-slug",
	}

	body, _ := json.Marshal(teamReq)
	req, _ := http.NewRequest("POST", "/api/teams", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("First team creation failed: %d", w.Code)
	}

	// Try to create second team with same slug
	teamReq2 := map[string]interface{}{
		"name": "Test Team 2",
		"slug": "duplicate-slug",
	}

	body2, _ := json.Marshal(teamReq2)
	req2, _ := http.NewRequest("POST", "/api/teams", bytes.NewBuffer(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("Expected status 409 for duplicate slug, got %d", w2.Code)
	}
}
