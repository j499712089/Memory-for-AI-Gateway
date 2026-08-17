package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"gateway/internal/db"
	"gateway/internal/skill"
	"gateway/internal/wiki"
)

// SkillReviewPayload carries a skill candidate plus the review action.
type SkillReviewPayload struct {
	TeamID  string      `json:"team_id"`
	AgentID string      `json:"agent_id"`
	Action  string      `json:"action"` // approve | deprecate
	Skill   skill.Skill `json:"skill"`
}

// EnqueueSkillReview queues a skill_review job for a candidate skill.
func EnqueueSkillReview(ctx context.Context, queue *Queue, payload SkillReviewPayload) (string, error) {
	if queue == nil {
		return "", fmt.Errorf("queue is nil")
	}
	if payload.Action != "approve" && payload.Action != "deprecate" && payload.Action != "candidate" {
		return "", fmt.Errorf("skill review action must be candidate, approve or deprecate")
	}
	return enqueueUniqueJob(ctx, queue, Job{
		Queue:        "skill_review",
		TeamID:       payload.TeamID,
		AgentID:      payload.AgentID,
		AssetID:      payload.Skill.Name,
		AssetType:    "skill",
		PartitionKey: "skill:" + payload.TeamID + ":" + payload.Skill.Name,
		Payload:      payload,
	})
}

// RecordSkillCandidate stores a newly extracted skill as a reviewable
// candidate without approving it. This keeps automatic extraction separate
// from the human/agent approval transition while still making the candidate
// visible to the skill_review queue and vault.
func RecordSkillCandidate(ctx context.Context, teamDB *sql.DB, teamID string, candidate skill.Skill, createdBy string) (db.Skill, error) {
	if teamDB == nil {
		return db.Skill{}, fmt.Errorf("team database is nil")
	}
	if candidate.Status == "" {
		candidate.Status = "candidate"
	}
	if candidate.Scope == "" {
		candidate.Scope = "team"
	}
	if candidate.Version == "" {
		candidate.Version = "0.1.0"
	}
	if candidate.DisplayName == "" {
		candidate.DisplayName = candidate.Name
	}
	if err := candidate.Validate(); err != nil {
		return db.Skill{}, fmt.Errorf("skill candidate validation failed: %w", err)
	}

	existing, err := db.GetSkillByName(ctx, teamDB, teamID, candidate.Name)
	if err != nil && err != sql.ErrNoRows {
		return db.Skill{}, err
	}
	assetID := existing.AssetID
	if assetID == "" {
		assetID = "skill-asset-" + candidate.Name
		if _, assetErr := db.GetAsset(ctx, teamDB, teamID, assetID); errors.Is(assetErr, sql.ErrNoRows) {
			if err := db.CreateAsset(ctx, teamDB, db.Asset{
				ID: assetID, TeamID: teamID, AssetType: "skill", Name: candidate.DisplayName,
				Slug: candidate.Name, Summary: candidate.TriggerBoundary, SourceEventIDs: candidate.SourceIDs,
				Confidence: 0.6, Status: "candidate", Visibility: "team", Version: 1,
			}, createdBy); err != nil {
				return db.Skill{}, fmt.Errorf("create skill candidate asset: %w", err)
			}
		} else if assetErr != nil {
			return db.Skill{}, fmt.Errorf("check skill candidate asset: %w", assetErr)
		}
	}
	steps := make([]string, 0, len(candidate.Steps))
	for _, step := range candidate.Steps {
		steps = append(steps, step.Title+": "+step.Body)
	}
	validation := map[string]any{"pass_criteria": candidate.Validation.PassCriteria}
	stored, err := db.UpsertSkill(ctx, teamDB, db.Skill{
		ID: existing.ID, AssetID: assetID, Name: candidate.Name, DisplayName: candidate.DisplayName,
		Version: candidate.Version, Status: "candidate", Scope: candidate.Scope,
		TriggerBoundary: candidate.TriggerBoundary, Steps: steps, Validation: validation,
		SourceIDs: candidate.SourceIDs, ResourceRefs: candidate.ResourceRefs,
		Entrypoint: candidate.Entrypoint, ManifestPath: candidate.ManifestPath,
	}, createdBy)
	if err != nil {
		return db.Skill{}, fmt.Errorf("upsert skill candidate: %w", err)
	}
	if err := db.CreateSkillVersion(ctx, teamDB, db.SkillVersion{
		ID: "sv-" + stored.ID + "-" + stored.Version + "-candidate", SkillID: stored.ID,
		Version: stored.Version, Status: "candidate", ContentRef: stored.ManifestPath,
		SourceIDs: stored.SourceIDs, CreatedBy: createdBy,
	}); err != nil {
		return db.Skill{}, fmt.Errorf("record skill candidate version: %w", err)
	}
	return stored, nil
}

// ApproveSkill validates a candidate, promotes it to approved, and records an
// immutable SemVer version. Re-approving changed content bumps the patch
// version instead of overwriting the prior version.
func ApproveSkill(ctx context.Context, teamDB *sql.DB, teamID string, candidate skill.Skill, createdBy string) (db.Skill, error) {
	if teamDB == nil {
		return db.Skill{}, fmt.Errorf("team database is nil")
	}
	if err := candidate.Validate(); err != nil {
		return db.Skill{}, fmt.Errorf("skill validation failed: %w", err)
	}
	version := candidate.Version
	existing, err := db.GetSkillByName(ctx, teamDB, "", candidate.Name)
	if err != nil && err != sql.ErrNoRows {
		return db.Skill{}, err
	}
	assetID := existing.AssetID
	if assetID == "" {
		assetID = "skill-asset-" + candidate.Name
		if err := db.CreateAsset(ctx, teamDB, db.Asset{
			ID:             assetID,
			TeamID:         teamID,
			IdentityCardID: "",
			AssetType:      "skill",
			Name:           candidate.Name,
			Slug:           candidate.Name,
			Summary:        candidate.TriggerBoundary,
			SourceEventIDs: candidate.SourceIDs,
			Confidence:     0.9,
			Status:         "approved",
			Visibility:     "team",
			Version:        1,
		}, createdBy); err != nil {
			return db.Skill{}, fmt.Errorf("create skill asset: %w", err)
		}
	}
	// Content changed since the last approved version -> bump SemVer.
	if existing.ID != "" && existing.Status == "approved" && existing.Version != "" {
		next, bumpErr := skill.NextVersion(existing.Version, "patch")
		if bumpErr != nil {
			return db.Skill{}, bumpErr
		}
		version = next
	}
	steps := make([]string, 0, len(candidate.Steps))
	for _, step := range candidate.Steps {
		steps = append(steps, step.Title+": "+step.Body)
	}
	stored, err := db.UpsertSkill(ctx, teamDB, db.Skill{
		ID:              existing.ID,
		AssetID:         assetID,
		Name:            candidate.Name,
		DisplayName:     candidate.DisplayName,
		Version:         version,
		Status:          "approved",
		Scope:           candidate.Scope,
		TriggerBoundary: candidate.TriggerBoundary,
		Steps:           steps,
		Validation:      map[string]any{},
		SourceIDs:       candidate.SourceIDs,
		ResourceRefs:    candidate.ResourceRefs,
		Entrypoint:      candidate.Entrypoint,
		ManifestPath:    candidate.ManifestPath,
	}, createdBy)
	if err != nil {
		return db.Skill{}, fmt.Errorf("upsert approved skill: %w", err)
	}
	if stored.Version != version {
		if err := SetSkillVersion(ctx, teamDB, stored.ID, version); err != nil {
			return db.Skill{}, err
		}
		stored.Version = version
	}
	if err := db.CreateSkillVersion(ctx, teamDB, db.SkillVersion{
		ID:         "sv-" + stored.ID + "-" + version,
		SkillID:    stored.ID,
		Version:    version,
		Status:     "approved",
		ContentRef: stored.ManifestPath,
		SourceIDs:  candidate.SourceIDs,
		CreatedBy:  createdBy,
	}); err != nil {
		return db.Skill{}, fmt.Errorf("record skill version: %w", err)
	}
	return stored, nil
}

// DeprecateSkill flips an approved skill to deprecated (terminal state for
// normal lifecycle; no content mutation).
func DeprecateSkill(ctx context.Context, teamDB *sql.DB, name string) (db.Skill, error) {
	if teamDB == nil {
		return db.Skill{}, fmt.Errorf("team database is nil")
	}
	existing, err := db.GetSkillByName(ctx, teamDB, "", name)
	if err != nil {
		return db.Skill{}, err
	}
	if !skill.ValidTransition(existing.Status, "deprecated") {
		return db.Skill{}, fmt.Errorf("invalid transition %s -> deprecated", existing.Status)
	}
	_, err = teamDB.ExecContext(ctx, `UPDATE skills SET status='deprecated', updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, existing.ID)
	if err != nil {
		return db.Skill{}, err
	}
	existing.Status = "deprecated"
	_, err = teamDB.ExecContext(ctx, `UPDATE assets SET status='deprecated', updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, existing.AssetID)
	if err != nil {
		return db.Skill{}, err
	}
	if err := db.CreateSkillVersion(ctx, teamDB, db.SkillVersion{
		ID:         "sv-" + existing.ID + "-" + existing.Version + "-deprecated",
		SkillID:    existing.ID,
		Version:    existing.Version,
		Status:     "deprecated",
		ContentRef: existing.ManifestPath,
		SourceIDs:  existing.SourceIDs,
	}); err != nil {
		return db.Skill{}, err
	}
	return existing, nil
}

func registerSkillReview(processor *Processor, deps AssetWorkerDeps) {
	processor.Register("skill_review", func(ctx context.Context, claim *Claim) error {
		var payload SkillReviewPayload
		if err := json.Unmarshal([]byte(claim.PayloadJSON), &payload); err != nil {
			return fmt.Errorf("skill_review payload: %w", err)
		}
		if payload.TeamID == "" {
			return fmt.Errorf("skill review team id is required")
		}
		teamDB, err := deps.resolveTeamDB(payload.TeamID)
		if err != nil {
			return err
		}
		defer teamDB.Close()
		var writer *wiki.VaultWriter
		if deps.VaultPath != "" {
			writer, err = wiki.NewVaultWriter(deps.VaultPath)
			if err != nil {
				return err
			}
		}
		switch payload.Action {
		case "candidate":
			_, err = RecordSkillCandidate(ctx, teamDB, payload.TeamID, payload.Skill, payload.AgentID)
		case "approve":
			_, err = ApproveSkill(ctx, teamDB, payload.TeamID, payload.Skill, payload.AgentID)
		case "deprecate":
			_, err = DeprecateSkill(ctx, teamDB, payload.Skill.Name)
		default:
			err = fmt.Errorf("unknown skill review action %q", payload.Action)
		}
		if err != nil {
			return err
		}
		if payload.Action == "approve" || payload.Action == "candidate" {
			_, err = WriteAssetMarkdown(writer, "skill", payload.Skill.Name, payload.Skill.DisplayName, payload.Skill.TriggerBoundary, payload.Skill.SourceIDs)
		}
		return err
	})
}

// SetSkillVersion updates the active SemVer of a skills row. Kept here so the
// worker layer owns the version transition while the db layer stays a thin
// store; declared in the worker package, implemented in db (see assets_repo).
func SetSkillVersion(ctx context.Context, teamDB *sql.DB, skillID, version string) error {
	if teamDB == nil {
		return fmt.Errorf("team database is nil")
	}
	result, err := teamDB.ExecContext(ctx, `UPDATE skills SET version=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, version, skillID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("skill %s not found for version update", skillID)
	}
	return nil
}
