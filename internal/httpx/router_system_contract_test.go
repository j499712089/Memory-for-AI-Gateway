package httpx

import (
	"path/filepath"
	"testing"

	"gateway/internal/secrets"

	"github.com/gin-gonic/gin"
)

func TestSystemManagementRouteContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := setupSystemTestDB(t)
	defer database.Close()

	manager, err := secrets.NewManager(filepath.Join(t.TempDir(), "secrets"), true)
	if err != nil {
		t.Fatalf("create secrets manager: %v", err)
	}
	router := SetupRouter(database, manager, t.TempDir())

	want := map[string]bool{
		"POST /api/system/service/start":   true,
		"POST /api/system/service/stop":    true,
		"POST /api/system/service/restart": true,
		"GET /api/system/service/status":   true,
		"PUT /api/system/autostart":        true,
		"GET /api/system/logs":             true,
		"POST /api/system/backup":          true,
		"POST /api/system/restore":         true,
		"GET /api/system/config":           true,
	}

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	for route := range want {
		if !registered[route] {
			t.Errorf("missing route %s", route)
		}
	}
}
