package worker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gateway/internal/codegraph"
	"gateway/internal/db"
	"gateway/internal/idgen"
	"gateway/internal/skill"
	"gateway/internal/wiki"
)

var (
	// These patterns cover provider keys commonly pasted into a conversation
	// and key-value credentials. Redaction happens before facts reach a queue,
	// database or vault.
	secretTokenPattern    = regexp.MustCompile(`(?i)\b(?:sk|rk|ghp|github_pat|xox[baprs]-)[A-Za-z0-9_-]{8,}`)
	bearerTokenPattern    = regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
	assignedSecretPattern = regexp.MustCompile(`(?i)(\b(?:key|api[ _-]?key|secret(?:[ _-]?key)?|access[ _-]?token|auth(?:orization)?|password)\b\s*[:=]\s*["']?)[A-Za-z0-9._~+/=-]{8,}`)
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

// RedactSensitiveText removes credential-shaped values before text enters any
// durable memory layer. It is idempotent so retries cannot reintroduce a
// secret through a second formatting pass.
func RedactSensitiveText(value string) string {
	value = secretTokenPattern.ReplaceAllString(value, "[REDACTED_SECRET]")
	value = bearerTokenPattern.ReplaceAllString(value, `${1}[REDACTED_SECRET]`)
	value = assignedSecretPattern.ReplaceAllString(value, `${1}[REDACTED_SECRET]`)
	return value
}

// ExtractL1Facts turns completed-turn text into a deterministic L1 candidate.
// The hash-based slug gives semantic deduplication across retries and repeated
// prompts while source event IDs retain the evidence trail.
func ExtractL1Facts(source, turnID string) []L1Fact {
	source = normalizeFactText(RedactSensitiveText(source), 1200)
	if source == "" {
		return nil
	}
	digest := sha256.Sum256([]byte(strings.ToLower(source)))
	suffix := hex.EncodeToString(digest[:])[:16]
	return []L1Fact{{
		Name:       "Conversation insight " + suffix,
		Slug:       "conversation-" + suffix,
		Category:   classifyFact(source),
		Summary:    source,
		Confidence: 0.6,
	}}
}

func normalizeFactText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return value
}

func classifyFact(summary string) string {
	lower := strings.ToLower(summary)
	switch {
	case strings.Contains(lower, "identity"), strings.Contains(lower, "身份"), strings.Contains(lower, "role"), strings.Contains(lower, "职责"):
		return "identity"
	case strings.Contains(lower, "always"), strings.Contains(lower, "must"), strings.Contains(lower, "rule"), strings.Contains(lower, "policy"), strings.Contains(lower, "原则"), strings.Contains(lower, "规则"):
		return "principle"
	default:
		return "conversation"
	}
}

func sanitizeFact(fact L1Fact) L1Fact {
	fact.Name = normalizeFactText(RedactSensitiveText(fact.Name), 200)
	fact.Summary = normalizeFactText(RedactSensitiveText(fact.Summary), 1200)
	fact.Slug = sanitizeFactSlug(fact.Slug)
	if fact.Slug == "" && fact.Summary != "" {
		digest := sha256.Sum256([]byte(strings.ToLower(fact.Summary)))
		fact.Slug = "conversation-" + hex.EncodeToString(digest[:])[:16]
	}
	if fact.Confidence <= 0 || fact.Confidence > 1 {
		fact.Confidence = 0.6
	}
	return fact
}

func sanitizeFactSlug(value string) string {
	value = strings.ToLower(RedactSensitiveText(strings.TrimSpace(value)))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case !lastDash && builder.Len() > 0:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

// ShouldRefine decides whether a refine pass should run after completedTurns.
// The Constitution schedules L1 refinement every 1-3 completed turns.
func ShouldRefine(completedTurns int) bool {
	return completedTurns >= 1
}

// EnqueueL1Refine queues an l1_refine job for a completed turn. This is the
// hook the terminal handler calls when a turn reaches 'complete'.
func EnqueueL1Refine(ctx context.Context, queue *Queue, payload L1RefinePayload) (string, error) {
	if queue == nil || queue.database == nil {
		return "", fmt.Errorf("queue is nil")
	}
	if queue.initErr != nil {
		return "", queue.initErr
	}
	if len(payload.Facts) == 0 {
		return "", nil
	}
	sanitizedFacts := make([]L1Fact, 0, len(payload.Facts))
	for _, fact := range payload.Facts {
		fact = sanitizeFact(fact)
		if fact.Name != "" && fact.Slug != "" && fact.Summary != "" {
			sanitizedFacts = append(sanitizedFacts, fact)
		}
	}
	if len(sanitizedFacts) == 0 {
		return "", nil
	}
	payload.Facts = sanitizedFacts
	partitionKey := "l1:" + payload.TeamID + ":" + payload.TurnID
	var existingID string
	err := queue.database.QueryRowContext(ctx, `SELECT id FROM jobs WHERE queue='l1_refine' AND partition_key=? AND status IN ('pending','processing','done') ORDER BY created_at DESC LIMIT 1`, partitionKey).Scan(&existingID)
	if err == nil {
		return existingID, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("check existing l1_refine job: %w", err)
	}
	id, err := queue.Enqueue(ctx, Job{
		Queue:        "l1_refine",
		TeamID:       payload.TeamID,
		AgentID:      payload.AgentID,
		AssetType:    "l1",
		PartitionKey: partitionKey,
		Payload:      payload,
	})
	if err == nil {
		return id, nil
	}
	// A concurrent terminal writer may have won the logical enqueue check.
	if queryErr := queue.database.QueryRowContext(ctx, `SELECT id FROM jobs WHERE queue='l1_refine' AND partition_key=? AND status IN ('pending','processing','done') ORDER BY created_at DESC LIMIT 1`, partitionKey).Scan(&existingID); queryErr == nil {
		return existingID, nil
	}
	return "", err
}

// RefineL1 persists candidate L1 assets for every fact, skipping duplicates
// (same team, slug and summary). It returns the number of new candidates.
func RefineL1(ctx context.Context, teamDB *sql.DB, payload L1RefinePayload) (int, error) {
	return refineL1(ctx, teamDB, payload, nil)
}

func refineL1(ctx context.Context, teamDB *sql.DB, payload L1RefinePayload, writer *wiki.VaultWriter) (int, error) {
	created, _, err := refineL1WithRoutes(ctx, teamDB, payload, writer, nil, AssetWorkerDeps{})
	return created, err
}

type refinedL1Asset struct {
	ID   string
	Fact L1Fact
}

func refineL1WithRoutes(ctx context.Context, teamDB *sql.DB, payload L1RefinePayload, writer *wiki.VaultWriter, queue *Queue, deps AssetWorkerDeps) (int, []refinedL1Asset, error) {
	if teamDB == nil {
		return 0, nil, fmt.Errorf("team database is nil")
	}
	created := 0
	assets := make([]refinedL1Asset, 0, len(payload.Facts))
	for _, fact := range payload.Facts {
		fact = sanitizeFact(fact)
		if strings.TrimSpace(fact.Name) == "" || strings.TrimSpace(fact.Slug) == "" {
			continue
		}
		assetID := idgen.NewID()
		ok, err := db.CreateL1Asset(ctx, teamDB, db.Asset{
			ID:             assetID,
			TeamID:         payload.TeamID,
			IdentityCardID: payload.IdentityCardID,
			AssetType:      "l1",
			Name:           fact.Name,
			Slug:           fact.Slug,
			Summary:        fact.Summary,
			SourceEventIDs: payload.SourceEventIDs,
			Confidence:     fact.Confidence,
			Status:         "candidate",
			Visibility:     "team",
			Version:        1,
		}, payload.AgentID)
		if err != nil {
			return created, assets, err
		}
		if ok {
			created++
			assets = append(assets, refinedL1Asset{ID: assetID, Fact: fact})
			if _, err := WriteAssetMarkdown(writer, "l1", fact.Slug, fact.Name, fact.Summary, payload.SourceEventIDs); err != nil {
				return created, assets, fmt.Errorf("write l1 markdown: %w", err)
			}
		} else if queue != nil {
			// A prior attempt may have created the candidate before failing while
			// routing derived jobs. Include the existing row so retries repair any
			// missing downstream queue entries.
			existing, listErr := db.ListAssetsBySlug(ctx, teamDB, payload.TeamID, "l1", fact.Slug)
			if listErr != nil {
				return created, assets, fmt.Errorf("load existing l1 asset: %w", listErr)
			}
			for _, candidate := range existing {
				if candidate.Summary == fact.Summary {
					assets = append(assets, refinedL1Asset{ID: candidate.ID, Fact: fact})
					break
				}
			}
		}
	}
	if err := routeRefinedAssets(ctx, teamDB, queue, deps, payload, assets); err != nil {
		return created, assets, err
	}
	return created, assets, nil
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
		if _, _, err := refineL1WithRoutes(ctx, teamDB, payload, writer, processor.Queue, deps); err != nil {
			return err
		}
		return nil
	})
}

func routeRefinedAssets(ctx context.Context, teamDB *sql.DB, queue *Queue, deps AssetWorkerDeps, payload L1RefinePayload, assets []refinedL1Asset) error {
	if queue == nil || len(assets) == 0 {
		return nil
	}
	for _, item := range assets {
		fact := item.Fact
		wikiPayload := WikiBuildPayload{
			TeamID:       payload.TeamID,
			AgentID:      payload.AgentID,
			AssetID:      item.ID,
			Title:        fact.Name,
			Slug:         "l1-" + fact.Slug,
			Summary:      fact.Summary,
			Content:      "Category: " + fact.Category + "\n\nSource events: " + strings.Join(payload.SourceEventIDs, ", "),
			RelativeDir:  "02_Wiki知识库/待审核",
			FileName:     "l1-" + fact.Slug + ".md",
			SourceEvents: payload.SourceEventIDs,
		}
		if _, err := enqueueUniqueJob(ctx, queue, Job{
			Queue: "wiki_build", TeamID: payload.TeamID, AgentID: payload.AgentID,
			AssetID: item.ID, AssetType: "wiki", PartitionKey: "wiki:" + payload.TeamID + ":" + fact.Slug,
			Payload: wikiPayload,
		}); err != nil {
			return fmt.Errorf("enqueue wiki build: %w", err)
		}

		// Two distinct source events (inbound + terminal) satisfy the evidence
		// gate for an initial promotion. Identity/principle facts advance to
		// their corresponding layer; ordinary facts remain conservative L2.
		if len(payload.SourceEventIDs) >= PromotionGate {
			layer := "l2"
			switch fact.Category {
			case "identity":
				layer = "l3"
			case "principle":
				layer = "l4"
			}
			promote := PromotePayload{
				TeamID: payload.TeamID, AgentID: payload.AgentID, IdentityCardID: payload.IdentityCardID,
				Layer: layer, Slug: fact.Slug, Name: fact.Name, Summary: fact.Summary,
				Confidence: fact.Confidence, SourceEventIDs: payload.SourceEventIDs,
			}
			if _, err := enqueueUniqueJob(ctx, queue, Job{
				Queue: layer + "_promote", TeamID: payload.TeamID, AgentID: payload.AgentID,
				AssetID: fact.Slug, AssetType: layer, PartitionKey: "promote:" + payload.TeamID + ":" + layer + ":" + fact.Slug,
				Payload: promote,
			}); err != nil {
				return fmt.Errorf("enqueue %s promotion: %w", layer, err)
			}
		}

		candidate := skill.Skill{
			Name: fact.Slug, DisplayName: fact.Name, Version: "0.1.0", Status: "candidate", Scope: "team",
			TriggerBoundary: "When a completed turn yields a reusable memory fact.",
			Steps:           []skill.Step{{Order: 1, Title: "Review extracted fact", Body: fact.Summary}},
			Validation:      skill.Validation{PassCriteria: "Fact is supported by its source events and contains no credentials."},
			SourceIDs:       payload.SourceEventIDs,
		}
		if _, err := EnqueueSkillReview(ctx, queue, SkillReviewPayload{TeamID: payload.TeamID, AgentID: payload.AgentID, Action: "candidate", Skill: candidate}); err != nil {
			return fmt.Errorf("enqueue skill candidate: %w", err)
		}
	}

	// CodeGraph receives a team-level sweep even before the first repository is
	// registered. The worker treats an empty RepoID as a safe no-op, preserving
	// the sub-track trigger without manufacturing a permanently failing job.
	rows, err := teamDB.QueryContext(ctx, `SELECT id FROM code_repos ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list code repositories: %w", err)
	}
	defer rows.Close()
	repoCount := 0
	for rows.Next() {
		var repoID string
		if err := rows.Scan(&repoID); err != nil {
			return fmt.Errorf("scan code repository: %w", err)
		}
		if _, err := enqueueUniqueJob(ctx, queue, Job{
			Queue: "codegraph_incremental", TeamID: payload.TeamID, AssetID: repoID,
			AssetType: "codegraph", PartitionKey: "codegraph:" + payload.TeamID + ":" + repoID,
			Payload: CodeGraphPayload{TeamID: payload.TeamID, RepoID: repoID},
		}); err != nil {
			return fmt.Errorf("enqueue codegraph: %w", err)
		}
		repoCount++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list code repositories: %w", err)
	}
	if repoCount == 0 {
		if _, err := enqueueUniqueJob(ctx, queue, Job{
			Queue: "codegraph_incremental", TeamID: payload.TeamID,
			AssetType: "codegraph", PartitionKey: "codegraph:" + payload.TeamID + ":all",
			Payload: CodeGraphPayload{TeamID: payload.TeamID},
		}); err != nil {
			return fmt.Errorf("enqueue codegraph team sweep: %w", err)
		}
	}

	if deps.VaultPath != "" && deps.GlobalDB != nil {
		pending, err := HasPendingGitCommit(ctx, deps.GlobalDB)
		if err != nil {
			return fmt.Errorf("check git commit queue: %w", err)
		}
		if !pending {
			if _, err := EnqueueGitCommit(ctx, queue, GitCommitPayload{TeamID: payload.TeamID, Reason: "post-refine batch commit"}); err != nil {
				return fmt.Errorf("enqueue git commit: %w", err)
			}
		}
	}
	return nil
}

func enqueueUniqueJob(ctx context.Context, queue *Queue, job Job) (string, error) {
	if queue == nil || queue.database == nil {
		return "", fmt.Errorf("worker queue is nil")
	}
	if queue.initErr != nil {
		return "", queue.initErr
	}
	if job.PartitionKey != "" {
		var existing string
		err := queue.database.QueryRowContext(ctx, `SELECT id FROM jobs WHERE queue=? AND partition_key=? AND status IN ('pending','processing','done') ORDER BY created_at DESC LIMIT 1`, job.Queue, job.PartitionKey).Scan(&existing)
		if err == nil {
			return existing, nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
	}
	id, err := queue.Enqueue(ctx, job)
	if err != nil && job.PartitionKey != "" {
		var existing string
		if queryErr := queue.database.QueryRowContext(ctx, `SELECT id FROM jobs WHERE queue=? AND partition_key=? AND status IN ('pending','processing','done') ORDER BY created_at DESC LIMIT 1`, job.Queue, job.PartitionKey).Scan(&existing); queryErr == nil {
			return existing, nil
		}
	}
	return id, err
}
