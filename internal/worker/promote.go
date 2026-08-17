package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"gateway/internal/db"
	"gateway/internal/idgen"
	"gateway/internal/wiki"
)

// PromotionGate is the minimum number of distinct source events required
// before a fact can be promoted to L2/L3/L4 (Constitution: >= 2 evidence).
const PromotionGate = 2

// PromotePayload describes one promotion attempt to a higher layer.
type PromotePayload struct {
	TeamID         string   `json:"team_id"`
	AgentID        string   `json:"agent_id"`
	IdentityCardID string   `json:"identity_card_id"`
	Layer          string   `json:"layer"` // l2 | l3 | l4
	Slug           string   `json:"slug"`
	Name           string   `json:"name"`
	Summary        string   `json:"summary"`
	Confidence     float64  `json:"confidence"`
	SourceEventIDs []string `json:"source_event_ids"`
	// L3 identity-card update payload
	CardRole             string   `json:"card_role,omitempty"`
	CardResponsibilities string   `json:"card_responsibilities,omitempty"`
	CardBoundaries       string   `json:"card_boundaries,omitempty"`
	CardAllowedTools     []string `json:"card_allowed_tools,omitempty"`
	CardStyle            string   `json:"card_style,omitempty"`
}

// EnqueuePromotion queues an L2/L3/L4 promotion job. It returns (false, nil)
// when the payload lacks enough explicit evidence to satisfy the gate.
func EnqueuePromotion(ctx context.Context, queue *Queue, payload PromotePayload) (bool, error) {
	if queue == nil {
		return false, fmt.Errorf("queue is nil")
	}
	if payload.Layer != "l2" && payload.Layer != "l3" && payload.Layer != "l4" {
		return false, fmt.Errorf("promotion layer must be l2, l3 or l4")
	}
	if len(payload.SourceEventIDs) < PromotionGate {
		return false, nil
	}
	_, err := queue.Enqueue(ctx, Job{
		Queue:        payload.Layer + "_promote",
		TeamID:       payload.TeamID,
		AgentID:      payload.AgentID,
		AssetID:      payload.Slug,
		AssetType:    payload.Layer,
		PartitionKey: "promote:" + payload.TeamID + ":" + payload.Layer + ":" + payload.Slug,
		Payload:      payload,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// EvidenceCount returns the number of distinct source events backing a topic
// (explicit events from the payload plus events attached to existing assets
// of the same team and slug).
func EvidenceCount(ctx context.Context, teamDB *sql.DB, teamID, slug string, explicit []string) (int, error) {
	if teamDB == nil {
		return 0, fmt.Errorf("team database is nil")
	}
	return db.CountPromotionEvidence(ctx, teamDB, teamID, slug, explicit)
}

// PromoteL2 promotes a fact to L2 knowledge/experience when the evidence
// gate is met. db.PromoteAsset creates the next version on conflicts.
func PromoteL2(ctx context.Context, teamDB *sql.DB, payload PromotePayload) (db.Asset, error) {
	if teamDB == nil {
		return db.Asset{}, fmt.Errorf("team database is nil")
	}
	evidence, err := EvidenceCount(ctx, teamDB, payload.TeamID, payload.Slug, payload.SourceEventIDs)
	if err != nil {
		return db.Asset{}, err
	}
	if evidence < PromotionGate {
		return db.Asset{}, fmt.Errorf("l2 promotion gate not met: %d evidence < %d", evidence, PromotionGate)
	}
	return db.PromoteAsset(ctx, teamDB, db.Asset{
		ID:             idgen.NewID(),
		TeamID:         payload.TeamID,
		IdentityCardID: payload.IdentityCardID,
		AssetType:      "l2",
		Name:           payload.Name,
		Slug:           payload.Slug,
		Summary:        payload.Summary,
		SourceEventIDs: payload.SourceEventIDs,
		Confidence:     payload.Confidence,
		Status:         "promoted",
		Visibility:     "team",
	}, payload.AgentID)
}

// PromoteL3 promotes a role/identity fact and updates the global identity
// card. The card update is versioned (never mutated in place).
func PromoteL3(ctx context.Context, globalDB, teamDB *sql.DB, payload PromotePayload) (string, error) {
	if teamDB == nil {
		return "", fmt.Errorf("team database is nil")
	}
	evidence, err := EvidenceCount(ctx, teamDB, payload.TeamID, payload.Slug, payload.SourceEventIDs)
	if err != nil {
		return "", err
	}
	if evidence < PromotionGate {
		return "", fmt.Errorf("l3 promotion gate not met: %d evidence < %d", evidence, PromotionGate)
	}
	asset, err := db.PromoteAsset(ctx, teamDB, db.Asset{
		ID:             idgen.NewID(),
		TeamID:         payload.TeamID,
		IdentityCardID: payload.IdentityCardID,
		AssetType:      "l3",
		Name:           payload.Name,
		Slug:           payload.Slug,
		Summary:        payload.Summary,
		SourceEventIDs: payload.SourceEventIDs,
		Confidence:     payload.Confidence,
		Status:         "promoted",
		Visibility:     "team",
	}, payload.AgentID)
	if err != nil {
		return "", err
	}
	if globalDB == nil {
		return asset.ID, nil
	}
	if payload.IdentityCardID == "" {
		return asset.ID, nil
	}
	cardID, err := db.UpsertIdentityCard(ctx, globalDB, db.IdentityCard{
		ID:               payload.IdentityCardID,
		TeamID:           payload.TeamID,
		AgentID:          payload.AgentID,
		Name:             payload.Name,
		Role:             payload.CardRole,
		Responsibilities: payload.CardResponsibilities,
		Boundaries:       payload.CardBoundaries,
		AllowedTools:     payload.CardAllowedTools,
		Style:            payload.CardStyle,
		Visibility:       "agent",
		Status:           "active",
		SourceEventIDs:   payload.SourceEventIDs,
	})
	if err != nil {
		return "", fmt.Errorf("upsert identity card: %w", err)
	}
	return cardID, nil
}

// PromoteL4 promotes a stable, cross-project principle. Version conflicts
// always create the next version instead of overwriting prior conclusions.
func PromoteL4(ctx context.Context, teamDB *sql.DB, payload PromotePayload) (db.Asset, error) {
	if teamDB == nil {
		return db.Asset{}, fmt.Errorf("team database is nil")
	}
	evidence, err := EvidenceCount(ctx, teamDB, payload.TeamID, payload.Slug, payload.SourceEventIDs)
	if err != nil {
		return db.Asset{}, err
	}
	if evidence < PromotionGate {
		return db.Asset{}, fmt.Errorf("l4 promotion gate not met: %d evidence < %d", evidence, PromotionGate)
	}
	return db.PromoteAsset(ctx, teamDB, db.Asset{
		ID:             idgen.NewID(),
		TeamID:         payload.TeamID,
		IdentityCardID: payload.IdentityCardID,
		AssetType:      "l4",
		Name:           payload.Name,
		Slug:           payload.Slug,
		Summary:        payload.Summary,
		SourceEventIDs: payload.SourceEventIDs,
		Confidence:     payload.Confidence,
		Status:         "promoted",
		Visibility:     "team",
	}, payload.AgentID)
}

func registerPromote(processor *Processor, deps AssetWorkerDeps) {
	handler := func(ctx context.Context, claim *Claim) error {
		var payload PromotePayload
		if err := json.Unmarshal([]byte(claim.PayloadJSON), &payload); err != nil {
			return fmt.Errorf("promotion payload: %w", err)
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
		switch payload.Layer {
		case "l2":
			_, err = PromoteL2(ctx, teamDB, payload)
		case "l3":
			_, err = PromoteL3(ctx, deps.GlobalDB, teamDB, payload)
		case "l4":
			_, err = PromoteL4(ctx, teamDB, payload)
		default:
			err = fmt.Errorf("unknown promotion layer %q", payload.Layer)
		}
		if err != nil {
			return err
		}
		_, err = WriteAssetMarkdown(writer, payload.Layer, payload.Slug, payload.Name, payload.Summary, payload.SourceEventIDs)
		return err
	}
	processor.Register("l2_promote", handler)
	processor.Register("l3_promote", handler)
	processor.Register("l4_promote", handler)
}
