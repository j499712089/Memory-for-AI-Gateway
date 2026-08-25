package test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"gateway/internal/db"
	"gateway/internal/httpx"

	"github.com/gin-gonic/gin"
)

func TestMCPMemoryGetRejectsAndAuditsBodyPathEscape(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB, err := db.OpenTeamDB(filepath.Join(root, "90_运行数据", "teams"), "team-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		teamDB.Close()
		t.Fatal(err)
	}
	_, err = teamDB.Exec(`INSERT INTO assets
		(id, team_id, asset_type, name, slug, summary, body_path, source_event_ids, confidence, status, visibility)
		VALUES ('asset-escape', 'team-1', 'l1', 'Escape', 'escape', 'escape', '../outside.txt', '[]', 1, 'approved', 'team')`)
	teamDB.Close()
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	handler := httpx.NewMCPHandler(database.Global, root)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/mcp/memory/get", strings.NewReader(`{"asset_id":"asset-escape","team_id":"team-1"}`))
	testContext, _ := gin.CreateTestContext(recorder)
	testContext.Request = request
	testContext.Set("team_id", "team-1")
	testContext.Set("api_key_id", "key-1")
	handler.HandleMemoryGet(testContext)

	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), `"path_not_allowed"`) {
		t.Fatalf("expected path_not_allowed 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var action, targetType, targetID, result, actorType, actorID, failure string
	err = database.Global.QueryRowContext(testContext.Request.Context(), `
		SELECT action, target_type, target_id, result, actor_type, COALESCE(actor_id,''), COALESCE(failure,'')
		FROM audit_log WHERE action='path_access_denied' ORDER BY created_at DESC LIMIT 1`).Scan(
		&action, &targetType, &targetID, &result, &actorType, &actorID, &failure)
	if err != nil {
		t.Fatalf("path rejection audit missing: %v", err)
	}
	if action != "path_access_denied" || targetType != "memory_path" || targetID != "../outside.txt" || result != "denied" || actorType != "system" || actorID != "key-1" || failure == "" {
		t.Fatalf("unexpected path audit row: action=%q target_type=%q target_id=%q result=%q actor_type=%q actor_id=%q failure=%q", action, targetType, targetID, result, actorType, actorID, failure)
	}
}
