package inject

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gateway/internal/adapter"
	"gateway/internal/embedding"
	"gateway/internal/paths"
	"gateway/internal/retrieval"
)

const ManifestVersion = adapter.ManifestVersion

type Request struct {
	GlobalDB       *sql.DB
	TeamDB         *sql.DB
	MemoryRoot     string
	TeamID         string
	AgentID        string
	UserID         string
	Role           string
	IdentityCardID string
	Query          string
	TokenBudget    int
	Limit          int
	// EmbeddingService is the optional semantic encoder shared with the MCP
	// retrieval path (ALL-233). A nil service keeps the FTS-only degradation:
	// retrieval.SearchWithEmbedding degrades inside SearchHybrid exactly like
	// an unconfigured embedding pipeline.
	EmbeddingService *embedding.Service
}

// Build assembles a protocol-neutral injection package from the approved path
// manifest, active identity card and ACL-filtered retrieval results.
func Build(ctx context.Context, request Request) (adapter.InjectionPackage, string, error) {
	if request.TeamDB == nil {
		return adapter.InjectionPackage{}, "", fmt.Errorf("team database is required")
	}
	if request.TeamID == "" {
		return adapter.InjectionPackage{}, "", fmt.Errorf("team id is required")
	}
	manifest, err := readManifest(request.MemoryRoot)
	if err != nil {
		return adapter.InjectionPackage{}, "", err
	}
	identity, err := loadIdentityCard(ctx, request.GlobalDB, request.TeamID, request.IdentityCardID)
	if err != nil {
		return adapter.InjectionPackage{}, "", err
	}
	result, err := retrieval.SearchWithEmbedding(ctx, request.TeamDB, request.EmbeddingService, retrieval.Request{
		TeamID: request.TeamID, AgentID: request.AgentID, UserID: request.UserID, Role: request.Role,
		IdentityCardID: request.IdentityCardID, Query: request.Query, Limit: request.Limit, TokenBudget: request.TokenBudget,
	})
	if err != nil {
		return adapter.InjectionPackage{}, "", err
	}
	pkg := adapter.InjectionPackage{PathManifest: manifest, IdentityCard: identity, ManifestVersion: ManifestVersion, Truncated: result.Truncated}
	seenSources := map[string]struct{}{}
	for _, item := range result.Items {
		text := item.Snippet
		if text == "" {
			text = item.Summary
		}
		pkg.RetrievalSnips = append(pkg.RetrievalSnips, fmt.Sprintf("[%s:%s] %s", item.AssetType, item.ID, text))
		for _, sourceID := range item.SourceEventIDs {
			if _, exists := seenSources[sourceID]; !exists && sourceID != "" {
				seenSources[sourceID] = struct{}{}
				pkg.SourceEventIDs = append(pkg.SourceEventIDs, sourceID)
			}
		}
	}
	return pkg, Render(pkg), nil
}

func readManifest(memoryRoot string) (string, error) {
	if strings.TrimSpace(memoryRoot) == "" {
		return "", fmt.Errorf("memory root is required")
	}
	path, err := paths.SafeJoin(memoryRoot, filepath.Join("00_系统", "路径注入清单.md"))
	if err != nil {
		return "", fmt.Errorf("path manifest outside memory root: %w", err)
	}
	content, err := os.ReadFile(path)
	if err == nil {
		return string(content), nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("read path manifest: %w", err)
	}
	return adapter.DefaultPathManifest, nil
}

func loadIdentityCard(ctx context.Context, database *sql.DB, teamID, identityCardID string) (string, error) {
	if database == nil || identityCardID == "" {
		return "", nil
	}
	var name, role, responsibilities, boundaries string
	err := database.QueryRowContext(ctx, `SELECT name, role, COALESCE(responsibilities,''), COALESCE(boundaries,'') FROM identity_cards WHERE id = ? AND team_id = ? AND status = 'active'`, identityCardID, teamID).Scan(&name, &role, &responsibilities, &boundaries)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load identity card: %w", err)
	}
	parts := []string{"## Agent Identity", "Name: " + name, "Role: " + role}
	if responsibilities != "" {
		parts = append(parts, "Responsibilities: "+responsibilities)
	}
	if boundaries != "" {
		parts = append(parts, "Boundaries: "+boundaries)
	}
	return strings.Join(parts, "\n"), nil
}

// Render includes source IDs and truncation as machine-readable comments so
// every upstream payload can be audited without parsing a database row.
func Render(pkg adapter.InjectionPackage) string {
	parts := make([]string, 0, 6)
	if pkg.PathManifest != "" {
		parts = append(parts, pkg.PathManifest)
	}
	if pkg.IdentityCard != "" {
		parts = append(parts, pkg.IdentityCard)
	}
	if len(pkg.RetrievalSnips) > 0 {
		parts = append(parts, "## Retrieved Context\n"+strings.Join(pkg.RetrievalSnips, "\n"))
	}
	if pkg.ManifestVersion == "" {
		pkg.ManifestVersion = ManifestVersion
	}
	parts = append(parts, "<!-- source_ids: ["+strings.Join(pkg.SourceEventIDs, ",")+"] -->")
	parts = append(parts, "<!-- manifest_version: "+pkg.ManifestVersion+" -->")
	if pkg.Truncated {
		parts = append(parts, "<!-- memory_truncated: true -->")
	}
	return strings.Join(parts, "\n\n")
}
