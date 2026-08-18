package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"gateway/internal/codegraph"
	"gateway/internal/db"
)

// CodeGraphPayload names the repo to index. The incremental worker loads the
// repo row, hashes unchanged files, diffs against the cursor, re-parses only
// changed files and advances the cursor — see codegraph.IncrementalIndexer.
type CodeGraphPayload struct {
	TeamID string `json:"team_id"`
	RepoID string `json:"repo_id"`
	TurnID string `json:"turn_id,omitempty"`
}

// EnqueueCodeGraphIncremental queues a codegraph_incremental job for a repo.
func EnqueueCodeGraphIncremental(ctx context.Context, queue *Queue, payload CodeGraphPayload) (string, error) {
	if queue == nil {
		return "", fmt.Errorf("queue is nil")
	}
	return queue.Enqueue(ctx, Job{
		Queue:        "codegraph_incremental",
		TeamID:       payload.TeamID,
		AssetID:      payload.RepoID,
		AssetType:    "codegraph",
		PartitionKey: codeGraphPartition(payload.TeamID, payload.TurnID, payload.RepoID),
		Payload:      payload,
	})
}

// IndexRepo runs one incremental pass over a repo and persists the cursor.
func IndexRepo(ctx context.Context, teamDB *sql.DB, repo db.CodeRepo, parser codegraph.Parser) (codegraph.IndexReport, error) {
	if teamDB == nil {
		return codegraph.IndexReport{}, fmt.Errorf("team database is nil")
	}
	indexer := codegraph.NewIncrementalIndexer(teamDB, parser)
	return indexer.IndexRepo(ctx, repo)
}

func registerCodeGraph(processor *Processor, deps AssetWorkerDeps) {
	processor.Register("codegraph_incremental", func(ctx context.Context, claim *Claim) error {
		var payload CodeGraphPayload
		if err := json.Unmarshal([]byte(claim.PayloadJSON), &payload); err != nil {
			return fmt.Errorf("codegraph payload: %w", err)
		}
		if payload.TeamID == "" {
			return fmt.Errorf("codegraph team id is required")
		}
		teamDB, err := deps.resolveTeamDB(payload.TeamID)
		if err != nil {
			return err
		}
		defer teamDB.Close()

		// A team-level sweep is a valid no-op when no repository has been
		// registered yet. The refine pipeline still records that the codegraph
		// sub-track was considered, while avoiding a permanently failing job for
		// an empty RepoID.
		if payload.RepoID == "" {
			rows, err := teamDB.QueryContext(ctx, `SELECT id FROM code_repos ORDER BY id`)
			if err != nil {
				return fmt.Errorf("list code repos: %w", err)
			}
			defer rows.Close()
			for rows.Next() {
				var repoID string
				if err := rows.Scan(&repoID); err != nil {
					return fmt.Errorf("scan code repo: %w", err)
				}
				if err := indexCodeRepo(ctx, teamDB, repoID, deps.Parser); err != nil {
					return err
				}
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("iterate code repos: %w", err)
			}
			return nil
		}
		return indexCodeRepo(ctx, teamDB, payload.RepoID, deps.Parser)
	})
}

func indexCodeRepo(ctx context.Context, teamDB *sql.DB, repoID string, parser codegraph.Parser) error {
	repo, err := db.GetCodeRepoByID(ctx, teamDB, repoID)
	if err != nil {
		return fmt.Errorf("load code repo %s: %w", repoID, err)
	}
	if _, err := IndexRepo(ctx, teamDB, repo, parser); err != nil {
		return fmt.Errorf("index code repo %s: %w", repoID, err)
	}
	return nil
}
