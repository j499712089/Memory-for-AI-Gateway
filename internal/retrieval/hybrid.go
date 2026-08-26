package retrieval

import (
	"context"
	"database/sql"
	"log"
	"math"
	"sort"
	"sync"
	"time"

	"gateway/internal/embedding"
)

type vectorBreaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

func (b *vectorBreaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.openUntil.IsZero() || !now.Before(b.openUntil)
}
func (b *vectorBreaker) success() {
	b.mu.Lock()
	b.failures = 0
	b.openUntil = time.Time{}
	b.mu.Unlock()
}
func (b *vectorBreaker) failure(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= 3 {
		b.openUntil = now.Add(30 * time.Second)
	}
}

var hybridState struct {
	mu       sync.Mutex
	breakers map[*sql.DB]*vectorBreaker
}

func breakerFor(database *sql.DB) *vectorBreaker {
	hybridState.mu.Lock()
	defer hybridState.mu.Unlock()
	if hybridState.breakers == nil {
		hybridState.breakers = map[*sql.DB]*vectorBreaker{}
	}
	if hybridState.breakers[database] == nil {
		hybridState.breakers[database] = &vectorBreaker{}
	}
	return hybridState.breakers[database]
}

// SearchHybrid combines lexical and semantic results using reciprocal rank
// fusion. Vector results are fetched independently and then unioned with FTS;
// a lexical miss can therefore still be returned by semantic retrieval.
func SearchHybrid(ctx context.Context, database *sql.DB, query string, limit int, services ...Encoder) ([]Candidate, error) {
	if limit <= 0 {
		limit = 20
	}
	fts, err := SearchFTS(ctx, database, query, limit*5)
	if err != nil {
		return nil, err
	}
	if len(services) == 0 || services[0] == nil {
		embeddingFallback("embedding service is not configured")
		return fts, nil
	}
	service := services[0]
	b := breakerFor(database)
	if !b.allow(time.Now()) {
		embeddingFallback("vector circuit breaker is open")
		return fts, nil
	}
	queryVector, err := service.Encode(query)
	if err != nil {
		b.failure(time.Now())
		embeddingFallback(err.Error())
		return fts, nil
	}
	rows, err := database.QueryContext(ctx, `SELECT id, team_id, COALESCE(identity_card_id,''), asset_type,
		name, summary, source_event_ids, visibility, version, updated_at,
		embedding, COALESCE(embedding_model_version,'')
		FROM assets WHERE embedding IS NOT NULL`)
	if err != nil {
		b.failure(time.Now())
		embeddingFallback(err.Error())
		return fts, nil
	}
	defer rows.Close()
	vector := make([]Candidate, 0)
	for rows.Next() {
		var c Candidate
		var sourceJSON, updated, version string
		var blob []byte
		if err := rows.Scan(&c.ID, &c.TeamID, &c.IdentityCardID, &c.AssetType, &c.Name, &c.Summary,
			&sourceJSON, &c.Visibility, &c.Version, &updated, &blob, &version); err != nil {
			b.failure(time.Now())
			embeddingFallback(err.Error())
			return fts, nil
		}
		if version != "" && version != service.ModelVersion() {
			continue
		}
		values, decodeErr := embedding.BytesToFloat32(blob)
		if decodeErr != nil || len(values) != embedding.Dimensions {
			continue
		}
		c.Layer = c.AssetType
		c.Snippet = c.Summary
		c.SourceEventIDs = parseSourceIDs(sourceJSON)
		c.UpdatedAt, _ = parseTime(updated)
		c.Score = cosine(queryVector, values)
		vector = append(vector, c)
	}
	if err := rows.Err(); err != nil {
		b.failure(time.Now())
		embeddingFallback(err.Error())
		return fts, nil
	}
	b.success()
	sort.SliceStable(vector, func(i, j int) bool { return vector[i].Score > vector[j].Score })
	return fuseRanks(fts, vector, limit), nil
}

func fuseRanks(fts, vector []Candidate, limit int) []Candidate {
	type scored struct {
		c     Candidate
		score float64
	}
	m := map[string]scored{}
	for i, c := range fts {
		e := m[c.ID]
		e.c = c
		e.score += .4 / (60 + float64(i+1))
		m[c.ID] = e
	}
	for i, c := range vector {
		e := m[c.ID]
		e.c = c
		e.score += .6 / (60 + float64(i+1))
		e.c.Score = c.Score
		m[c.ID] = e
	}
	out := make([]Candidate, 0, len(m))
	for _, e := range m {
		e.c.Score = e.score
		out = append(out, e.c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].ID < out[j].ID
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		aa, bb := float64(a[i]), float64(b[i])
		dot += aa * bb
		normA += aa * aa
		normB += bb * bb
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / math.Sqrt(normA*normB)
}

func embeddingFallback(reason string) {
	log.Printf("embedding retrieval degraded to FTS: %s", reason)
}
