package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Asset is the team-local representation used by retrieval and API consumers.
type Asset struct {
	ID             string   `json:"asset_id"`
	TeamID         string   `json:"team_id"`
	IdentityCardID string   `json:"identity_card_id,omitempty"`
	AssetType      string   `json:"asset_type"`
	Name           string   `json:"name"`
	Slug           string   `json:"slug"`
	Summary        string   `json:"summary"`
	BodyPath       string   `json:"body_path,omitempty"`
	SourceEventIDs []string `json:"source_event_ids"`
	Confidence     float64  `json:"confidence"`
	Status         string   `json:"status"`
	Visibility     string   `json:"visibility"`
	Version        int      `json:"version"`
	UpdatedAt      string   `json:"updated_at"`
}

// AssetFilter limits a team asset listing.
type AssetFilter struct {
	AssetType  string
	Visibility string
	Status     string
	Limit      int
	Offset     int
}

// EnsureAssetsSchema creates the subset of the team schema needed by the
// memory/retrieval path. It is safe to call on every startup.
func EnsureAssetsSchema(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("asset database is nil")
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS assets (
			id TEXT PRIMARY KEY, team_id TEXT NOT NULL, identity_card_id TEXT,
			asset_type TEXT NOT NULL, name TEXT NOT NULL, slug TEXT NOT NULL,
			summary TEXT NOT NULL DEFAULT '', body_path TEXT,
			source_event_ids TEXT NOT NULL DEFAULT '[]', confidence REAL NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'draft', visibility TEXT NOT NULL DEFAULT 'private',
			version INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE(asset_type, slug, version))`,
		`CREATE INDEX IF NOT EXISTS idx_assets_team_type ON assets(team_id, asset_type, status)`,
		`CREATE INDEX IF NOT EXISTS idx_assets_visibility ON assets(team_id, visibility)`,
		`CREATE TABLE IF NOT EXISTS asset_versions (
			id TEXT PRIMARY KEY, asset_id TEXT NOT NULL REFERENCES assets(id), version INTEGER NOT NULL,
			body_path TEXT, summary TEXT, confidence REAL, source_event_ids TEXT DEFAULT '[]',
			created_by TEXT, created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE(asset_id, version))`,
		`CREATE TABLE IF NOT EXISTS acl_entries (
			id TEXT PRIMARY KEY, asset_id TEXT NOT NULL REFERENCES assets(id),
			grantee_type TEXT NOT NULL, grantee_id TEXT NOT NULL, permission TEXT NOT NULL DEFAULT 'read',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE(asset_id, grantee_type, grantee_id, permission))`,
		`CREATE INDEX IF NOT EXISTS idx_acl_grantee ON acl_entries(grantee_type, grantee_id)`,
		`CREATE TABLE IF NOT EXISTS wiki_pages (
			id TEXT PRIMARY KEY, asset_id TEXT NOT NULL UNIQUE REFERENCES assets(id), title TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE, content_md TEXT NOT NULL DEFAULT '', frontmatter_json TEXT DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'ready', built_at TEXT,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`,
		`CREATE TABLE IF NOT EXISTS wiki_edges (
			id TEXT PRIMARY KEY, from_page_id TEXT NOT NULL REFERENCES wiki_pages(id),
			to_page_id TEXT NOT NULL REFERENCES wiki_pages(id), link_type TEXT NOT NULL DEFAULT 'internal',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE(from_page_id, to_page_id, link_type))`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS wiki_fts USING fts5(page_id UNINDEXED, title, content, tokenize='unicode61 remove_diacritics 2')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("ensure assets schema: %w", err)
		}
	}
	return nil
}

// CreateAsset inserts a new asset and its first version atomically.
func CreateAsset(ctx context.Context, database *sql.DB, asset Asset, createdBy string) error {
	if database == nil {
		return fmt.Errorf("asset database is nil")
	}
	if asset.ID == "" || asset.TeamID == "" || asset.AssetType == "" || asset.Name == "" || asset.Slug == "" {
		return fmt.Errorf("asset id, team, type, name and slug are required")
	}
	if asset.Version == 0 {
		asset.Version = 1
	}
	if asset.Status == "" {
		asset.Status = "candidate"
	}
	if asset.Visibility == "" {
		asset.Visibility = "private"
	}
	sourceJSON, err := json.Marshal(asset.SourceEventIDs)
	if err != nil {
		return fmt.Errorf("marshal source event ids: %w", err)
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin asset transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO assets
		(id, team_id, identity_card_id, asset_type, name, slug, summary, body_path, source_event_ids, confidence, status, visibility, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		asset.ID, asset.TeamID, nullable(asset.IdentityCardID), asset.AssetType, asset.Name, asset.Slug,
		asset.Summary, nullable(asset.BodyPath), string(sourceJSON), asset.Confidence, asset.Status, asset.Visibility, asset.Version); err != nil {
		return fmt.Errorf("insert asset: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO asset_versions
		(id, asset_id, version, body_path, summary, confidence, source_event_ids, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, asset.ID+"-v"+fmt.Sprint(asset.Version), asset.ID, asset.Version,
		nullable(asset.BodyPath), asset.Summary, asset.Confidence, string(sourceJSON), nullable(createdBy)); err != nil {
		return fmt.Errorf("insert asset version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit asset: %w", err)
	}
	return nil
}

// ListAssets returns team-local assets. TeamID is always required to prevent
// accidental cross-team reads.
func ListAssets(ctx context.Context, database *sql.DB, teamID string, filter AssetFilter) ([]Asset, error) {
	if database == nil {
		return nil, fmt.Errorf("asset database is nil")
	}
	if strings.TrimSpace(teamID) == "" {
		return nil, fmt.Errorf("team id is required")
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := `SELECT id, team_id, COALESCE(identity_card_id,''), asset_type, name, slug, summary,
		COALESCE(body_path,''), source_event_ids, confidence, status, visibility, version, updated_at
		FROM assets WHERE team_id = ?`
	args := []any{teamID}
	if filter.AssetType != "" {
		query += " AND asset_type = ?"
		args = append(args, filter.AssetType)
	}
	if filter.Visibility != "" {
		query += " AND visibility = ?"
		args = append(args, filter.Visibility)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	query += " ORDER BY updated_at DESC, id LIMIT ? OFFSET ?"
	args = append(args, filter.Limit, filter.Offset)
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()
	var assets []Asset
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

// GetAsset returns an asset only when it belongs to teamID.
func GetAsset(ctx context.Context, database *sql.DB, teamID, assetID string) (Asset, error) {
	if database == nil {
		return Asset{}, fmt.Errorf("asset database is nil")
	}
	row := database.QueryRowContext(ctx, `SELECT id, team_id, COALESCE(identity_card_id,''), asset_type, name, slug, summary,
		COALESCE(body_path,''), source_event_ids, confidence, status, visibility, version, updated_at
		FROM assets WHERE id = ? AND team_id = ?`, assetID, teamID)
	asset, err := scanAsset(row)
	if err != nil {
		return Asset{}, fmt.Errorf("get asset: %w", err)
	}
	return asset, nil
}

func scanAsset(scanner interface{ Scan(...any) error }) (Asset, error) {
	var asset Asset
	var sourceJSON string
	if err := scanner.Scan(&asset.ID, &asset.TeamID, &asset.IdentityCardID, &asset.AssetType, &asset.Name,
		&asset.Slug, &asset.Summary, &asset.BodyPath, &sourceJSON, &asset.Confidence, &asset.Status,
		&asset.Visibility, &asset.Version, &asset.UpdatedAt); err != nil {
		return Asset{}, err
	}
	if sourceJSON != "" {
		_ = json.Unmarshal([]byte(sourceJSON), &asset.SourceEventIDs)
	}
	return asset, nil
}

// AddACL grants permission to a subject on a team-local asset.
func AddACL(ctx context.Context, database *sql.DB, id, assetID, granteeType, granteeID, permission string) error {
	if database == nil {
		return fmt.Errorf("asset database is nil")
	}
	if id == "" || assetID == "" || granteeType == "" || granteeID == "" {
		return fmt.Errorf("ACL id, asset, grantee type and grantee id are required")
	}
	if permission == "" {
		permission = "read"
	}
	_, err := database.ExecContext(ctx, `INSERT OR IGNORE INTO acl_entries (id, asset_id, grantee_type, grantee_id, permission) VALUES (?, ?, ?, ?, ?)`, id, assetID, granteeType, granteeID, permission)
	return err
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// AssetUpdatedAt parses the timestamp for deterministic retrieval ordering.
func AssetUpdatedAt(asset Asset) time.Time {
	value, _ := time.Parse(time.RFC3339Nano, asset.UpdatedAt)
	return value
}

// ---------------------------------------------------------------------------
// Phase 3b extensions (IMP-04): L1-L4 / Wiki / CodeGraph / Skill sub-tracks.
// All functions below are independent additions on top of the Phase 3a CRUD;
// they do not alter the behaviour of the functions above.
// ---------------------------------------------------------------------------

// EnsureAssetSubTracksSchema creates the additional team-side tables needed by
// the Phase 3b asset workers (CodeGraph + Skill). It is safe to call on every
// startup and complements EnsureAssetsSchema from Phase 3a.
func EnsureAssetSubTracksSchema(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("asset database is nil")
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS code_repos (
			id TEXT PRIMARY KEY, asset_id TEXT NOT NULL UNIQUE REFERENCES assets(id),
			repo_url TEXT NOT NULL, local_path TEXT NOT NULL, branch TEXT,
			head_commit TEXT, status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_code_repos_local_path ON code_repos(local_path)`,
		`CREATE TABLE IF NOT EXISTS code_files (
			id TEXT PRIMARY KEY, repo_id TEXT NOT NULL REFERENCES code_repos(id),
			path TEXT NOT NULL, file_hash TEXT NOT NULL, language TEXT,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			indexed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE (repo_id, path))`,
		`CREATE INDEX IF NOT EXISTS idx_code_files_hash ON code_files(repo_id, file_hash)`,
		`CREATE TABLE IF NOT EXISTS code_symbols (
			id TEXT PRIMARY KEY, file_id TEXT NOT NULL REFERENCES code_files(id),
			name TEXT NOT NULL, kind TEXT NOT NULL, signature TEXT DEFAULT '',
			line_start INTEGER, line_end INTEGER, symbol_hash TEXT NOT NULL,
			UNIQUE (file_id, name, kind, line_start))`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_name ON code_symbols(name)`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_file ON code_symbols(file_id)`,
		`CREATE TABLE IF NOT EXISTS code_edges (
			id TEXT PRIMARY KEY, source_symbol_id TEXT NOT NULL REFERENCES code_symbols(id),
			target_symbol_id TEXT NOT NULL REFERENCES code_symbols(id),
			edge_type TEXT NOT NULL CHECK (edge_type IN ('import','call','export','inherit','reference')),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE (source_symbol_id, target_symbol_id, edge_type))`,
		`CREATE INDEX IF NOT EXISTS idx_code_edges_target ON code_edges(target_symbol_id)`,
		`CREATE INDEX IF NOT EXISTS idx_code_edges_type ON code_edges(edge_type)`,
		`CREATE TABLE IF NOT EXISTS codegraph_cursor (
			repo_id TEXT PRIMARY KEY REFERENCES code_repos(id),
			last_indexed_commit TEXT, last_indexed_at TEXT, pending_diffs TEXT DEFAULT '[]')`,
		`CREATE TABLE IF NOT EXISTS skills (
			id TEXT PRIMARY KEY, asset_id TEXT NOT NULL UNIQUE REFERENCES assets(id),
			name TEXT NOT NULL UNIQUE, display_name TEXT, version TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'candidate' CHECK (status IN ('candidate','approved','deprecated')),
			scope TEXT NOT NULL DEFAULT 'personal' CHECK (scope IN ('personal','team')),
			trigger_boundary TEXT, steps_json TEXT NOT NULL DEFAULT '[]',
			validation_json TEXT NOT NULL DEFAULT '{}', source_ids TEXT NOT NULL DEFAULT '[]',
			resource_refs TEXT NOT NULL DEFAULT '[]', entrypoint TEXT, manifest_path TEXT,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`,
		`CREATE TABLE IF NOT EXISTS skill_versions (
			id TEXT PRIMARY KEY, skill_id TEXT NOT NULL REFERENCES skills(id),
			version TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'candidate',
			content_ref TEXT NOT NULL, source_ids TEXT DEFAULT '[]', created_by TEXT,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE (skill_id, version))`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("ensure asset sub-track schema: %w", err)
		}
	}
	return nil
}

// CreateL1Asset inserts a new candidate L1 asset, skipping duplicates that
// carry the same slug and identical summary (dedup before facts land).
func CreateL1Asset(ctx context.Context, database *sql.DB, asset Asset, createdBy string) (bool, error) {
	if asset.ID == "" || asset.TeamID == "" || asset.AssetType != "l1" || asset.Name == "" || asset.Slug == "" {
		return false, fmt.Errorf("l1 asset id, team, name and slug are required")
	}
	var existing int
	err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM assets WHERE team_id=? AND asset_type=? AND slug=? AND summary=?`, asset.TeamID, asset.AssetType, asset.Slug, asset.Summary).Scan(&existing)
	if err != nil {
		return false, fmt.Errorf("check duplicate l1 asset: %w", err)
	}
	if existing > 0 {
		return false, nil
	}
	if err := CreateAsset(ctx, database, asset, createdBy); err != nil {
		// A concurrent refiner may win the same slug between the duplicate
		// check and the insert. Re-read before surfacing a false failure.
		var duplicate int
		if queryErr := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM assets WHERE team_id=? AND asset_type=? AND slug=? AND summary=?`, asset.TeamID, asset.AssetType, asset.Slug, asset.Summary).Scan(&duplicate); queryErr == nil && duplicate > 0 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ListAssetsBySlug returns every version of a (team, type, slug) asset ordered
// by version ascending. It is the basis for L4 version-conflict detection.
func ListAssetsBySlug(ctx context.Context, database *sql.DB, teamID, assetType, slug string) ([]Asset, error) {
	if strings.TrimSpace(teamID) == "" || strings.TrimSpace(slug) == "" {
		return nil, fmt.Errorf("team id and slug are required")
	}
	rows, err := database.QueryContext(ctx, `SELECT id, team_id, COALESCE(identity_card_id,''), asset_type, name, slug, summary,
		COALESCE(body_path,''), source_event_ids, confidence, status, visibility, version, updated_at
		FROM assets WHERE team_id = ? AND asset_type = ? AND slug = ? ORDER BY version ASC`, teamID, assetType, slug)
	if err != nil {
		return nil, fmt.Errorf("list assets by slug: %w", err)
	}
	defer rows.Close()
	var assets []Asset
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

// CountPromotionEvidence counts the number of distinct source events attached
// to existing assets whose slug matches the promotion topic. An optional list
// of explicit evidence ids is taken into account for caller-supplied proof.
func CountPromotionEvidence(ctx context.Context, database *sql.DB, teamID, slug string, explicitEventIDs []string) (int, error) {
	if database == nil {
		return 0, fmt.Errorf("evidence database is nil")
	}
	seen := make(map[string]bool)
	for _, id := range explicitEventIDs {
		if id != "" {
			seen[id] = true
		}
	}
	rows, err := database.QueryContext(ctx, `SELECT source_event_ids FROM assets WHERE team_id=? AND slug=? AND asset_type IN ('l1','l2','l3')`, teamID, slug)
	if err != nil {
		return 0, fmt.Errorf("count promotion evidence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return 0, err
		}
		var ids []string
		if json.Unmarshal([]byte(raw), &ids) == nil {
			for _, id := range ids {
				if id != "" {
					seen[id] = true
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return len(seen), nil
}

// PromoteAsset promotes an asset to a higher layer. Version conflicts never
// overwrite: an existing (team, type, slug) row creates the next version.
// Idempotent when the same summary already exists at the current max version.
func PromoteAsset(ctx context.Context, database *sql.DB, asset Asset, createdBy string) (Asset, error) {
	if asset.ID == "" || asset.TeamID == "" || asset.Name == "" || asset.Slug == "" {
		return Asset{}, fmt.Errorf("promoted asset id, team, name and slug are required")
	}
	existing, err := ListAssetsBySlug(ctx, database, asset.TeamID, asset.AssetType, asset.Slug)
	if err != nil {
		return Asset{}, err
	}
	if len(existing) > 0 {
		latest := existing[len(existing)-1]
		if latest.Summary == asset.Summary {
			return latest, nil // idempotent re-promotion
		}
		asset.Version = latest.Version + 1
	}
	if asset.Version == 0 {
		asset.Version = 1
	}
	if asset.Status == "" {
		asset.Status = "promoted"
	}
	if asset.Visibility == "" {
		asset.Visibility = "team"
	}
	if err := CreateAsset(ctx, database, asset, createdBy); err != nil {
		return Asset{}, err
	}
	return asset, nil
}

// IdentityCard mirrors the global identity_cards table for L3 promotion.
type IdentityCard struct {
	ID               string
	TeamID           string
	AgentID          string
	Name             string
	Role             string
	Responsibilities string
	Boundaries       string
	AllowedTools     []string
	Style            string
	Visibility       string
	Version          int
	Status           string
	SourceEventIDs   []string
	BodyPath         string
}

// UpsertIdentityCard creates or updates (version+1) a global identity card.
// Existing cards are never mutated in place: a changed card bumps the version.
func UpsertIdentityCard(ctx context.Context, database *sql.DB, card IdentityCard) (string, error) {
	if database == nil {
		return "", fmt.Errorf("identity card database is nil")
	}
	if card.ID == "" {
		card.ID = "card-" + time.Now().UTC().Format("20060102150405") + "-" + fmt.Sprint(time.Now().UnixNano())
	}
	if card.Visibility == "" {
		card.Visibility = "agent"
	}
	if card.Status == "" {
		card.Status = "active"
	}
	if card.Version == 0 {
		card.Version = 1
	}
	toolsJSON, err := json.Marshal(card.AllowedTools)
	if err != nil {
		return "", err
	}
	sourceJSON, err := json.Marshal(card.SourceEventIDs)
	if err != nil {
		return "", err
	}
	var currentVersion int
	err = database.QueryRowContext(ctx, `SELECT version FROM identity_cards WHERE id=?`, card.ID).Scan(&currentVersion)
	switch {
	case err == sql.ErrNoRows:
		_, err = database.ExecContext(ctx, `INSERT INTO identity_cards
			(id, team_id, agent_id, name, role, responsibilities, boundaries, allowed_tools, style, visibility, version, status, source_event_ids, body_path)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			card.ID, nullable(card.TeamID), nullable(card.AgentID), card.Name, card.Role, card.Responsibilities,
			card.Boundaries, string(toolsJSON), card.Style, card.Visibility, card.Version, card.Status,
			string(sourceJSON), nullable(card.BodyPath))
		if err != nil {
			return "", fmt.Errorf("insert identity card: %w", err)
		}
		return card.ID, nil
	case err != nil:
		return "", err
	default:
		next := currentVersion + 1
		_, err = database.ExecContext(ctx, `UPDATE identity_cards SET version=?, name=?, role=?, responsibilities=?, boundaries=?, allowed_tools=?, style=?, visibility=?, status=?, source_event_ids=?, body_path=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`,
			next, card.Name, card.Role, card.Responsibilities, card.Boundaries, string(toolsJSON), card.Style,
			card.Visibility, card.Status, string(sourceJSON), nullable(card.BodyPath), card.ID)
		if err != nil {
			return "", fmt.Errorf("update identity card: %w", err)
		}
		return card.ID, nil
	}
}

// GetIdentityCard returns a single global identity card by id.
func GetIdentityCard(ctx context.Context, database *sql.DB, cardID string) (IdentityCard, error) {
	var card IdentityCard
	var toolsJSON, sourceJSON string
	err := database.QueryRowContext(ctx, `SELECT id, COALESCE(team_id,''), COALESCE(agent_id,''), name, role,
		COALESCE(responsibilities,''), COALESCE(boundaries,''), COALESCE(allowed_tools,'[]'), COALESCE(style,''),
		visibility, version, status, COALESCE(source_event_ids,'[]'), COALESCE(body_path,'')
		FROM identity_cards WHERE id=?`, cardID).Scan(&card.ID, &card.TeamID, &card.AgentID, &card.Name, &card.Role,
		&card.Responsibilities, &card.Boundaries, &toolsJSON, &card.Style, &card.Visibility, &card.Version,
		&card.Status, &sourceJSON, &card.BodyPath)
	if err != nil {
		return IdentityCard{}, err
	}
	_ = json.Unmarshal([]byte(toolsJSON), &card.AllowedTools)
	_ = json.Unmarshal([]byte(sourceJSON), &card.SourceEventIDs)
	return card, nil
}

// WikiPage mirrors the wiki_pages row used by the wiki_build worker.
type WikiPage struct {
	ID              string
	AssetID         string
	Title           string
	Slug            string
	ContentMD       string
	FrontmatterJSON string
	Status          string
}

// UpsertWikiPage inserts or updates a wiki page by slug (idempotent).
func UpsertWikiPage(ctx context.Context, database *sql.DB, page WikiPage) (string, error) {
	if database == nil {
		return "", fmt.Errorf("wiki database is nil")
	}
	if page.AssetID == "" || page.Slug == "" || page.Title == "" {
		return "", fmt.Errorf("wiki page asset id, slug and title are required")
	}
	if page.ID == "" {
		page.ID = "wiki-" + page.Slug
	}
	if page.Status == "" {
		page.Status = "ready"
	}
	if page.FrontmatterJSON == "" {
		page.FrontmatterJSON = "{}"
	}
	_, err := database.ExecContext(ctx, `INSERT INTO wiki_pages (id, asset_id, title, slug, content_md, frontmatter_json, status, built_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(slug) DO UPDATE SET asset_id=excluded.asset_id, title=excluded.title, content_md=excluded.content_md,
		frontmatter_json=excluded.frontmatter_json, status=excluded.status, built_at=excluded.built_at,
		updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		page.ID, nullable(page.AssetID), page.Title, page.Slug, page.ContentMD, page.FrontmatterJSON, page.Status)
	if err != nil {
		return "", fmt.Errorf("upsert wiki page: %w", err)
	}
	return page.ID, nil
}

// GetWikiPageBySlug returns one wiki page.
func GetWikiPageBySlug(ctx context.Context, database *sql.DB, slug string) (WikiPage, error) {
	var page WikiPage
	err := database.QueryRowContext(ctx, `SELECT id, COALESCE(asset_id,''), title, slug, content_md, COALESCE(frontmatter_json,'{}'), status FROM wiki_pages WHERE slug=?`, slug).
		Scan(&page.ID, &page.AssetID, &page.Title, &page.Slug, &page.ContentMD, &page.FrontmatterJSON, &page.Status)
	if err != nil {
		return WikiPage{}, err
	}
	return page, nil
}

// ListWikiPages returns all wiki pages (used for FTS rebuilds and full builds).
func ListWikiPages(ctx context.Context, database *sql.DB, limit int) ([]WikiPage, error) {
	if database == nil {
		return nil, fmt.Errorf("wiki database is nil")
	}
	if limit <= 0 || limit > 10000 {
		limit = 5000
	}
	rows, err := database.QueryContext(ctx, `SELECT id, COALESCE(asset_id,''), title, slug, content_md, COALESCE(frontmatter_json,'{}'), status FROM wiki_pages ORDER BY slug LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pages []WikiPage
	for rows.Next() {
		var page WikiPage
		if err := rows.Scan(&page.ID, &page.AssetID, &page.Title, &page.Slug, &page.ContentMD, &page.FrontmatterJSON, &page.Status); err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	return pages, rows.Err()
}

// UpdateWikiEdges resolves link targets to page ids and records wiki_edges.
// It returns the number of edges written (duplicates are ignored).
func UpdateWikiEdges(ctx context.Context, database *sql.DB, fromPageID string, targetSlugs []string) (int, error) {
	if database == nil {
		return 0, fmt.Errorf("wiki database is nil")
	}
	if fromPageID == "" {
		return 0, fmt.Errorf("from page id is required")
	}
	written := 0
	for _, target := range targetSlugs {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		page, err := GetWikiPageBySlug(ctx, database, target)
		if err != nil {
			if err == sql.ErrNoRows {
				continue // dangling link tolerated at build time
			}
			return written, err
		}
		if page.ID == fromPageID {
			continue
		}
		result, err := database.ExecContext(ctx, `INSERT OR IGNORE INTO wiki_edges (id, from_page_id, to_page_id, link_type) VALUES (?, ?, ?, 'internal')`,
			"edge-"+fromPageID+"-"+page.ID, fromPageID, page.ID)
		if err != nil {
			return written, err
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			written++
		}
	}
	return written, nil
}

// RebuildWikiFTS drops and repopulates the FTS5 index from wiki_pages.
func RebuildWikiFTS(ctx context.Context, database *sql.DB) (int, error) {
	if database == nil {
		return 0, fmt.Errorf("wiki database is nil")
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM wiki_fts`); err != nil {
		return 0, fmt.Errorf("clear wiki fts: %w", err)
	}
	pages, err := ListWikiPages(ctx, database, 10000)
	if err != nil {
		return 0, err
	}
	for _, page := range pages {
		if _, err := database.ExecContext(ctx, `INSERT INTO wiki_fts (page_id, title, content) VALUES (?, ?, ?)`, page.ID, page.Title, page.ContentMD); err != nil {
			return 0, fmt.Errorf("rebuild wiki fts: %w", err)
		}
	}
	return len(pages), nil
}

// CodeRepo mirrors the code_repos row used by the codegraph worker.
type CodeRepo struct {
	ID         string
	AssetID    string
	RepoURL    string
	LocalPath  string
	Branch     string
	HeadCommit string
	Status     string
}

// GetOrCreateCodeRepo inserts a repo (or returns the existing row by local path).
func GetOrCreateCodeRepo(ctx context.Context, database *sql.DB, repo CodeRepo) (CodeRepo, error) {
	if database == nil {
		return CodeRepo{}, fmt.Errorf("codegraph database is nil")
	}
	if repo.LocalPath == "" {
		return CodeRepo{}, fmt.Errorf("code repo local path is required")
	}
	if repo.ID == "" {
		repo.ID = "repo-" + repo.LocalPath
	}
	if repo.Status == "" {
		repo.Status = "pending"
	}
	var existing CodeRepo
	err := database.QueryRowContext(ctx, `SELECT id, COALESCE(asset_id,''), COALESCE(repo_url,''), local_path, COALESCE(branch,''), COALESCE(head_commit,''), status FROM code_repos WHERE local_path=?`, repo.LocalPath).
		Scan(&existing.ID, &existing.AssetID, &existing.RepoURL, &existing.LocalPath, &existing.Branch, &existing.HeadCommit, &existing.Status)
	if err == nil {
		if repo.AssetID != "" {
			existing.AssetID = repo.AssetID
		}
		if repo.RepoURL != "" {
			existing.RepoURL = repo.RepoURL
		}
		if repo.Branch != "" {
			existing.Branch = repo.Branch
		}
		if repo.HeadCommit != "" {
			existing.HeadCommit = repo.HeadCommit
		}
		if repo.Status != "" {
			existing.Status = repo.Status
		}
		_, err = database.ExecContext(ctx, `UPDATE code_repos SET asset_id=?, repo_url=?, branch=?, head_commit=?, status=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, nullable(existing.AssetID), existing.RepoURL, nullable(existing.Branch), nullable(existing.HeadCommit), existing.Status, existing.ID)
		if err != nil {
			return CodeRepo{}, fmt.Errorf("update code repo: %w", err)
		}
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return CodeRepo{}, fmt.Errorf("query code repo: %w", err)
	}
	_, err = database.ExecContext(ctx, `INSERT INTO code_repos (id, asset_id, repo_url, local_path, branch, head_commit, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, repo.ID, nullable(repo.AssetID), repo.RepoURL, repo.LocalPath, nullable(repo.Branch), nullable(repo.HeadCommit), repo.Status)
	if err != nil {
		return CodeRepo{}, fmt.Errorf("insert code repo: %w", err)
	}
	return repo, nil
}

// GetCodeRepoByID loads one repo row.
func GetCodeRepoByID(ctx context.Context, database *sql.DB, repoID string) (CodeRepo, error) {
	var repo CodeRepo
	err := database.QueryRowContext(ctx, `SELECT id, COALESCE(asset_id,''), COALESCE(repo_url,''), local_path, COALESCE(branch,''), COALESCE(head_commit,''), status FROM code_repos WHERE id=?`, repoID).
		Scan(&repo.ID, &repo.AssetID, &repo.RepoURL, &repo.LocalPath, &repo.Branch, &repo.HeadCommit, &repo.Status)
	if err != nil {
		return CodeRepo{}, err
	}
	return repo, nil
}

// CodeFile mirrors the code_files row.
type CodeFile struct {
	ID        string
	RepoID    string
	Path      string
	FileHash  string
	Language  string
	SizeBytes int
}

// GetOrCreateCodeFile inserts a file row when it does not exist yet.
// Returns the file id and whether a new row was created.
func GetOrCreateCodeFile(ctx context.Context, database *sql.DB, file CodeFile) (string, bool, error) {
	if database == nil {
		return "", false, fmt.Errorf("codegraph database is nil")
	}
	if file.RepoID == "" || file.Path == "" || file.FileHash == "" {
		return "", false, fmt.Errorf("code file repo, path and hash are required")
	}
	if file.ID == "" {
		file.ID = "file-" + file.RepoID + "-" + file.Path
	}
	var existing string
	err := database.QueryRowContext(ctx, `SELECT id FROM code_files WHERE repo_id=? AND path=?`, file.RepoID, file.Path).Scan(&existing)
	if err == nil {
		// hash unchanged -> skip reindex (the incremental worker relies on this)
		if existing != "" {
			var hash string
			_ = database.QueryRowContext(ctx, `SELECT file_hash FROM code_files WHERE id=?`, existing).Scan(&hash)
			if hash == file.FileHash {
				return existing, false, nil
			}
		}
		_, err := database.ExecContext(ctx, `UPDATE code_files SET file_hash=?, language=?, size_bytes=?, indexed_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`,
			file.FileHash, nullable(file.Language), file.SizeBytes, existing)
		return existing, true, err
	}
	if err != sql.ErrNoRows {
		return "", false, err
	}
	_, err = database.ExecContext(ctx, `INSERT INTO code_files (id, repo_id, path, file_hash, language, size_bytes) VALUES (?, ?, ?, ?, ?, ?)`,
		file.ID, file.RepoID, file.Path, file.FileHash, nullable(file.Language), file.SizeBytes)
	if err != nil {
		return "", false, fmt.Errorf("insert code file: %w", err)
	}
	return file.ID, true, nil
}

// CodeSymbol mirrors a code_symbols row; CodeEdge a code_edges row.
type CodeSymbol struct {
	ID         string
	FileID     string
	Name       string
	Kind       string
	Signature  string
	LineStart  int
	LineEnd    int
	SymbolHash string
}

type CodeEdge struct {
	ID             string
	SourceSymbolID string
	TargetSymbolID string
	EdgeType       string
}

// ReplaceFileSymbols rewrites the symbols and edges of one file atomically.
// It is used when a file changed (hash mismatch) during incremental indexing.
func ReplaceFileSymbols(ctx context.Context, database *sql.DB, fileID string, symbols []CodeSymbol, edges []CodeEdge) error {
	if database == nil {
		return fmt.Errorf("codegraph database is nil")
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldIDs []string
	rows, err := tx.QueryContext(ctx, `SELECT id FROM code_symbols WHERE file_id=?`, fileID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		oldIDs = append(oldIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range oldIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM code_edges WHERE source_symbol_id=? OR target_symbol_id=?`, id, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM code_symbols WHERE id=?`, id); err != nil {
			return err
		}
	}
	for _, symbol := range symbols {
		if symbol.ID == "" {
			symbol.ID = "sym-" + fileID + "-" + symbol.Name
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO code_symbols (id, file_id, name, kind, signature, line_start, line_end, symbol_hash)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, symbol.ID, fileID, symbol.Name, symbol.Kind, symbol.Signature, symbol.LineStart, symbol.LineEnd, symbol.SymbolHash); err != nil {
			return err
		}
	}
	for _, edge := range edges {
		if edge.ID == "" {
			edge.ID = "edge-" + edge.SourceSymbolID + "-" + edge.TargetSymbolID + "-" + edge.EdgeType
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO code_edges (id, source_symbol_id, target_symbol_id, edge_type) VALUES (?, ?, ?, ?)`,
			edge.ID, edge.SourceSymbolID, edge.TargetSymbolID, edge.EdgeType); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CodeGraphCursor mirrors the codegraph_cursor row.
type CodeGraphCursor struct {
	RepoID            string
	LastIndexedCommit string
	LastIndexedAt     string
	PendingDiffs      []string
}

// GetCodeGraphCursor loads the cursor for a repo (empty when absent).
func GetCodeGraphCursor(ctx context.Context, database *sql.DB, repoID string) (CodeGraphCursor, error) {
	var cursor CodeGraphCursor
	var diffsJSON string
	err := database.QueryRowContext(ctx, `SELECT repo_id, COALESCE(last_indexed_commit,''), COALESCE(last_indexed_at,''), COALESCE(pending_diffs,'[]') FROM codegraph_cursor WHERE repo_id=?`, repoID).
		Scan(&cursor.RepoID, &cursor.LastIndexedCommit, &cursor.LastIndexedAt, &diffsJSON)
	if err == sql.ErrNoRows {
		return CodeGraphCursor{RepoID: repoID}, nil
	}
	if err != nil {
		return CodeGraphCursor{}, err
	}
	_ = json.Unmarshal([]byte(diffsJSON), &cursor.PendingDiffs)
	return cursor, nil
}

// SetCodeGraphCursor persists the incremental cursor (upsert).
func SetCodeGraphCursor(ctx context.Context, database *sql.DB, cursor CodeGraphCursor) error {
	diffsJSON, err := json.Marshal(cursor.PendingDiffs)
	if err != nil {
		return err
	}
	_, err = database.ExecContext(ctx, `INSERT INTO codegraph_cursor (repo_id, last_indexed_commit, last_indexed_at, pending_diffs)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(repo_id) DO UPDATE SET last_indexed_commit=excluded.last_indexed_commit, last_indexed_at=excluded.last_indexed_at, pending_diffs=excluded.pending_diffs`,
		cursor.RepoID, nullable(cursor.LastIndexedCommit), nullable(cursor.LastIndexedAt), string(diffsJSON))
	return err
}

// SymbolRef is a symbol joined with its file path for impact traversal.
type SymbolRef struct {
	CodeSymbol
	FilePath string
	RepoID   string
}

// FindSymbolByFile locates a symbol by name within one file of a repo.
func FindSymbolByFile(ctx context.Context, database *sql.DB, repoID, filePath, name string) (SymbolRef, error) {
	row := database.QueryRowContext(ctx, `SELECT s.id, s.file_id, s.name, s.kind, COALESCE(s.signature,''), COALESCE(s.line_start,0), COALESCE(s.line_end,0), s.symbol_hash, f.path, f.repo_id
		FROM code_symbols s JOIN code_files f ON f.id = s.file_id
		WHERE f.repo_id = ? AND f.path = ? AND s.name = ? LIMIT 1`, repoID, filePath, name)
	var ref SymbolRef
	err := row.Scan(&ref.ID, &ref.FileID, &ref.Name, &ref.Kind, &ref.Signature, &ref.LineStart, &ref.LineEnd, &ref.SymbolHash, &ref.FilePath, &ref.RepoID)
	if err != nil {
		return SymbolRef{}, err
	}
	return ref, nil
}

// FindSymbolInRepo locates a symbol by name across the whole repo (returns the
// first match, preferring the given file path when provided).
func FindSymbolInRepo(ctx context.Context, database *sql.DB, repoID, filePath, name string) (SymbolRef, error) {
	if filePath != "" {
		if ref, err := FindSymbolByFile(ctx, database, repoID, filePath, name); err == nil {
			return ref, nil
		}
	}
	row := database.QueryRowContext(ctx, `SELECT s.id, s.file_id, s.name, s.kind, COALESCE(s.signature,''), COALESCE(s.line_start,0), COALESCE(s.line_end,0), s.symbol_hash, f.path, f.repo_id
		FROM code_symbols s JOIN code_files f ON f.id = s.file_id
		WHERE f.repo_id = ? AND s.name = ? LIMIT 1`, repoID, name)
	var ref SymbolRef
	err := row.Scan(&ref.ID, &ref.FileID, &ref.Name, &ref.Kind, &ref.Signature, &ref.LineStart, &ref.LineEnd, &ref.SymbolHash, &ref.FilePath, &ref.RepoID)
	if err != nil {
		return SymbolRef{}, err
	}
	return ref, nil
}

// ImpactNeighbors returns symbols connected to symbolID over edges of the
// given direction ('callers' = inbound, 'callees' = outbound) with file info.
func ImpactNeighbors(ctx context.Context, database *sql.DB, symbolID, direction string, depth int) ([]SymbolRef, error) {
	if database == nil {
		return nil, fmt.Errorf("codegraph database is nil")
	}
	if strings.TrimSpace(symbolID) == "" {
		return nil, fmt.Errorf("symbol id is required")
	}
	if direction != "callers" && direction != "callees" {
		return nil, fmt.Errorf("impact direction must be callers or callees")
	}
	if depth <= 0 {
		depth = 3
	}
	seen := map[string]bool{symbolID: true}
	frontier := []string{symbolID}
	var result []SymbolRef
	for level := 0; level < depth && len(frontier) > 0; level++ {
		nextFrontier := make([]string, 0)
		for _, next := range frontier {
			var rows *sql.Rows
			var err error
			if direction == "callers" {
				rows, err = database.QueryContext(ctx, `SELECT s.id, s.file_id, s.name, s.kind, COALESCE(s.signature,''), COALESCE(s.line_start,0), COALESCE(s.line_end,0), s.symbol_hash, f.path, f.repo_id
					FROM code_edges e JOIN code_symbols s ON s.id = e.source_symbol_id JOIN code_files f ON f.id = s.file_id
					WHERE e.target_symbol_id = ? AND e.edge_type IN ('call','import','reference')`, next)
			} else {
				rows, err = database.QueryContext(ctx, `SELECT s.id, s.file_id, s.name, s.kind, COALESCE(s.signature,''), COALESCE(s.line_start,0), COALESCE(s.line_end,0), s.symbol_hash, f.path, f.repo_id
					FROM code_edges e JOIN code_symbols s ON s.id = e.target_symbol_id JOIN code_files f ON f.id = s.file_id
					WHERE e.source_symbol_id = ? AND e.edge_type IN ('call','import','reference')`, next)
			}
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var ref SymbolRef
				if err := rows.Scan(&ref.ID, &ref.FileID, &ref.Name, &ref.Kind, &ref.Signature, &ref.LineStart, &ref.LineEnd, &ref.SymbolHash, &ref.FilePath, &ref.RepoID); err != nil {
					rows.Close()
					return nil, err
				}
				if !seen[ref.ID] {
					seen[ref.ID] = true
					nextFrontier = append(nextFrontier, ref.ID)
					result = append(result, ref)
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
		frontier = nextFrontier
	}
	return result, nil
}

// Skill mirrors a skills row.
type Skill struct {
	ID              string
	AssetID         string
	Name            string
	DisplayName     string
	Version         string
	Status          string
	Scope           string
	TriggerBoundary string
	Steps           []string
	Validation      map[string]any
	SourceIDs       []string
	ResourceRefs    []string
	Entrypoint      string
	ManifestPath    string
}

// GetSkillByName loads a skill row by its stable kebab-case name.
func GetSkillByName(ctx context.Context, database *sql.DB, teamID, name string) (Skill, error) {
	if database == nil {
		return Skill{}, fmt.Errorf("skill database is nil")
	}
	var row *sql.Row
	if strings.TrimSpace(teamID) == "" {
		row = database.QueryRowContext(ctx, `SELECT id, COALESCE(asset_id,''), name, COALESCE(display_name,''), version, status,
			scope, COALESCE(trigger_boundary,''), COALESCE(steps_json,'[]'), COALESCE(validation_json,'{}'),
			COALESCE(source_ids,'[]'), COALESCE(resource_refs,'[]'), COALESCE(entrypoint,''), COALESCE(manifest_path,'')
			FROM skills WHERE name=?`, name)
	} else {
		row = database.QueryRowContext(ctx, `SELECT sk.id, COALESCE(sk.asset_id,''), sk.name, COALESCE(sk.display_name,''), sk.version, sk.status,
			sk.scope, COALESCE(sk.trigger_boundary,''), COALESCE(sk.steps_json,'[]'), COALESCE(sk.validation_json,'{}'),
			COALESCE(sk.source_ids,'[]'), COALESCE(sk.resource_refs,'[]'), COALESCE(sk.entrypoint,''), COALESCE(sk.manifest_path,'')
			FROM skills sk JOIN assets a ON a.id=sk.asset_id WHERE sk.name=? AND a.team_id=?`, name, teamID)
	}
	return scanSkill(row)
}

// ListSkills returns skills for a team with optional status filter.
func ListSkills(ctx context.Context, database *sql.DB, teamID, status string, limit int) ([]Skill, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT sk.id, COALESCE(sk.asset_id,''), sk.name, COALESCE(sk.display_name,''), sk.version, sk.status,
		sk.scope, COALESCE(sk.trigger_boundary,''), COALESCE(sk.steps_json,'[]'), COALESCE(sk.validation_json,'{}'),
		COALESCE(sk.source_ids,'[]'), COALESCE(sk.resource_refs,'[]'), COALESCE(sk.entrypoint,''), COALESCE(sk.manifest_path,'')
		FROM skills sk JOIN assets a ON a.id = sk.asset_id WHERE a.team_id = ?`
	args := []any{teamID}
	if status != "" {
		query += " AND sk.status = ?"
		args = append(args, status)
	}
	query += " ORDER BY sk.name LIMIT ?"
	args = append(args, limit)
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var skills []Skill
	for rows.Next() {
		skill, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}
	return skills, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanSkill(scanner rowScanner) (Skill, error) {
	var skill Skill
	var stepsJSON, validationJSON, sourceJSON, resourceJSON string
	if err := scanner.Scan(&skill.ID, &skill.AssetID, &skill.Name, &skill.DisplayName, &skill.Version, &skill.Status,
		&skill.Scope, &skill.TriggerBoundary, &stepsJSON, &validationJSON, &sourceJSON, &resourceJSON,
		&skill.Entrypoint, &skill.ManifestPath); err != nil {
		return Skill{}, err
	}
	_ = json.Unmarshal([]byte(stepsJSON), &skill.Steps)
	_ = json.Unmarshal([]byte(validationJSON), &skill.Validation)
	_ = json.Unmarshal([]byte(sourceJSON), &skill.SourceIDs)
	_ = json.Unmarshal([]byte(resourceJSON), &skill.ResourceRefs)
	return skill, nil
}

// UpsertSkill inserts a skill or bumps it to the next version when the same
// name already exists. The previous version row is always preserved.
func UpsertSkill(ctx context.Context, database *sql.DB, skill Skill, createdBy string) (Skill, error) {
	if database == nil {
		return Skill{}, fmt.Errorf("skill database is nil")
	}
	if skill.Name == "" || skill.Version == "" {
		return Skill{}, fmt.Errorf("skill name and version are required")
	}
	if skill.ID == "" {
		skill.ID = "skill-" + skill.Name
	}
	if skill.AssetID == "" {
		return Skill{}, fmt.Errorf("skill asset id is required")
	}
	if skill.Scope == "" {
		skill.Scope = "team"
	}
	if skill.Status == "" {
		skill.Status = "candidate"
	}
	stepsJSON, _ := json.Marshal(skill.Steps)
	validationJSON, _ := json.Marshal(skill.Validation)
	sourceJSON, _ := json.Marshal(skill.SourceIDs)
	resourceJSON, _ := json.Marshal(skill.ResourceRefs)
	existing, err := GetSkillByName(ctx, database, "", skill.Name)
	if err != nil && err != sql.ErrNoRows {
		return Skill{}, err
	}
	if existing.ID != "" {
		skill.ID = existing.ID
		skill.AssetID = existing.AssetID
		skill.Version = existing.Version
		if skill.Status == "candidate" {
			skill.Status = existing.Status
		}
		_, err = database.ExecContext(ctx, `UPDATE skills SET display_name=?, status=?, trigger_boundary=?, steps_json=?, validation_json=?,
			source_ids=?, resource_refs=?, entrypoint=?, manifest_path=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`,
			nullable(skill.DisplayName), skill.Status, nullable(skill.TriggerBoundary), string(stepsJSON), string(validationJSON),
			string(sourceJSON), string(resourceJSON), nullable(skill.Entrypoint), nullable(skill.ManifestPath), skill.ID)
		if err != nil {
			return Skill{}, fmt.Errorf("update skill: %w", err)
		}
		return skill, nil
	}
	_, err = database.ExecContext(ctx, `INSERT INTO skills (id, asset_id, name, display_name, version, status, scope, trigger_boundary,
		steps_json, validation_json, source_ids, resource_refs, entrypoint, manifest_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		skill.ID, skill.AssetID, skill.Name, nullable(skill.DisplayName), skill.Version, skill.Status, skill.Scope,
		nullable(skill.TriggerBoundary), string(stepsJSON), string(validationJSON), string(sourceJSON),
		string(resourceJSON), nullable(skill.Entrypoint), nullable(skill.ManifestPath))
	if err != nil {
		return Skill{}, fmt.Errorf("insert skill: %w", err)
	}
	return skill, nil
}

// SkillVersion mirrors a skill_versions row.
type SkillVersion struct {
	ID         string
	SkillID    string
	Version    string
	Status     string
	ContentRef string
	SourceIDs  []string
	CreatedBy  string
}

// CreateSkillVersion records an immutable skill version row.
func CreateSkillVersion(ctx context.Context, database *sql.DB, version SkillVersion) error {
	sourceJSON, _ := json.Marshal(version.SourceIDs)
	_, err := database.ExecContext(ctx, `INSERT OR IGNORE INTO skill_versions (id, skill_id, version, status, content_ref, source_ids, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, version.ID, version.SkillID, version.Version, version.Status, version.ContentRef,
		string(sourceJSON), nullable(version.CreatedBy))
	return err
}

// GitBatch mirrors the global git_batches row.
type GitBatch struct {
	ID        string
	Status    string
	Files     []string
	CommitSHA string
	Message   string
	Error     string
}

// CreateGitBatch inserts a pending git batch. It is idempotent: a retried
// git_commit job reuses the batch row created by its first attempt instead of
// tripping the primary key.
func CreateGitBatch(ctx context.Context, database *sql.DB, batch GitBatch) error {
	if batch.ID == "" {
		batch.ID = "batch-" + time.Now().UTC().Format("20060102150405") + "-" + fmt.Sprint(time.Now().UnixNano())
	}
	if batch.Status == "" {
		batch.Status = "pending"
	}
	filesJSON, _ := json.Marshal(batch.Files)
	_, err := database.ExecContext(ctx, `INSERT OR IGNORE INTO git_batches (id, status, files_json, message, created_at) VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		batch.ID, batch.Status, string(filesJSON), nullable(batch.Message))
	return err
}

// MarkGitBatchCommitted records the commit sha of a finished batch.
func MarkGitBatchCommitted(ctx context.Context, database *sql.DB, batchID, sha, message string) error {
	_, err := database.ExecContext(ctx, `UPDATE git_batches SET status='committed', commit_sha=?, message=?, committed_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), error=NULL WHERE id=?`,
		sha, message, batchID)
	return err
}

// MarkGitBatchFailed records a failed batch for observability.
func MarkGitBatchFailed(ctx context.Context, database *sql.DB, batchID, reason string) error {
	_, err := database.ExecContext(ctx, `UPDATE git_batches SET status='failed', error=?, committed_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, reason, batchID)
	return err
}

// ListGitBatches returns recent batches for the health panel.
func ListGitBatches(ctx context.Context, database *sql.DB, limit int) ([]GitBatch, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := database.QueryContext(ctx, `SELECT id, status, files_json, COALESCE(commit_sha,''), COALESCE(message,''), COALESCE(error,'') FROM git_batches ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batches []GitBatch
	for rows.Next() {
		var batch GitBatch
		var filesJSON string
		if err := rows.Scan(&batch.ID, &batch.Status, &filesJSON, &batch.CommitSHA, &batch.Message, &batch.Error); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(filesJSON), &batch.Files)
		batches = append(batches, batch)
	}
	return batches, rows.Err()
}
