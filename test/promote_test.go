package test

import (
	"context"
	"testing"

	"gateway/internal/db"
	"gateway/internal/worker"
)

// TestPromoteL2EvidenceGate verifies the >=2 distinct evidence gate.
func TestPromoteL2EvidenceGate(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB := newAssetTeamDB(t, database, root)

	base := worker.PromotePayload{
		TeamID: "team-1", AgentID: "agent-1", Layer: "l2",
		Name: "Project Retro", Slug: "project-retro", Summary: "retro findings", Confidence: 0.8,
	}
	// One explicit event -> gate not met.
	base.SourceEventIDs = []string{"evt-1"}
	if _, err := worker.PromoteL2(context.Background(), teamDB, base); err == nil {
		t.Fatal("expected promotion to be rejected with a single evidence")
	}
	// Two distinct events -> gate met.
	base.SourceEventIDs = []string{"evt-1", "evt-2"}
	asset, err := worker.PromoteL2(context.Background(), teamDB, base)
	if err != nil {
		t.Fatalf("promote l2: %v", err)
	}
	if asset.AssetType != "l2" || asset.Status != "promoted" {
		t.Fatalf("unexpected promoted asset: %+v", asset)
	}
	// Evidence can also come from existing L1 assets sharing the slug.
	_, err = teamDB.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, summary, source_event_ids, confidence, status, visibility) VALUES ('l1-a', 'team-1', 'l1', 'x', 'project-retro', 'l1', '["evt-3"]', 0.7, 'candidate', 'team')`)
	if err != nil {
		t.Fatal(err)
	}
	count, err := worker.EvidenceCount(context.Background(), teamDB, "team-1", "project-retro", []string{"evt-1"})
	if err != nil {
		t.Fatal(err)
	}
	if count < 2 {
		t.Fatalf("expected existing l1 evidence to count, got %d", count)
	}
}

// TestPromoteL3UpdatesIdentityCard verifies L3 promotion bumps the identity
// card version instead of mutating it in place.
func TestPromoteL3UpdatesIdentityCard(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	seedTeam(t, database)
	teamDB := newAssetTeamDB(t, database, root)

	payload := worker.PromotePayload{
		TeamID: "team-1", AgentID: "agent-1", Layer: "l3", IdentityCardID: "card-1",
		Slug: "backend-owner", Name: "Backend Owner",
		Summary: "owns gateway backend", Confidence: 0.95,
		SourceEventIDs:       []string{"evt-1", "evt-2"},
		CardRole:             "Backend Engineer",
		CardResponsibilities: "gateway, worker queue, sqlite",
		CardBoundaries:       "no frontend changes",
		CardAllowedTools:     []string{"go", "git"},
	}
	if _, err := worker.PromoteL3(context.Background(), database.Global, teamDB, payload); err != nil {
		t.Fatalf("promote l3: %v", err)
	}
	card, err := db.GetIdentityCard(context.Background(), database.Global, "card-1")
	if err != nil {
		t.Fatal(err)
	}
	if card.Version != 1 || card.Role != "Backend Engineer" || len(card.AllowedTools) != 2 {
		t.Fatalf("unexpected identity card: %+v", card)
	}
	// Second promotion with new evidence bumps the version.
	payload.Summary = "owns gateway backend and mcp"
	payload.SourceEventIDs = []string{"evt-3", "evt-4"}
	if _, err := worker.PromoteL3(context.Background(), database.Global, teamDB, payload); err != nil {
		t.Fatalf("promote l3 again: %v", err)
	}
	card, err = db.GetIdentityCard(context.Background(), database.Global, "card-1")
	if err != nil {
		t.Fatal(err)
	}
	if card.Version != 2 {
		t.Fatalf("expected identity card version 2, got %d", card.Version)
	}
}

// TestPromoteL4VersionConflictCreatesNewVersion verifies L4 conflicts never
// overwrite: the second promotion creates version 2 of the same slug.
func TestPromoteL4VersionConflictCreatesNewVersion(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB := newAssetTeamDB(t, database, root)

	payload := worker.PromotePayload{
		TeamID: "team-1", AgentID: "agent-1", Layer: "l4",
		Slug: "no-secrets-in-markdown", Name: "No Secrets",
		Summary: "never write secrets to markdown", Confidence: 1.0,
		SourceEventIDs: []string{"evt-1", "evt-2"},
	}
	first, err := worker.PromoteL4(context.Background(), teamDB, payload)
	if err != nil {
		t.Fatalf("promote l4 v1: %v", err)
	}
	if first.Version != 1 {
		t.Fatalf("expected version 1, got %d", first.Version)
	}
	// Conflict: same slug, different conclusion.
	payload.Summary = "never write secrets to markdown or logs"
	payload.SourceEventIDs = []string{"evt-3", "evt-4"}
	second, err := worker.PromoteL4(context.Background(), teamDB, payload)
	if err != nil {
		t.Fatalf("promote l4 v2: %v", err)
	}
	if second.Version != 2 {
		t.Fatalf("expected version 2 on conflict, got %d", second.Version)
	}
	versions, err := db.ListAssetsBySlug(context.Background(), teamDB, "team-1", "l4", "no-secrets-in-markdown")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions preserved, got %d", len(versions))
	}
}
