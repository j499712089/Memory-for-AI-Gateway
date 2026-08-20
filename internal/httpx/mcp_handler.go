package httpx

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"gateway/internal/acl"
	"gateway/internal/db"
	"gateway/internal/embedding"
	"gateway/internal/idgen"
	"gateway/internal/paths"
	"gateway/internal/retrieval"
	"gateway/internal/worker"

	"github.com/gin-gonic/gin"
)

// MCPHandler implements the internal service API consumed by the MCP Server
// (Node.js, :8097). The MCP Server never touches SQLite directly: reads go
// through the retrieval/query layer, the single write tool (memory/append)
// goes through the same SQLite transaction layer used by the Worker pipeline.
type MCPHandler struct {
	globalDB       *sql.DB
	teamsDir       string
	memoryRoot     string
	embeddingQueue *worker.Queue
}

func NewMCPHandler(globalDB *sql.DB, memoryRoot string) *MCPHandler {
	teamsDir := paths.TeamsDir(memoryRoot)
	return &MCPHandler{globalDB: globalDB, teamsDir: teamsDir, memoryRoot: memoryRoot, embeddingQueue: worker.NewQueue(globalDB, 30*time.Second)}
}

var safeTeamID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9\-_]{0,63}$`)

// errorResponse writes the contract-standard error body.
func errorResponse(c *gin.Context, status int, errType, message string) {
	requestID, _ := GetIdempotencyKey(c)
	if requestID == "" {
		requestID = idgen.NewID()
	}
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message, "request_id": requestID}})
}

// auditPathViolation records rejected DB-controlled file paths without
// allowing a cancelled HTTP request to interrupt the audit write. Auditing is
// best effort because a path rejection must still return a deterministic 403
// when the global database is unavailable.
func (h *MCPHandler) auditPathViolation(c *gin.Context, requestedPath string, pathErr error) {
	if h == nil || h.globalDB == nil {
		return
	}
	requestID, _ := GetIdempotencyKey(c)
	actorID, _ := GetAPIKeyID(c)
	failure := "path is outside the memory root"
	if pathErr != nil {
		failure = pathErr.Error()
	}
	_, _ = h.globalDB.ExecContext(context.WithoutCancel(c.Request.Context()), `
		INSERT INTO audit_log
			(id, request_id, actor_type, actor_id, action, target_type, target_id, result, failure)
		VALUES (?, ?, 'system', ?, 'path_access_denied', 'memory_path', ?, 'denied', ?)
	`, idgen.NewID(), nullableAudit(requestID), nullableAudit(actorID), requestedPath, failure)
}

func nullableAudit(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// resolveTeam derives the effective team from the authenticated key and the
// optional team_id in the request body. A mismatch is forbidden.
func (h *MCPHandler) resolveTeam(c *gin.Context, requested string) (string, error) {
	keyTeam, ok := GetTeamID(c)
	if !ok {
		keyTeam = ""
	}
	requested = strings.TrimSpace(requested)
	if requested != "" && keyTeam != "" && requested != keyTeam {
		return "", fmt.Errorf("team mismatch: key is scoped to %s", keyTeam)
	}
	teamID := requested
	if teamID == "" {
		teamID = keyTeam
	}
	if teamID == "" {
		return "", fmt.Errorf("team_id is required")
	}
	if !safeTeamID.MatchString(teamID) {
		return "", fmt.Errorf("invalid team_id")
	}
	return teamID, nil
}

// openTeamDB opens (creating if needed) a team-local database and ensures the
// asset schema required by the MCP read/write endpoints exists.
func (h *MCPHandler) openTeamDB(teamID string) (*sql.DB, error) {
	teamDB, err := db.OpenTeamDB(h.teamsDir, teamID)
	if err != nil {
		return nil, err
	}
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		teamDB.Close()
		return nil, err
	}
	if err := db.EnsureAssetSubTracksSchema(teamDB); err != nil {
		teamDB.Close()
		return nil, err
	}
	return teamDB, nil
}

// requireMCPScope is a route middleware asserting the authenticated API key's
// scopes include "mcp". It must run behind AuthMiddleware.
func requireMCPScope(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKeyID, ok := GetAPIKeyID(c)
		if !ok {
			errorResponse(c, http.StatusUnauthorized, "unauthorized", "missing api key context")
			c.Abort()
			return
		}
		var scopesJSON string
		err := database.QueryRowContext(c.Request.Context(), `SELECT scopes FROM api_keys WHERE id = ?`, apiKeyID).Scan(&scopesJSON)
		if err != nil {
			errorResponse(c, http.StatusUnauthorized, "unauthorized", "api key not found")
			c.Abort()
			return
		}
		var scopes []string
		if json.Unmarshal([]byte(scopesJSON), &scopes) != nil {
			errorResponse(c, http.StatusForbidden, "forbidden", "api key scopes unreadable")
			c.Abort()
			return
		}
		for _, scope := range scopes {
			if scope == "mcp" {
				c.Next()
				return
			}
		}
		errorResponse(c, http.StatusForbidden, "forbidden", "api key scopes do not include 'mcp'")
		c.Abort()
	}
}

// ---------------------------------------------------------------------------
// POST /api/mcp/memory/search
// ---------------------------------------------------------------------------

type memorySearchRequest struct {
	Query          string `json:"query"`
	TeamID         string `json:"team_id"`
	IdentityCardID string `json:"identity_card_id"`
	Limit          int    `json:"limit"`
	Layer          string `json:"layer"`
}

func (h *MCPHandler) HandleMemorySearch(c *gin.Context) {
	var req memorySearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		errorResponse(c, http.StatusBadRequest, "invalid_request", "query is required")
		return
	}
	teamID, err := h.resolveTeam(c, req.TeamID)
	if err != nil {
		errorResponse(c, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	teamDB, err := h.openTeamDB(teamID)
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer teamDB.Close()

	result, err := retrieval.Search(c.Request.Context(), teamDB, retrieval.Request{
		TeamID:         teamID,
		IdentityCardID: req.IdentityCardID,
		Query:          req.Query,
		Limit:          req.Limit,
		TokenBudget:    3000,
	})
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	items := make([]gin.H, 0, len(result.Items))
	for _, candidate := range result.Items {
		if req.Layer != "" && !strings.EqualFold(candidate.Layer, req.Layer) {
			continue
		}
		items = append(items, gin.H{
			"asset_id":         candidate.ID,
			"asset_type":       candidate.AssetType,
			"layer":            candidate.Layer,
			"summary":          candidate.Summary,
			"visibility":       candidate.Visibility,
			"source_event_ids": candidate.SourceEventIDs,
			"score":            roundScore(candidate.Score),
			"version":          candidate.Version,
			"snippet":          candidate.Snippet,
		})
	}
	c.JSON(http.StatusOK, gin.H{"results": items, "truncated": result.Truncated})
}

// ---------------------------------------------------------------------------
// POST /api/mcp/memory/get
// ---------------------------------------------------------------------------

type memoryGetRequest struct {
	AssetID string `json:"asset_id"`
	TeamID  string `json:"team_id"`
}

func (h *MCPHandler) HandleMemoryGet(c *gin.Context) {
	var req memoryGetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.AssetID = strings.TrimSpace(req.AssetID)
	if req.AssetID == "" {
		errorResponse(c, http.StatusBadRequest, "invalid_request", "asset_id is required")
		return
	}
	teamID, err := h.resolveTeam(c, req.TeamID)
	if err != nil {
		errorResponse(c, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	teamDB, err := h.openTeamDB(teamID)
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer teamDB.Close()

	asset, err := db.GetAsset(c.Request.Context(), teamDB, teamID, req.AssetID)
	if err != nil {
		errorResponse(c, http.StatusNotFound, "not_found", "asset not found")
		return
	}
	subject := acl.Subject{TeamID: teamID}
	allowed, err := acl.CanRead(c.Request.Context(), teamDB, acl.Resource{
		ID: asset.ID, TeamID: asset.TeamID, IdentityCardID: asset.IdentityCardID, Visibility: asset.Visibility,
	}, subject)
	if err != nil || !allowed {
		errorResponse(c, http.StatusNotFound, "not_found", "asset not found")
		return
	}

	body := ""
	if asset.BodyPath != "" {
		abs, pathErr := paths.SafeJoin(h.memoryRoot, asset.BodyPath)
		if pathErr != nil {
			h.auditPathViolation(c, asset.BodyPath, pathErr)
			errorResponse(c, http.StatusForbidden, "path_not_allowed", "asset body path is outside the memory root")
			return
		}
		if data, readErr := os.ReadFile(abs); readErr == nil {
			body = string(data)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"asset_id":         asset.ID,
		"asset_type":       asset.AssetType,
		"name":             asset.Name,
		"summary":          asset.Summary,
		"body":             body,
		"body_path":        asset.BodyPath,
		"source_event_ids": asset.SourceEventIDs,
		"confidence":       asset.Confidence,
		"visibility":       asset.Visibility,
		"version":          asset.Version,
		"status":           asset.Status,
		"created_at":       asset.UpdatedAt,
	})
}

// ---------------------------------------------------------------------------
// POST /api/mcp/memory/append  (the only write tool)
// ---------------------------------------------------------------------------

type memoryAppendRequest struct {
	TeamID     string   `json:"team_id"`
	Content    string   `json:"content"`
	SourceIDs  []string `json:"source_ids"`
	Confidence float64  `json:"confidence"`
	Visibility string   `json:"visibility"`
}

func (h *MCPHandler) HandleMemoryAppend(c *gin.Context) {
	var req memoryAppendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		errorResponse(c, http.StatusBadRequest, "invalid_request", "content is required")
		return
	}
	teamID, err := h.resolveTeam(c, req.TeamID)
	if err != nil {
		errorResponse(c, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	teamDB, err := h.openTeamDB(teamID)
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer teamDB.Close()

	confidence := req.Confidence
	if confidence <= 0 || confidence > 1 {
		confidence = 0.8
	}
	visibility := req.Visibility
	if visibility == "" {
		visibility = acl.VisibilityPrivate
	}
	name := truncateRunes(req.Content, 64)
	// Deterministic slug derived from the content so that identical appends are
	// deduplicated by the transaction layer (same slug + summary = no-op).
	digest := sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(req.Content), " "))))
	slug := "l1-" + hex.EncodeToString(digest[:])[:16]
	apiKeyID, _ := GetAPIKeyID(c)

	asset := db.Asset{
		ID:             idgen.NewID(),
		TeamID:         teamID,
		AssetType:      "l1",
		Name:           name,
		Slug:           slug,
		Summary:        truncateRunes(req.Content, 512),
		SourceEventIDs: req.SourceIDs,
		Confidence:     confidence,
		Status:         "candidate",
		Visibility:     visibility,
		Version:        1,
	}
	// CreateL1Asset runs through the same SQLite transaction layer as the L1
	// refinement worker (dedup check -> BEGIN -> insert asset + version -> commit).
	// It never writes a Markdown file directly.
	created, err := db.CreateL1Asset(c.Request.Context(), teamDB, asset, apiKeyID)
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !created {
		errorResponse(c, http.StatusConflict, "conflict", "duplicate memory already exists")
		return
	}
	if h.embeddingQueue != nil {
		_ = embedding.Enqueue(context.WithoutCancel(c.Request.Context()), h.embeddingQueue, teamID, asset.ID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	c.JSON(http.StatusCreated, gin.H{
		"asset_id":   asset.ID,
		"asset_type": "l1",
		"version":    asset.Version,
		"status":     "candidate",
		"created_at": now,
	})
}

// ---------------------------------------------------------------------------
// POST /api/mcp/wiki/search
// ---------------------------------------------------------------------------

type wikiSearchRequest struct {
	Query  string `json:"query"`
	TeamID string `json:"team_id"`
	Limit  int    `json:"limit"`
}

func (h *MCPHandler) HandleWikiSearch(c *gin.Context) {
	var req wikiSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		errorResponse(c, http.StatusBadRequest, "invalid_request", "query is required")
		return
	}
	teamID, err := h.resolveTeam(c, req.TeamID)
	if err != nil {
		errorResponse(c, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	teamDB, err := h.openTeamDB(teamID)
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer teamDB.Close()

	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	ctx := c.Request.Context()

	type wikiHit struct {
		ID, Title, Slug, Content string
	}
	hits := make(map[string]wikiHit)

	// FTS5 MATCH first; fall back to a LIKE scan when the query is not FTS
	// friendly (e.g. CJK phrases that unicode61 tokenizes away) or when the
	// FTS pass finds nothing.
	if ftsRows, err := teamDB.QueryContext(ctx, `SELECT wp.id, wp.title, wp.slug, wp.content_md
		FROM wiki_fts wf JOIN wiki_pages wp ON wp.id = wf.page_id
		WHERE wiki_fts MATCH ? LIMIT ?`, escapeFTSQuery(req.Query), limit); err == nil {
		for ftsRows.Next() {
			var hit wikiHit
			if err := ftsRows.Scan(&hit.ID, &hit.Title, &hit.Slug, &hit.Content); err == nil {
				hits[hit.ID] = hit
			}
		}
		ftsRows.Close()
	}
	if len(hits) == 0 {
		like := "%" + escapeLikePattern(req.Query) + "%"
		if likeRows, err := teamDB.QueryContext(ctx, `SELECT id, title, slug, content_md FROM wiki_pages
			WHERE title LIKE ? ESCAPE '\' OR content_md LIKE ? ESCAPE '\' LIMIT ?`, like, like, limit); err == nil {
			for likeRows.Next() {
				var hit wikiHit
				if err := likeRows.Scan(&hit.ID, &hit.Title, &hit.Slug, &hit.Content); err == nil {
					hits[hit.ID] = hit
				}
			}
			likeRows.Close()
		}
	}

	results := make([]gin.H, 0, len(hits))
	for _, hit := range hits {
		linkRows, linkErr := teamDB.QueryContext(ctx, `SELECT to_page_id FROM wiki_edges WHERE from_page_id = ?`, hit.ID)
		var links []string
		if linkErr == nil {
			for linkRows.Next() {
				var to string
				if linkRows.Scan(&to) == nil {
					links = append(links, to)
				}
			}
			linkRows.Close()
		}
		results = append(results, gin.H{
			"page_id": hit.ID,
			"title":   hit.Title,
			"slug":    hit.Slug,
			"snippet": truncateRunes(hit.Content, 256),
			"score":   roundScore(0.8),
			"links":   links,
		})
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// ---------------------------------------------------------------------------
// POST /api/mcp/codegraph/impact
// ---------------------------------------------------------------------------

type codeGraphImpactRequest struct {
	Symbol   string `json:"symbol"`
	FilePath string `json:"file_path"`
	RepoID   string `json:"repo_id"`
	Depth    int    `json:"depth"`
	TeamID   string `json:"team_id"`
}

type impactNode struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	Depth  int    `json:"depth"`
}

func (h *MCPHandler) HandleCodeGraphImpact(c *gin.Context) {
	var req codeGraphImpactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.RepoID = strings.TrimSpace(req.RepoID)
	req.Symbol = strings.TrimSpace(req.Symbol)
	if req.RepoID == "" {
		errorResponse(c, http.StatusBadRequest, "invalid_request", "repo_id is required")
		return
	}
	if req.Symbol == "" && strings.TrimSpace(req.FilePath) == "" {
		errorResponse(c, http.StatusBadRequest, "invalid_request", "symbol or file_path is required")
		return
	}
	teamID, err := h.resolveTeam(c, req.TeamID)
	if err != nil {
		errorResponse(c, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	teamDB, err := h.openTeamDB(teamID)
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer teamDB.Close()

	depth := req.Depth
	if depth <= 0 {
		depth = 3
	}
	root, err := db.FindSymbolInRepo(c.Request.Context(), teamDB, req.RepoID, req.FilePath, req.Symbol)
	if err != nil {
		errorResponse(c, http.StatusNotFound, "not_found", "symbol not found in repo")
		return
	}
	ctx := c.Request.Context()
	callers := h.impactTraverse(ctx, teamDB, root.ID, "callers", depth)
	callees := h.impactTraverse(ctx, teamDB, root.ID, "callees", depth)

	affected := map[string]bool{root.FilePath: true}
	for _, node := range append(append([]impactNode{}, callers...), callees...) {
		if node.File != "" {
			affected[node.File] = true
		}
	}
	affectedFiles := make([]string, 0, len(affected))
	for file := range affected {
		affectedFiles = append(affectedFiles, file)
	}
	sort.Strings(affectedFiles)

	c.JSON(http.StatusOK, gin.H{
		"root":           gin.H{"symbol": root.Name, "file": root.FilePath, "kind": root.Kind},
		"callers":        callers,
		"callees":        callees,
		"affected_files": affectedFiles,
	})
}

func (h *MCPHandler) impactTraverse(ctx context.Context, teamDB *sql.DB, rootID, direction string, depth int) []impactNode {
	type frontierItem struct {
		id    string
		level int
	}
	seen := map[string]bool{rootID: true}
	frontier := []frontierItem{{id: rootID, level: 1}}
	nodes := make([]impactNode, 0)
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		if cur.level > depth {
			continue
		}
		neighbors, err := db.ImpactNeighbors(ctx, teamDB, cur.id, direction, 1)
		if err != nil {
			continue
		}
		for _, neighbor := range neighbors {
			if seen[neighbor.ID] {
				continue
			}
			seen[neighbor.ID] = true
			nodes = append(nodes, impactNode{Symbol: neighbor.Name, File: neighbor.FilePath, Depth: cur.level})
			frontier = append(frontier, frontierItem{id: neighbor.ID, level: cur.level + 1})
		}
	}
	return nodes
}

// ---------------------------------------------------------------------------
// POST /api/mcp/skill/search
// ---------------------------------------------------------------------------

type skillSearchRequest struct {
	Query   string `json:"query"`
	Status  string `json:"status"`
	Version string `json:"version"`
	TeamID  string `json:"team_id"`
}

func (h *MCPHandler) HandleSkillSearch(c *gin.Context) {
	var req skillSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	teamID, err := h.resolveTeam(c, req.TeamID)
	if err != nil {
		errorResponse(c, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	teamDB, err := h.openTeamDB(teamID)
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer teamDB.Close()

	skills, err := db.ListSkills(c.Request.Context(), teamDB, teamID, req.Status, 100)
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	needle := strings.ToLower(req.Query)
	results := make([]gin.H, 0, len(skills))
	for _, skill := range skills {
		if req.Query != "" {
			haystack := strings.ToLower(skill.Name + " " + skill.DisplayName + " " + skill.TriggerBoundary)
			if !strings.Contains(haystack, needle) {
				continue
			}
		}
		if req.Version != "" && skill.Version != req.Version {
			continue
		}
		steps := skill.Steps
		if len(steps) > 3 {
			steps = steps[:3]
		}
		results = append(results, gin.H{
			"skill_id":         skill.ID,
			"name":             skill.Name,
			"display_name":     skill.DisplayName,
			"version":          skill.Version,
			"status":           skill.Status,
			"trigger_boundary": skill.TriggerBoundary,
			"steps_summary":    steps,
			"source_ids":       skill.SourceIDs,
		})
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// ---------------------------------------------------------------------------
// POST /api/mcp/binding/get
// ---------------------------------------------------------------------------

type bindingGetRequest struct {
	SessionID      string `json:"session_id"`
	ConversationID string `json:"conversation_id"`
}

func (h *MCPHandler) HandleBindingGet(c *gin.Context) {
	var req bindingGetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.ConversationID = strings.TrimSpace(req.ConversationID)
	if req.SessionID == "" && req.ConversationID == "" {
		errorResponse(c, http.StatusBadRequest, "invalid_request", "session_id or conversation_id is required")
		return
	}

	query := `SELECT session_id, conversation_id, COALESCE(team_id,''), COALESCE(agent_id,''), COALESCE(identity_card_id,''), COALESCE(upstream_channel_id,''), binding_version, binding_state
		FROM session_bindings WHERE `
	var args []any
	if req.ConversationID != "" {
		query += "conversation_id = ?"
		args = append(args, req.ConversationID)
	} else {
		query += "session_id = ?"
		args = append(args, req.SessionID)
	}
	query += " ORDER BY binding_version DESC LIMIT 1"

	var binding struct {
		SessionID         string `json:"session_id"`
		ConversationID    string `json:"conversation_id"`
		TeamID            string `json:"team_id"`
		AgentID           string `json:"agent_id"`
		IdentityCardID    string `json:"identity_card_id"`
		UpstreamChannelID string `json:"upstream_channel_id"`
		BindingVersion    int    `json:"binding_version"`
		BindingState      string `json:"binding_state"`
	}
	err := h.globalDB.QueryRowContext(c.Request.Context(), query, args...).Scan(
		&binding.SessionID, &binding.ConversationID, &binding.TeamID, &binding.AgentID, &binding.IdentityCardID,
		&binding.UpstreamChannelID, &binding.BindingVersion, &binding.BindingState)
	if err == sql.ErrNoRows {
		errorResponse(c, http.StatusNotFound, "not_found", "binding not found")
		return
	}
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	keyTeam, _ := GetTeamID(c)
	if keyTeam != "" && binding.TeamID != "" && keyTeam != binding.TeamID {
		errorResponse(c, http.StatusForbidden, "forbidden", "binding belongs to another team")
		return
	}
	c.JSON(http.StatusOK, binding)
}

// ---------------------------------------------------------------------------
// POST /api/mcp/assets/list
// ---------------------------------------------------------------------------

type assetsListRequest struct {
	TeamID     string `json:"team_id"`
	AssetType  string `json:"asset_type"`
	Visibility string `json:"visibility"`
	Status     string `json:"status"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
}

func (h *MCPHandler) HandleAssetsList(c *gin.Context) {
	var req assetsListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	teamID, err := h.resolveTeam(c, req.TeamID)
	if err != nil {
		errorResponse(c, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	teamDB, err := h.openTeamDB(teamID)
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer teamDB.Close()

	assets, err := db.ListAssets(c.Request.Context(), teamDB, teamID, db.AssetFilter{
		AssetType:  req.AssetType,
		Visibility: req.Visibility,
		Status:     req.Status,
		Limit:      req.Limit,
		Offset:     req.Offset,
	})
	if err != nil {
		errorResponse(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	items := make([]gin.H, 0, len(assets))
	for _, asset := range assets {
		items = append(items, gin.H{
			"asset_id":   asset.ID,
			"asset_type": asset.AssetType,
			"name":       asset.Name,
			"summary":    asset.Summary,
			"visibility": asset.Visibility,
			"status":     asset.Status,
			"version":    asset.Version,
			"updated_at": asset.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"assets": items})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func roundScore(value float64) float64 {
	return float64(int(value*100+0.5)) / 100
}

func escapeFTSQuery(query string) string {
	terms := strings.Fields(strings.ReplaceAll(query, `"`, ""))
	for i := range terms {
		terms[i] = `"` + terms[i] + `"`
	}
	return strings.Join(terms, " AND ")
}

func escapeLikePattern(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}
