package retrieval

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"gateway/internal/acl"
	"gateway/internal/embedding"
)

type Request struct {
	TeamID         string
	AgentID        string
	UserID         string
	Role           string
	IdentityCardID string
	Query          string
	Limit          int
	TokenBudget    int
	Timeout        time.Duration
}

type Encoder interface {
	Encode(string) ([]float32, error)
	ModelVersion() string
}

type Pipeline struct {
	database  *sql.DB
	embedding Encoder
}

func NewPipeline(database *sql.DB, services ...Encoder) *Pipeline {
	var service Encoder
	if len(services) > 0 {
		service = services[0]
	}
	return &Pipeline{database: database, embedding: service}
}

func (p *Pipeline) Search(ctx context.Context, request Request) (Result, error) {
	if p == nil || p.database == nil {
		return Result{}, fmt.Errorf("retrieval pipeline database is nil")
	}
	if request.TeamID == "" {
		return Result{}, fmt.Errorf("team id is required")
	}
	if request.Timeout <= 0 {
		request.Timeout = 300 * time.Millisecond
	}
	searchCtx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	searchLimit := request.Limit * 5
	if searchLimit <= 0 {
		searchLimit = 50
	}
	if searchLimit > 200 {
		searchLimit = 200
	}
	candidates, err := SearchHybrid(searchCtx, p.database, request.Query, searchLimit, p.embedding)
	if err != nil {
		return Result{}, err
	}
	// When the query produces no exact hits (or there is no query text at
	// all), fall back to the team's most recent assets so the LLM path still
	// injects current team context instead of an empty package (V2.1/V2.4:
	// 按需回退 L1/L0). The recency query is team-scoped and the ACL filter
	// below still applies.
	if len(candidates) == 0 {
		recent, recentErr := recentAssets(searchCtx, p.database, request.TeamID, searchLimit)
		if recentErr != nil {
			return Result{}, recentErr
		}
		candidates = recent
	}
	subject := acl.Subject{TeamID: request.TeamID, AgentID: request.AgentID, UserID: request.UserID, Role: request.Role, IdentityCardID: request.IdentityCardID}
	visible := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		allowed, err := acl.CanRead(searchCtx, p.database, acl.Resource{ID: candidate.ID, TeamID: candidate.TeamID, IdentityCardID: candidate.IdentityCardID, Visibility: candidate.Visibility}, subject)
		if err != nil {
			return Result{}, err
		}
		if allowed {
			visible = append(visible, candidate)
		}
	}
	deadline, _ := searchCtx.Deadline()
	return Fuse(visible, Limits{MaxResults: request.Limit, MaxTokens: request.TokenBudget, Deadline: deadline}), nil
}

func Search(ctx context.Context, database *sql.DB, request Request) (Result, error) {
	return NewPipeline(database).Search(ctx, request)
}

func SearchWithEmbedding(ctx context.Context, database *sql.DB, service *embedding.Service, request Request) (Result, error) {
	return NewPipeline(database, service).Search(ctx, request)
}
