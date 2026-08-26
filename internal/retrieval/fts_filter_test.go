package retrieval

import (
	"context"
	"database/sql"
	"testing"

	"gateway/internal/acl"
)

func insertCandidateWithStatus(t *testing.T, database *sql.DB, id, team, visibility, summary string, status string) {
	t.Helper()
	_, err := database.Exec(`INSERT INTO assets
		(id, team_id, asset_type, name, slug, summary, source_event_ids, visibility, status, version, updated_at)
		VALUES (?, ?, 'l2', ?, ?, ?, '[]', ?, ?, 1, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		id, team, id, id, summary, visibility, status)
	if err != nil {
		t.Fatal(err)
	}
}

// TestSearchFTSExcludesArchivedAndDeprecatedAssets covers the LIKE fallback:
// archived/deprecated assets must never surface through the lexical path.
func TestSearchFTSExcludesArchivedAndDeprecatedAssets(t *testing.T) {
	database := retrievalDB(t)
	insertCandidate(t, database, "active", "team-1", acl.VisibilityTeam, "alpha project", testVector(0, 1))
	insertCandidateWithStatus(t, database, "archived", "team-1", acl.VisibilityTeam, "alpha archived", "archived")
	insertCandidateWithStatus(t, database, "deprecated", "team-1", acl.VisibilityTeam, "alpha deprecated", "deprecated")

	got, err := SearchFTS(context.Background(), database, "alpha", 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool)
	for _, candidate := range got {
		ids[candidate.ID] = true
	}
	if !ids["active"] {
		t.Fatalf("active asset was not returned by LIKE fallback: %#v", got)
	}
	if ids["archived"] || ids["deprecated"] {
		t.Fatalf("archived/deprecated asset leaked through SearchFTS LIKE fallback: %#v", got)
	}
}

func seedWikiFTSDB(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`CREATE TABLE wiki_pages (
		id TEXT PRIMARY KEY, asset_id TEXT NOT NULL UNIQUE REFERENCES assets(id), title TEXT NOT NULL,
		slug TEXT NOT NULL UNIQUE, content_md TEXT NOT NULL DEFAULT '', frontmatter_json TEXT DEFAULT '{}',
		status TEXT NOT NULL DEFAULT 'ready', built_at TEXT,
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE VIRTUAL TABLE wiki_fts USING fts5(page_id UNINDEXED, title, content)`); err != nil {
		t.Fatal(err)
	}
}

func insertWikiPage(t *testing.T, database *sql.DB, pageID, assetID, title, slug, content, status string) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO wiki_pages (id, asset_id, title, slug, content_md, status) VALUES (?,?,?,?,?,?)`,
		pageID, assetID, title, slug, content, status); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO wiki_fts (page_id, title, content) VALUES (?,?,?)`,
		pageID, title, content); err != nil {
		t.Fatal(err)
	}
}

// TestSearchFTSExcludesNonReadyAndArchivedWikiPages covers the FTS JOIN path:
// non-ready wiki pages and pages whose asset is archived/deprecated must not
// be returned.
func TestSearchFTSExcludesNonReadyAndArchivedWikiPages(t *testing.T) {
	database := retrievalDB(t)
	seedWikiFTSDB(t, database)
	// ready page on a live asset -> searchable
	insertCandidate(t, database, "asset-live", "team-1", acl.VisibilityTeam, "unrelated", testVector(0, 1))
	insertWikiPage(t, database, "page-live", "asset-live", "alpha live", "alpha-live", "alpha live content", "ready")
	// building page on a live asset -> excluded by wp.status
	insertCandidate(t, database, "asset-building", "team-1", acl.VisibilityTeam, "unrelated", testVector(0, 1))
	insertWikiPage(t, database, "page-building", "asset-building", "alpha building", "alpha-building", "alpha building content", "building")
	// ready page on an archived asset -> excluded by a.status
	insertCandidateWithStatus(t, database, "asset-archived", "team-1", acl.VisibilityTeam, "unrelated", "archived")
	insertWikiPage(t, database, "page-archived", "asset-archived", "alpha archived", "alpha-archived", "alpha archived content", "ready")
	// ready page on a deprecated asset -> excluded by a.status
	insertCandidateWithStatus(t, database, "asset-deprecated", "team-1", acl.VisibilityTeam, "unrelated", "deprecated")
	insertWikiPage(t, database, "page-deprecated", "asset-deprecated", "alpha deprecated", "alpha-deprecated", "alpha deprecated content", "ready")

	got, err := SearchFTS(context.Background(), database, "alpha", 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool)
	for _, candidate := range got {
		ids[candidate.ID] = true
	}
	if !ids["page-live"] {
		t.Fatalf("ready page on live asset missing from FTS results: %#v", got)
	}
	for _, leaked := range []string{"page-building", "page-archived", "page-deprecated"} {
		if ids[leaked] {
			t.Fatalf("excluded wiki page %s leaked through FTS JOIN: %#v", leaked, got)
		}
	}
}

// TestSearchFTSDeleteOrphanRegression is the delete-orphan regression test:
// create an archived asset with a wiki page, then delete the asset row. The
// orphan wiki page (and its stale FTS entry) must no longer be returned,
// because the FTS JOIN now requires a live assets row.
func TestSearchFTSDeleteOrphanRegression(t *testing.T) {
	database := retrievalDB(t)
	seedWikiFTSDB(t, database)
	insertCandidate(t, database, "asset-live", "team-1", acl.VisibilityTeam, "unrelated", testVector(0, 1))
	insertWikiPage(t, database, "page-live", "asset-live", "alpha live", "alpha-live", "alpha live content", "ready")
	// Orphan candidate: archived asset whose wiki page will outlive the row.
	insertCandidateWithStatus(t, database, "asset-gone", "team-1", acl.VisibilityTeam, "unrelated", "archived")
	insertWikiPage(t, database, "page-gone", "asset-gone", "zebra orphan", "zebra-orphan", "zebra orphan content", "ready")

	// Deleting the asset row leaves an orphan wiki page + stale FTS entry.
	if _, err := database.Exec(`DELETE FROM assets WHERE id='asset-gone'`); err != nil {
		t.Fatal(err)
	}

	// The orphan must not surface through the FTS path.
	got, err := SearchFTS(context.Background(), database, "zebra", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range got {
		if candidate.ID == "page-gone" {
			t.Fatalf("orphan wiki page of deleted asset returned by SearchFTS: %#v", got)
		}
	}
	// The live page must still be searchable.
	got, err = SearchFTS(context.Background(), database, "alpha", 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool)
	for _, candidate := range got {
		ids[candidate.ID] = true
	}
	if !ids["page-live"] {
		t.Fatalf("live wiki page disappeared after orphan regression scenario: %#v", got)
	}
}
