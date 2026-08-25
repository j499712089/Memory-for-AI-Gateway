package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"gateway/internal/db"
	"gateway/internal/wiki"
)

// WikiBuildPayload is the input of a wiki_build job. Content may contain
// Obsidian [[internal links]], which are resolved into wiki_edges.
type WikiBuildPayload struct {
	TeamID       string   `json:"team_id"`
	AgentID      string   `json:"agent_id"`
	AssetID      string   `json:"asset_id"`
	Title        string   `json:"title"`
	Slug         string   `json:"slug"`
	Summary      string   `json:"summary"`
	Content      string   `json:"content"`
	RelativeDir  string   `json:"relative_dir"`
	FileName     string   `json:"file_name"`
	SourceEvents []string `json:"source_event_ids"`
}

// layerVaultDirs maps asset layers to their vault directories so every
// sub-track worker renders Obsidian-compatible markdown for its assets.
var layerVaultDirs = map[string]string{
	"l1":    "L1_任务纪要",
	"l2":    "L2_知识经验",
	"l3":    "L3_团队身份/代理",
	"l4":    "L4_长期准则",
	"skill": "04_技能库/团队共享",
}

// WriteAssetMarkdown renders a frontmatter + heading markdown file into the
// vault for an L1-L4 or skill asset. It returns the relative path, or ""
// when no writer is configured (DB-only mode).
func WriteAssetMarkdown(writer *wiki.VaultWriter, layer, slug, name, summary string, sources []string) (string, error) {
	if writer == nil {
		return "", nil
	}
	dir, ok := layerVaultDirs[layer]
	if !ok {
		return "", nil
	}
	frontmatter := wiki.RenderFrontmatter(wiki.Frontmatter{
		Title:    name,
		Type:     layer,
		Status:   "ready",
		Source:   sources,
		Modified: time.Now().UTC().Format(time.RFC3339),
	})
	body := frontmatter + "\n" + wiki.ComposePage(name, summary, "")
	return writer.WriteMarkdown(dir, slug+".md", []byte(body))
}

// BuildWikiPage runs the full wiki pipeline for one page:
//  1. render the markdown body and write it into the vault
//  2. upsert the wiki_pages row
//  3. resolve [[links]] into wiki_edges
//  4. rebuild the FTS5 index so the page is searchable
//
// It returns the number of edges written.
func BuildWikiPage(ctx context.Context, teamDB *sql.DB, writer *wiki.VaultWriter, payload WikiBuildPayload) (int, error) {
	if teamDB == nil {
		return 0, fmt.Errorf("team database is nil")
	}
	if payload.Title == "" || payload.Slug == "" {
		return 0, fmt.Errorf("wiki title and slug are required")
	}
	if payload.AssetID == "" {
		return 0, fmt.Errorf("wiki asset id is required")
	}
	if payload.RelativeDir == "" {
		payload.RelativeDir = "02_Wiki知识库/团队共享"
	}
	if payload.FileName == "" {
		payload.FileName = payload.Slug + ".md"
	}
	frontmatter := wiki.RenderFrontmatter(wiki.Frontmatter{
		Title:    payload.Title,
		AssetID:  payload.AssetID,
		Type:     "wiki",
		Status:   "ready",
		Source:   payload.SourceEvents,
		Modified: time.Now().UTC().Format(time.RFC3339),
	})
	body := frontmatter + "\n" + wiki.ComposePage(payload.Title, payload.Summary, payload.Content)

	var err error
	if writer != nil {
		_, err = writer.WriteMarkdown(payload.RelativeDir, payload.FileName, []byte(body))
		if err != nil {
			return 0, fmt.Errorf("write wiki markdown: %w", err)
		}
	}
	pageID, err := db.UpsertWikiPage(ctx, teamDB, db.WikiPage{
		ID:              "wiki-" + payload.Slug,
		AssetID:         payload.AssetID,
		Title:           payload.Title,
		Slug:            payload.Slug,
		ContentMD:       body,
		FrontmatterJSON: `{"type":"wiki","status":"ready"}`,
		Status:          "ready",
	})
	if err != nil {
		return 0, fmt.Errorf("upsert wiki page: %w", err)
	}
	links := wiki.ParseLinks(payload.Content)
	targets := wiki.TargetSlugs(links)
	edges, err := db.UpdateWikiEdges(ctx, teamDB, pageID, targets)
	if err != nil {
		return 0, fmt.Errorf("update wiki edges: %w", err)
	}
	if _, err := db.RebuildWikiFTS(ctx, teamDB); err != nil {
		return 0, fmt.Errorf("rebuild wiki fts: %w", err)
	}
	return edges, nil
}

func registerWikiBuild(processor *Processor, deps AssetWorkerDeps) {
	processor.Register("wiki_build", func(ctx context.Context, claim *Claim) error {
		var payload WikiBuildPayload
		if err := json.Unmarshal([]byte(claim.PayloadJSON), &payload); err != nil {
			return fmt.Errorf("wiki_build payload: %w", err)
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
		if _, err := BuildWikiPage(ctx, teamDB, writer, payload); err != nil {
			return err
		}
		return nil
	})
}
