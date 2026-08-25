package test

import (
	"context"
	"testing"

	"gateway/internal/skill"
	"gateway/internal/worker"
)

func sampleSkill() skill.Skill {
	return skill.Skill{
		Name:            "review-skill",
		DisplayName:     "Review Skill",
		Version:         "1.0.0",
		Status:          "candidate",
		Scope:           "team",
		TriggerBoundary: "trigger when a review comment arrives",
		Steps:           []skill.Step{{Order: 1, Title: "Collect", Body: "gather evidence"}},
		SourceIDs:       []string{"evt-1", "evt-2"},
	}
}

// TestSkillReviewCandidateToApproved verifies the candidate -> approved flow
// and records an immutable SemVer version in skill_versions.
func TestSkillReviewCandidateToApproved(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB := newAssetTeamDB(t, database, root)

	stored, err := worker.ApproveSkill(context.Background(), teamDB, "team-1", sampleSkill(), "agent-1")
	if err != nil {
		t.Fatalf("approve skill: %v", err)
	}
	if stored.Status != "approved" || stored.Version != "1.0.0" {
		t.Fatalf("unexpected approved skill: %+v", stored)
	}
	var versionCount int
	if err := teamDB.QueryRow(`SELECT COUNT(*) FROM skill_versions WHERE skill_id=? AND version='1.0.0' AND status='approved'`, stored.ID).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if versionCount != 1 {
		t.Fatalf("skill version 1.0.0 not recorded: %d", versionCount)
	}
	var assetStatus string
	if err := teamDB.QueryRow(`SELECT status FROM assets WHERE id=?`, stored.AssetID).Scan(&assetStatus); err != nil {
		t.Fatal(err)
	}
	if assetStatus != "approved" {
		t.Fatalf("skill asset not approved: %s", assetStatus)
	}
}

// TestSkillReviewSemVerBumpOnContentChange verifies re-approving changed
// content bumps the patch version instead of overwriting the prior version.
func TestSkillReviewSemVerBumpOnContentChange(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB := newAssetTeamDB(t, database, root)

	if _, err := worker.ApproveSkill(context.Background(), teamDB, "team-1", sampleSkill(), "agent-1"); err != nil {
		t.Fatalf("approve v1: %v", err)
	}
	changed := sampleSkill()
	changed.Steps = append(changed.Steps, skill.Step{Order: 2, Title: "Verify", Body: "run verification"})
	stored, err := worker.ApproveSkill(context.Background(), teamDB, "team-1", changed, "agent-1")
	if err != nil {
		t.Fatalf("approve v2: %v", err)
	}
	if stored.Version != "1.0.1" {
		t.Fatalf("expected patched version 1.0.1, got %s", stored.Version)
	}
	var versions int
	if err := teamDB.QueryRow(`SELECT COUNT(*) FROM skill_versions WHERE skill_id=?`, stored.ID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("expected 2 immutable versions, got %d", versions)
	}
}

// TestSkillReviewProcessorRunEndToEnd verifies the skill_review queue handler
// is registered and processes a job via the processor framework.
func TestSkillReviewProcessorRunEndToEnd(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	seedTeam(t, database)
	teamDB := newAssetTeamDB(t, database, root)

	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	if err := worker.RegisterAssetWorkers(processor, worker.AssetWorkerDeps{GlobalDB: database.Global, MemoryRoot: root}); err != nil {
		t.Fatalf("register asset workers: %v", err)
	}
	if _, err := worker.EnqueueSkillReview(context.Background(), queue, worker.SkillReviewPayload{
		TeamID: "team-1", AgentID: "agent-1", Action: "approve", Skill: sampleSkill(),
	}); err != nil {
		t.Fatalf("enqueue skill review: %v", err)
	}
	processed, err := processor.ProcessOnce(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("process skill review: %v", err)
	}
	if !processed {
		t.Fatal("expected a job to be processed")
	}
	var status string
	if err := teamDB.QueryRow(`SELECT status FROM skills WHERE name='review-skill'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "approved" {
		t.Fatalf("skill was not approved through the worker: %s", status)
	}
}

// TestSkillDeprecateLifecycle verifies approved -> deprecated transition.
func TestSkillDeprecateLifecycle(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB := newAssetTeamDB(t, database, root)

	if _, err := worker.ApproveSkill(context.Background(), teamDB, "team-1", sampleSkill(), "agent-1"); err != nil {
		t.Fatal(err)
	}
	deprecated, err := worker.DeprecateSkill(context.Background(), teamDB, "review-skill")
	if err != nil {
		t.Fatalf("deprecate skill: %v", err)
	}
	if deprecated.Status != "deprecated" {
		t.Fatalf("expected deprecated, got %s", deprecated.Status)
	}
}
