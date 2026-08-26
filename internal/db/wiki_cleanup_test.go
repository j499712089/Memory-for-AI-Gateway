package db

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func wikiCleanupDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:wiki-cleanup-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := EnsureAssetsSchema(database); err != nil {
		t.Fatalf("ensure assets schema: %v", err)
	}
	return database
}

func seedWikiCleanupFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := database.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, status)
		VALUES ('asset-live', 'team-1', 'wiki', 'live', 'live', 'ready')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, status)
		VALUES ('asset-gone', 'team-1', 'wiki', 'gone', 'gone', 'ready')`); err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertWikiPage(ctx, database, WikiPage{
		ID: "page-live", AssetID: "asset-live", Title: "Live", Slug: "live", ContentMD: "live content",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertWikiPage(ctx, database, WikiPage{
		ID: "page-gone", AssetID: "asset-gone", Title: "Gone", Slug: "gone", ContentMD: "gone content",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO wiki_fts (page_id, title, content) VALUES (?,?,?)`,
		"page-live", "Live", "live content"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO wiki_fts (page_id, title, content) VALUES (?,?,?)`,
		"page-gone", "Gone", "gone content"); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateWikiEdges(ctx, database, "page-live", []string{"gone"}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveWikiForAssetCleansPageEdgesAndFTS(t *testing.T) {
	database := wikiCleanupDB(t)
	seedWikiCleanupFixture(t, database)
	ctx := context.Background()

	if err := RemoveWikiForAsset(ctx, database, "asset-gone"); err != nil {
		t.Fatalf("remove wiki for asset: %v", err)
	}
	// wiki_pages row gone
	var pages int
	if err := database.QueryRow(`SELECT COUNT(*) FROM wiki_pages WHERE asset_id='asset-gone'`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if pages != 0 {
		t.Fatalf("wiki_pages row still present for removed asset: count=%d", pages)
	}
	// FTS entry gone
	var fts int
	if err := database.QueryRow(`SELECT COUNT(*) FROM wiki_fts WHERE page_id='page-gone'`).Scan(&fts); err != nil {
		t.Fatal(err)
	}
	if fts != 0 {
		t.Fatalf("wiki_fts entry still present for removed asset: count=%d", fts)
	}
	// edges referencing the removed page gone (both directions)
	var edges int
	if err := database.QueryRow(`SELECT COUNT(*) FROM wiki_edges WHERE from_page_id='page-gone' OR to_page_id='page-gone'`).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges != 0 {
		t.Fatalf("wiki_edges referencing removed page still present: count=%d", edges)
	}
	// live page untouched
	var live int
	if err := database.QueryRow(`SELECT COUNT(*) FROM wiki_pages WHERE asset_id='asset-live'`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("live wiki page was removed by cleanup: count=%d", live)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM wiki_fts WHERE page_id='page-live'`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("live wiki fts entry was removed by cleanup: count=%d", live)
	}
}

func TestRemoveWikiForAssetIsIdempotentAndToleratesMissingPage(t *testing.T) {
	database := wikiCleanupDB(t)
	seedWikiCleanupFixture(t, database)
	ctx := context.Background()

	if err := RemoveWikiForAsset(ctx, database, "asset-gone"); err != nil {
		t.Fatalf("first remove: %v", err)
	}
	// Second call is a no-op, not an error.
	if err := RemoveWikiForAsset(ctx, database, "asset-gone"); err != nil {
		t.Fatalf("second remove should be idempotent: %v", err)
	}
	// Asset with no wiki page is a no-op.
	if err := RemoveWikiForAsset(ctx, database, "does-not-exist"); err != nil {
		t.Fatalf("remove with missing asset: %v", err)
	}
}

// TestRemoveWikiForAssetRunsInsideTransaction proves the cleanup can execute
// on a *sql.Tx, so DeprecateSkill can roll the wiki removal back together
// with the status updates when a later step fails.
func TestRemoveWikiForAssetRunsInsideTransaction(t *testing.T) {
	database := wikiCleanupDB(t)
	seedWikiCleanupFixture(t, database)
	ctx := context.Background()

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := RemoveWikiForAsset(ctx, tx, "asset-gone"); err != nil {
		t.Fatalf("remove wiki for asset inside tx: %v", err)
	}
	// Uncommitted: the page is still visible to a second connection only if
	// committed, but within the same transaction the delete is visible.
	var pages int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM wiki_pages WHERE asset_id='asset-gone'`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if pages != 0 {
		t.Fatalf("wiki_pages row still present after tx-scoped removal: count=%d", pages)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
