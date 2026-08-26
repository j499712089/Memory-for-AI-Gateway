package httpx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gateway/internal/db"

	"github.com/gin-gonic/gin"
)

// setupWikiSearchHandler builds an MCP handler backed by a real global DB
// (schema.sql) and a team DB with the full asset/wiki schema, then seeds wiki
// pages across live / archived / non-ready assets.
func setupWikiSearchHandler(t *testing.T) (*MCPHandler, *sqlTeamDB) {
	t.Helper()
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, ".runtime", "memory-gateway.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.Migrate(database.Global, filepath.Join("..", "..", "schema", "schema.sql")); err != nil {
		database.Global.Close()
		t.Fatalf("migrate database: %v", err)
	}
	if _, err := database.Global.Exec(`INSERT INTO teams (id, name, slug) VALUES ('team-1', 'Test Team', 'test-team')`); err != nil {
		database.Global.Close()
		t.Fatalf("seed team: %v", err)
	}
	handler := NewMCPHandler(database.Global, root)

	teamDB, err := db.OpenTeamDB(filepath.Join(root, "90_运行数据", "teams"), "team-1")
	if err != nil {
		database.Global.Close()
		t.Fatalf("open team db: %v", err)
	}
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		teamDB.Close()
		database.Global.Close()
		t.Fatalf("ensure assets schema: %v", err)
	}
	return handler, &sqlTeamDB{teamDB: teamDB, global: database.Global}
}

type sqlTeamDB struct {
	teamDB *sql.DB
	global *sql.DB
}

func seedWikiAsset(t *testing.T, teamDB *sql.DB, assetID, assetStatus, pageID, pageStatus, title, content string) {
	t.Helper()
	if _, err := teamDB.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, status) VALUES (?,?,?,?,?,?)`,
		assetID, "team-1", "wiki", assetID, assetID, assetStatus); err != nil {
		t.Fatalf("seed asset %s: %v", assetID, err)
	}
	if _, err := teamDB.Exec(`INSERT INTO wiki_pages (id, asset_id, title, slug, content_md, status) VALUES (?,?,?,?,?,?)`,
		pageID, assetID, title, pageID, content, pageStatus); err != nil {
		t.Fatalf("seed wiki page %s: %v", pageID, err)
	}
	if _, err := teamDB.Exec(`INSERT INTO wiki_fts (page_id, title, content) VALUES (?,?,?)`,
		pageID, title, content); err != nil {
		t.Fatalf("seed fts %s: %v", pageID, err)
	}
}

func runWikiSearch(t *testing.T, handler *MCPHandler, query string) ([]string, int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("team_id", "team-1"); c.Next() })
	router.POST("/mcp/wiki/search", handler.HandleWikiSearch)

	payload := map[string]interface{}{"team_id": "team-1", "query": query, "limit": 10}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/mcp/wiki/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Results []struct {
			PageID string `json:"page_id"`
			Title  string `json:"title"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	ids := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		ids = append(ids, r.PageID)
	}
	return ids, len(resp.Results)
}

func TestHandleWikiSearchExcludesArchivedAndNonReadyPages(t *testing.T) {
	handler, handle := setupWikiSearchHandler(t)
	defer handle.teamDB.Close()
	defer handle.global.Close()

	seedWikiAsset(t, handle.teamDB, "asset-live", "ready", "page-live", "ready", "alpha live page", "alpha live content")
	seedWikiAsset(t, handle.teamDB, "asset-archived", "archived", "page-archived", "ready", "alpha archived page", "alpha archived content")
	seedWikiAsset(t, handle.teamDB, "asset-building", "ready", "page-building", "building", "alpha building page", "alpha building content")

	ids, _ := runWikiSearch(t, handler, "alpha")
	found := make(map[string]bool)
	for _, id := range ids {
		found[id] = true
	}
	if !found["page-live"] {
		t.Fatalf("ready page on live asset missing from results: %+v", ids)
	}
	if found["page-archived"] {
		t.Fatalf("page on archived asset leaked into results: %+v", ids)
	}
	if found["page-building"] {
		t.Fatalf("non-ready (building) page leaked into results: %+v", ids)
	}
}

// TestHandleWikiSearchDeleteOrphanRegression creates an archived asset with a
// wiki page, then deletes the asset row. The orphan page must not be returned
// through the FTS or LIKE paths.
func TestHandleWikiSearchDeleteOrphanRegression(t *testing.T) {
	handler, handle := setupWikiSearchHandler(t)
	defer handle.teamDB.Close()
	defer handle.global.Close()

	seedWikiAsset(t, handle.teamDB, "asset-live", "ready", "page-live", "ready", "alpha live page", "alpha live content")
	seedWikiAsset(t, handle.teamDB, "asset-gone", "archived", "page-gone", "ready", "zebra orphan page", "zebra orphan content")

	// Delete the asset row, leaving an orphan wiki page + stale FTS entry.
	// The team DB opens with foreign_keys ON, so the delete must run on a
	// connection that temporarily disables enforcement (an asset row may be
	// removed by a path that did not clean its wiki page first).
	conn, err := handle.teamDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys = OFF`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `DELETE FROM assets WHERE id='asset-gone'`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	conn.Close()

	ids, _ := runWikiSearch(t, handler, "zebra")
	for _, id := range ids {
		if id == "page-gone" {
			t.Fatalf("orphan wiki page of deleted asset returned by HandleWikiSearch: %+v", ids)
		}
	}
	// Live page still searchable through the same handler.
	ids, _ = runWikiSearch(t, handler, "alpha")
	found := make(map[string]bool)
	for _, id := range ids {
		found[id] = true
	}
	if !found["page-live"] {
		t.Fatalf("live page missing after orphan regression scenario: %+v", ids)
	}
}
