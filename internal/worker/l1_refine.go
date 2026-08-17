package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gateway/internal/codegraph"
	"gateway/internal/db"
	"gateway/internal/idgen"
	"gateway/internal/wiki"
)

// AssetWorkerDeps bundles the dependencies shared by the six Phase 3b asset
// sub-track handlers. A nil TeamDB resolver falls back to opening the team
// database under <memoryRoot>/teams/<teamID>/memory.db.
type AssetWorkerDeps struct {
	GlobalDB   *sql.DB
	TeamDB     func(teamID string) (*sql.DB, error)
	MemoryRoot string
	VaultPath  string
	Parser     codegraph.Parser
	Now        func() time.Time
}

// resolveTeamDB opens (or re-opens) the team database for a team.
func (d AssetWorkerDeps) resolveTeamDB(teamID string) (*sql.DB, error) {
	if strings.TrimSpace(teamID) == "" {
		return nil, fmt.Errorf("team id is required")
	}
	if d.TeamDB != nil {
		return d.TeamDB(teamID)
	}
	if d.MemoryRoot == "" {
		return nil, fmt.Errorf("memory root is required to resolve team database")
	}
	teamDB, err := db.OpenTeamDB(d.MemoryRoot+"/teams", teamID)
	if err != nil {
		return nil, err
	}
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		teamDB.Close()
		return nil, fmt.Errorf("ensure assets schema: %w", err)
	}
	if err := db.EnsureAssetSubTracksSchema(teamDB); err != nil {
		teamDB.Close()
		return nil, fmt.Errorf("ensure asset sub-track schema: %w", err)
	}
	return teamDB, nil
}

func (d AssetWorkerDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// RegisterAssetWorkers wires the six Phase 3b sub-track handlers into a Phase
// 3a processor. Calling it twice replaces the handlers idempotently.
func RegisterAssetWorkers(processor *Processor, deps AssetWorkerDeps) error {
	if processor == nil {
		return fmt.Errorf("processor is nil")
	}
	if processor.Queue == nil {
		return fmt.Errorf("processor queue is nil")
	}
	if deps.Parser == nil {
		deps.Parser = codegraph.NewParser()
	}
	registerL1Refine(processor, deps)
	registerPromote(processor, deps)
	registerWikiBuild(processor, deps)
	registerCodeGraph(processor, deps)
	registerSkillReview(processor, deps)
	registerGitCommit(processor, deps)
	return nil
}

// ---------------------------------------------------------------------------
// L1 refine sub-track
// ---------------------------------------------------------------------------

// L1Fact is one atomic fact distilled from completed turns.
type L1Fact struct {
	Name       string  `json:"name"`
	Slug       string  `json:"slug"`
	Category   string  `json:"category"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence"`
}

// L1RefinePayload is what the turn-terminal path enqueues after a complete
// turn (or every 1-3 turns) so the refiner can promote facts to assets.
type L1RefinePayload struct {
	TeamID         string    `json:"team_id"`
	AgentID        string    `json:"agent_id"`
	IdentityCardID string    `json:"identity_card_id"`
	TurnID         string    `json:"turn_id"`
	SourceEventIDs []string  `json:"source_event_ids"`
	Facts          []L1Fact  `json:"facts"`
	CompletedAt    time.Time `json:"completed_at"`
}

// ShouldRefine decides whether a refine pass should run after completedTurns.
// The Constitution schedules L1 refinement every 1-3 completed turns.
func ShouldRefine(completedTurns int) bool {
	return completedTurns >= 1
}

// EnqueueL1Refine queues an l1_refine job for a completed turn. This is the
// hook the terminal handler calls when a turn reaches 'complete'.
func EnqueueL1Refine(ctx context.Context, queue *Queue, payload L1RefinePayload) (string, error) {
	if queue == nil {
		return "", fmt.Errorf("queue is nil")
	}
	if len(payload.Facts) == 0 {
		return "", nil
	}
	return queue.Enqueue(ctx, Job{
		Queue:        "l1_refine",
		TeamID:       payload.TeamID,
		AgentID:      payload.AgentID,
		AssetType:    "l1",
		PartitionKey: "l1:" + payload.TeamID + ":" + payload.TurnID,
		Payload:      payload,
	})
}

// RefineL1 persists candidate L1 assets for every fact, skipping duplicates
// (same team, slug and summary). It returns the number of new candidates.
func RefineL1(ctx context.Context, teamDB *sql.DB, payload L1RefinePayload) (int, error) {
	return refineL1(ctx, teamDB, payload, nil)
}

func refineL1(ctx context.Context, teamDB *sql.DB, payload L1RefinePayload, writer *wiki.VaultWriter) (int, error) {
	if teamDB == nil {
		return 0, fmt.Errorf("team database is nil")
	}
	created := 0
	for _, fact := range payload.Facts {
		if strings.TrimSpace(fact.Name) == "" || strings.TrimSpace(fact.Slug) == "" {
			continue
		}
		confidence := fact.Confidence
		if confidence <= 0 || confidence > 1 {
			confidence = 0.6
		}
		ok, err := db.CreateL1Asset(ctx, teamDB, db.Asset{
			ID:             idgen.NewID(),
			TeamID:         payload.TeamID,
			IdentityCardID: payload.IdentityCardID,
			AssetType:      "l1",
			Name:           fact.Name,
			Slug:           fact.Slug,
			Summary:        fact.Summary,
			SourceEventIDs: payload.SourceEventIDs,
			Confidence:     confidence,
			Status:         "candidate",
			Visibility:     "team",
			Version:        1,
		}, payload.AgentID)
		if err != nil {
			return created, err
		}
		if ok {
			created++
			if _, err := WriteAssetMarkdown(writer, "l1", fact.Slug, fact.Name, fact.Summary, payload.SourceEventIDs); err != nil {
				return created, fmt.Errorf("write l1 markdown: %w", err)
			}
		}
	}
	return created, nil
}

func registerL1Refine(processor *Processor, deps AssetWorkerDeps) {
	processor.Register("l1_refine", func(ctx context.Context, claim *Claim) error {
		var payload L1RefinePayload
		if err := json.Unmarshal([]byte(claim.PayloadJSON), &payload); err != nil {
			return fmt.Errorf("l1_refine payload: %w", err)
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
		if _, err := refineL1(ctx, teamDB, payload, writer); err != nil {
			return err
		}
		return nil
	})
}
