package worker

import (
	"context"
	"database/sql"
	"testing"

	"gateway/internal/db"

	_ "modernc.org/sqlite"
)

// skillReviewDB builds a team database with the asset + wiki + skill schema
// required by DeprecateSkill.
func skillReviewDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:skill-review-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.EnsureAssetsSchema(database); err != nil {
		t.Fatalf("ensure assets schema: %v", err)
	}
	if err := db.EnsureAssetSubTracksSchema(database); err != nil {
		t.Fatalf("ensure asset sub-track schema: %v", err)
	}
	return database
}

// seedApprovedSkill creates an approved skill with its asset and a wiki page.
func seedApprovedSkill(t *testing.T, database *sql.DB, name string) {
	t.Helper()
	ctx := context.Background()
	if err := db.CreateAsset(ctx, database, db.Asset{
		ID:         "asset-" + name,
		TeamID:     "team-1",
		AssetType:  "skill",
		Name:       name,
		Slug:       name,
		Summary:    "trigger boundary",
		Confidence: 0.9,
		Status:     "approved",
		Visibility: "team",
		Version:    1,
	}, "tester"); err != nil {
		t.Fatalf("create skill asset: %v", err)
	}
	if _, err := db.UpsertSkill(ctx, database, db.Skill{
		Name:         name,
		AssetID:      "asset-" + name,
		DisplayName:  name,
		Version:      "1.0.0",
		Status:       "approved",
		Scope:        "team",
		ManifestPath: "skills/" + name + ".md",
	}, "tester"); err != nil {
		t.Fatalf("upsert skill: %v", err)
	}
	if _, err := db.UpsertWikiPage(ctx, database, db.WikiPage{
		ID: "page-" + name, AssetID: "asset-" + name, Title: name, Slug: name, ContentMD: name + " content",
	}); err != nil {
		t.Fatalf("upsert wiki page: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO wiki_fts (page_id, title, content) VALUES (?,?,?)`,
		"page-"+name, name, name+" content"); err != nil {
		t.Fatalf("insert wiki fts: %v", err)
	}
}

func skillStatus(t *testing.T, database *sql.DB, name string) string {
	t.Helper()
	var status string
	if err := database.QueryRow(`SELECT status FROM skills WHERE name=?`, name).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func assetStatus(t *testing.T, database *sql.DB, assetID string) string {
	t.Helper()
	var status string
	if err := database.QueryRow(`SELECT status FROM assets WHERE id=?`, assetID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func wikiPageCount(t *testing.T, database *sql.DB, assetID string) int {
	t.Helper()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM wiki_pages WHERE asset_id=?`, assetID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestDeprecateSkillCompletesAllSteps proves the happy path: skills status,
// asset status, wiki cleanup and version record all land together.
func TestDeprecateSkillCompletesAllSteps(t *testing.T) {
	database := skillReviewDB(t)
	seedApprovedSkill(t, database, "alpha")
	ctx := context.Background()

	got, err := DeprecateSkill(ctx, database, "alpha")
	if err != nil {
		t.Fatalf("deprecate skill: %v", err)
	}
	if got.Status != "deprecated" {
		t.Fatalf("returned skill status = %q, want deprecated", got.Status)
	}
	if status := skillStatus(t, database, "alpha"); status != "deprecated" {
		t.Fatalf("skills.status = %q, want deprecated", status)
	}
	if status := assetStatus(t, database, "asset-alpha"); status != "deprecated" {
		t.Fatalf("assets.status = %q, want deprecated", status)
	}
	if count := wikiPageCount(t, database, "asset-alpha"); count != 0 {
		t.Fatalf("wiki page for deprecated asset still present: count=%d", count)
	}
	var fts int
	if err := database.QueryRow(`SELECT COUNT(*) FROM wiki_fts WHERE page_id='page-alpha'`).Scan(&fts); err != nil {
		t.Fatal(err)
	}
	if fts != 0 {
		t.Fatalf("wiki fts entry for deprecated asset still present: count=%d", fts)
	}
	var versions int
	if err := database.QueryRow(`SELECT COUNT(*) FROM skill_versions WHERE skill_id=? AND status='deprecated'`, got.ID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 1 {
		t.Fatalf("deprecated skill version record missing: count=%d", versions)
	}
}

// TestDeprecateSkillRollsBackOnWikiCleanupFailure forces the wiki cleanup to
// fail after the status updates, then verifies the whole transaction rolled
// back: the skill stays approved, the asset stays approved, and no deprecated
// version is recorded.
func TestDeprecateSkillRollsBackOnWikiCleanupFailure(t *testing.T) {
	database := skillReviewDB(t)
	seedApprovedSkill(t, database, "alpha")
	ctx := context.Background()

	// Break the wiki_fts table so RemoveWikiForAsset fails after the skills
	// and assets status updates have already run inside the transaction.
	if _, err := database.Exec(`DROP TABLE wiki_fts`); err != nil {
		t.Fatalf("drop wiki_fts: %v", err)
	}

	if _, err := DeprecateSkill(ctx, database, "alpha"); err == nil {
		t.Fatal("expected deprecate to fail when wiki cleanup fails")
	}
	if status := skillStatus(t, database, "alpha"); status != "approved" {
		t.Fatalf("skills.status = %q after failed deprecate, want approved (rollback)", status)
	}
	if status := assetStatus(t, database, "asset-alpha"); status != "approved" {
		t.Fatalf("assets.status = %q after failed deprecate, want approved (rollback)", status)
	}
	// The wiki page must survive the rollback.
	if count := wikiPageCount(t, database, "asset-alpha"); count != 1 {
		t.Fatalf("wiki page was not rolled back: count=%d", count)
	}
	var versions int
	if err := database.QueryRow(`SELECT COUNT(*) FROM skill_versions WHERE skill_id='skill-alpha' AND status='deprecated'`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 0 {
		t.Fatalf("deprecated version recorded despite rollback: count=%d", versions)
	}
}
