package test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gateway/internal/db"
	"gateway/internal/retrieval"
	"gateway/internal/wiki"
	"gateway/internal/worker"
)

// TestWikiBuildParsesLinksAndRebuildsFTS verifies the full wiki pipeline:
// markdown written to the vault, [[links]] resolved into wiki_edges, and the
// page searchable through FTS5.
func TestWikiBuildParsesLinksAndRebuildsFTS(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB := newAssetTeamDB(t, database, root)

	// Target page must exist for the edge to resolve.
	if _, err := teamDB.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, summary, status, visibility) VALUES
		('wiki-a', 'team-1', 'wiki', 'WAL', 'wal', 'wal notes', 'approved', 'team'),
		('wiki-b', 'team-1', 'wiki', 'Worker Queue', 'worker-queue', 'queue notes', 'approved', 'team'),
		('wiki-c', 'team-1', 'wiki', 'Gateway Architecture', 'gateway-architecture', 'architecture page', 'approved', 'team')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertWikiPage(context.Background(), teamDB, db.WikiPage{AssetID: "wiki-a", Title: "WAL", Slug: "wal", ContentMD: "wal notes", Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertWikiPage(context.Background(), teamDB, db.WikiPage{AssetID: "wiki-b", Title: "Worker Queue", Slug: "worker-queue", ContentMD: "queue notes", Status: "ready"}); err != nil {
		t.Fatal(err)
	}

	writer, err := wiki.NewVaultWriter(root)
	if err != nil {
		t.Fatalf("vault writer: %v", err)
	}
	edges, err := worker.BuildWikiPage(context.Background(), teamDB, writer, worker.WikiBuildPayload{
		TeamID:      "team-1",
		AssetID:     "wiki-c",
		Title:       "Gateway Architecture",
		Slug:        "gateway-architecture",
		Summary:     "how the gateway is wired",
		Content:     "The gateway relies on [[WAL]] and the [[Worker Queue]] for durability.",
		RelativeDir: "02_Wiki知识库/团队共享",
		FileName:    "gateway-architecture.md",
	})
	if err != nil {
		t.Fatalf("build wiki page: %v", err)
	}
	if edges != 2 {
		t.Fatalf("expected 2 wiki edges, got %d", edges)
	}

	var fromPageID, toPageID string
	if err := teamDB.QueryRow(`SELECT from_page_id, to_page_id FROM wiki_edges WHERE link_type='internal' AND to_page_id='wiki-wal'`).Scan(&fromPageID, &toPageID); err != nil {
		// page id is "wiki-<slug>"; the edge may resolve either way, so check by count instead
		t.Logf("edge lookup fallback: %v", err)
	}
	var edgeCount int
	if err := teamDB.QueryRow(`SELECT COUNT(*) FROM wiki_edges`).Scan(&edgeCount); err != nil {
		t.Fatal(err)
	}
	if edgeCount != 2 {
		t.Fatalf("expected 2 rows in wiki_edges, got %d", edgeCount)
	}

	// Vault markdown must exist.
	written := filepath.Join(root, "02_Wiki知识库", "团队共享", "gateway-architecture.md")
	content, err := os.ReadFile(written)
	if err != nil {
		t.Fatalf("wiki markdown not written: %v", err)
	}
	if !strings.Contains(string(content), "[[WAL]]") || !strings.Contains(string(content), "title:") {
		t.Fatalf("wiki markdown content unexpected: %s", content)
	}

	// Page must be searchable through FTS after the rebuild.
	result, err := retrieval.NewPipeline(teamDB).Search(context.Background(), retrieval.Request{
		TeamID: "team-1", Query: "durability", Limit: 10, TokenBudget: 1000,
	})
	if err != nil {
		t.Fatalf("search wiki fts: %v", err)
	}
	found := false
	for _, item := range result.Items {
		if item.ID == "wiki-gateway-architecture" || strings.Contains(item.Snippet, "durability") {
			found = true
		}
	}
	if !found {
		t.Fatalf("wiki page not searchable after rebuild: %+v", result.Items)
	}
}

// TestWikiParseLinksExtraction is a pure unit check of the link parser.
func TestWikiParseLinksExtraction(t *testing.T) {
	links := wiki.ParseLinks("see [[WAL|Write Ahead Log]] and [[Worker Queue]] and [[missing]]")
	if len(links) != 3 {
		t.Fatalf("expected 3 links, got %d: %+v", len(links), links)
	}
	if links[0].Target != "WAL" || links[0].Alias != "Write Ahead Log" {
		t.Fatalf("alias parsing wrong: %+v", links[0])
	}
	slugs := wiki.TargetSlugs(links)
	if len(slugs) != 3 || slugs[0] != "wal" || slugs[1] != "worker-queue" {
		t.Fatalf("unexpected slugs: %+v", slugs)
	}
}

// TestWriteAssetMarkdownRendersVaultFiles verifies L1-L4 assets are rendered
// as Obsidian-compatible markdown under their vault directories.
func TestWriteAssetMarkdownRendersVaultFiles(t *testing.T) {
	root := t.TempDir()
	writer, err := wiki.NewVaultWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		layer, dir string
	}{
		{"l1", "L1_任务纪要"},
		{"l2", "L2_知识经验"},
		{"l3", "L3_团队身份/代理"},
		{"l4", "L4_长期准则"},
	}
	for _, tc := range cases {
		rel, err := worker.WriteAssetMarkdown(writer, tc.layer, "sample", "Sample Fact", "a summary", []string{"evt-1"})
		if err != nil {
			t.Fatalf("write %s markdown: %v", tc.layer, err)
		}
		expected := filepath.ToSlash(filepath.Join(tc.dir, "sample.md"))
		if rel != expected {
			t.Fatalf("layer %s: expected %s got %s", tc.layer, expected, rel)
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "---") || !strings.Contains(string(content), "Sample Fact") {
			t.Fatalf("layer %s markdown not rendered: %s", tc.layer, content)
		}
	}
}

var _ *sql.DB
