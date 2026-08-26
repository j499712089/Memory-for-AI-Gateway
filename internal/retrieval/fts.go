package retrieval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SearchFTS uses the wiki FTS5 index when present and always provides a
// LIKE-based asset fallback, which also covers L1-L4 assets without a second
// virtual table migration.
func SearchFTS(ctx context.Context, database *sql.DB, query string, limit int) ([]Candidate, error) {
	if database == nil {
		return nil, fmt.Errorf("retrieval database is nil")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	likeQuery := "%" + escapeLIKE(query) + "%"
	rows, err := database.QueryContext(ctx, `SELECT id, team_id, COALESCE(identity_card_id,''), asset_type, name, summary, source_event_ids, visibility, version, updated_at
		FROM assets WHERE (name LIKE ? ESCAPE '\' OR summary LIKE ? ESCAPE '\' OR COALESCE(body_path,'') LIKE ? ESCAPE '\')
		AND status NOT IN ('archived','deprecated') ORDER BY updated_at DESC LIMIT ?`, likeQuery, likeQuery, likeQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("search assets: %w", err)
	}
	defer rows.Close()
	var candidates []Candidate
	for rows.Next() {
		var c Candidate
		var sourceJSON, updated string
		if err := rows.Scan(&c.ID, &c.TeamID, &c.IdentityCardID, &c.AssetType, &c.Name, &c.Summary, &sourceJSON, &c.Visibility, &c.Version, &updated); err != nil {
			return nil, err
		}
		c.Layer = c.AssetType
		c.Snippet = c.Summary
		c.Score = score(query, c.Name, c.Summary)
		c.UpdatedAt, _ = parseTime(updated)
		c.SourceEventIDs = parseSourceIDs(sourceJSON)
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Wiki FTS is optional in older team databases. Query it only when the
	// virtual table exists and merge page hits into the same result set.
	var ftsExists int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='wiki_fts'`).Scan(&ftsExists); err == nil && ftsExists > 0 {
		ftsRows, ftsErr := database.QueryContext(ctx, `SELECT wf.page_id, COALESCE(a.team_id,''), COALESCE(a.identity_card_id,''), COALESCE(a.visibility,''), wf.title, wf.content,
			COALESCE(a.source_event_ids,'[]'), COALESCE(a.version,1)
			FROM wiki_fts wf
			INNER JOIN wiki_pages wp ON wp.id = wf.page_id
			INNER JOIN assets a ON a.id = wp.asset_id
			WHERE wiki_fts MATCH ?
			AND wp.status = 'ready'
			AND a.status NOT IN ('archived','deprecated')
			LIMIT ?`, escapeFTS(query), limit)
		if ftsErr == nil {
			defer ftsRows.Close()
			for ftsRows.Next() {
				var id, teamID, identityCardID, visibility, title, content, sourceJSON string
				var version int
				if err := ftsRows.Scan(&id, &teamID, &identityCardID, &visibility, &title, &content, &sourceJSON, &version); err != nil {
					return nil, err
				}
				candidates = append(candidates, Candidate{ID: id, TeamID: teamID, IdentityCardID: identityCardID, AssetType: "wiki", Layer: "wiki", Name: title, Summary: content, Snippet: content, Visibility: visibility, SourceEventIDs: parseSourceIDs(sourceJSON), Score: 0.8, Version: version})
			}
			if err := ftsRows.Err(); err != nil {
				return nil, err
			}
		}
	}
	return candidates, nil
}

// escapeLIKE quotes the wildcard characters in a user query. Without this,
// a query containing '%' or '_' silently turns the retrieval into a broad
// table scan and can bypass the caller's intended relevance filter.
func escapeLIKE(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func escapeFTS(query string) string {
	terms := strings.Fields(strings.ReplaceAll(query, `"`, ""))
	for i := range terms {
		terms[i] = `"` + terms[i] + `"`
	}
	return strings.Join(terms, " AND ")
}

func score(query, name, summary string) float64 {
	q := strings.ToLower(query)
	value := strings.ToLower(name + " " + summary)
	score := 0.1
	if strings.Contains(strings.ToLower(name), q) {
		score += 0.6
	}
	if strings.Contains(value, q) {
		score += 0.3
	}
	return score
}

func parseTime(value string) (t time.Time, _ error) { return time.Parse(time.RFC3339Nano, value) }

func parseSourceIDs(value string) []string {
	var ids []string
	_ = json.Unmarshal([]byte(value), &ids)
	return ids
}

// recentAssets returns the team's most recently updated assets as a retrieval
// fallback when a query has no exact hits. Layer ordering is applied later by
// Fuse (L4→L3→L2→L1→L0 priority weighting), and ACL filtering runs in
// Pipeline.Search after this function returns.
func recentAssets(ctx context.Context, database *sql.DB, teamID string, limit int) ([]Candidate, error) {
	if database == nil {
		return nil, fmt.Errorf("retrieval database is nil")
	}
	if teamID == "" {
		return nil, fmt.Errorf("team id is required")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := database.QueryContext(ctx, `SELECT id, team_id, COALESCE(identity_card_id,''), asset_type, name, summary, source_event_ids, visibility, version, updated_at
		FROM assets WHERE team_id = ? AND status NOT IN ('archived','deprecated') ORDER BY updated_at DESC, id DESC LIMIT ?`, teamID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent assets: %w", err)
	}
	defer rows.Close()
	var candidates []Candidate
	for rows.Next() {
		var c Candidate
		var sourceJSON, updated string
		if err := rows.Scan(&c.ID, &c.TeamID, &c.IdentityCardID, &c.AssetType, &c.Name, &c.Summary, &sourceJSON, &c.Visibility, &c.Version, &updated); err != nil {
			return nil, err
		}
		c.Layer = c.AssetType
		c.Snippet = c.Summary
		c.Score = 0.1
		c.UpdatedAt, _ = parseTime(updated)
		c.SourceEventIDs = parseSourceIDs(sourceJSON)
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return candidates, nil
}
