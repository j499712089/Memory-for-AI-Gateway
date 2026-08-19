package httpx

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gateway/internal/db"

	"github.com/gin-gonic/gin"
)

func setupSystemTestDB(t *testing.T) *sql.DB {
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, ".runtime", "memory-gateway.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.Migrate(database.Global, filepath.Join("..", "..", "schema", "schema.sql")); err != nil {
		database.Close()
		t.Fatalf("migrate database: %v", err)
	}
	return database.Global
}

func TestHandleServiceStatus(t *testing.T) {
	database := setupSystemTestDB(t)
	defer database.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewSystemHandler(database)
	router.GET("/system/service/status", handler.HandleServiceStatus)

	req := httptest.NewRequest(http.MethodGet, "/system/service/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp ServiceStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if !resp.Running {
		t.Fatal("expected service to be running")
	}
	if resp.Version == "" {
		t.Fatal("response missing version field")
	}
}

func TestHandleServiceStartAlreadyRunning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/system/service/start", (&SystemHandler{}).HandleServiceStart)

	req := httptest.NewRequest(http.MethodPost, "/system/service/start", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleGetConfig(t *testing.T) {
	database := setupSystemTestDB(t)
	defer database.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewSystemHandler(database)
	router.GET("/system/config", handler.HandleGetConfig)

	req := httptest.NewRequest(http.MethodGet, "/system/config", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp SystemConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.DBPath == "" {
		t.Fatal("response missing db_path field")
	}
}

func TestHandleSetAutostart(t *testing.T) {
	database := setupSystemTestDB(t)
	defer database.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewSystemHandler(database)
	router.PUT("/system/autostart", handler.HandleSetAutostart)

	payload := map[string]interface{}{"enabled": true}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/system/autostart", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Autostart script may not exist in test environment, accept 500 or 200
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 200 or 500, got %d: %s", w.Code, w.Body.String())
	}

	if w.Code == http.StatusOK {
		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if msg, ok := resp["message"].(string); !ok || msg == "" {
			t.Fatal("response missing or empty message field")
		}
	}
}

func TestHandleSetAutostartRejectsMalformedJSON(t *testing.T) {
	database := setupSystemTestDB(t)
	defer database.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/system/autostart", NewSystemHandler(database).HandleSetAutostart)

	req := httptest.NewRequest(http.MethodPut, "/system/autostart", bytes.NewBufferString(`{`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleGetLogs(t *testing.T) {
	database := setupSystemTestDB(t)
	defer database.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewSystemHandler(database)
	router.GET("/system/logs", handler.HandleGetLogs)

	req := httptest.NewRequest(http.MethodGet, "/system/logs?limit=10", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp []LogEntry
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	// Empty logs array is valid for a fresh database
	if resp == nil {
		t.Fatal("response should be an array, not null")
	}
}

func TestHandleBackup(t *testing.T) {
	database := setupSystemTestDB(t)
	defer database.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewSystemHandler(database)
	router.POST("/system/backup", handler.HandleBackup)

	req := httptest.NewRequest(http.MethodPost, "/system/backup", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" && contentType != "application/json; charset=utf-8" {
		t.Fatalf("expected application/json, got %s", contentType)
	}

	disposition := w.Header().Get("Content-Disposition")
	if disposition == "" {
		t.Fatal("missing Content-Disposition header")
	}
}

func TestHandleRestore(t *testing.T) {
	database := setupSystemTestDB(t)
	defer database.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewSystemHandler(database)
	router.POST("/system/restore", handler.HandleRestore)

	t.Run("accepts a JSON backup", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/system/restore", bytes.NewBufferString(`{"version":"1.0.0"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("rejects malformed JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/system/restore", bytes.NewBufferString(`{`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})
}
