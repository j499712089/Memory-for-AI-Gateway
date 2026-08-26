package retrieval

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"gateway/internal/acl"
	"gateway/internal/embedding"
	_ "modernc.org/sqlite"
)

type testEncoder struct {
	vectors map[string][]float32
	err     error
	calls   int
}

func (e *testEncoder) Encode(text string) ([]float32, error) {
	e.calls++
	if e.err != nil {
		return nil, e.err
	}
	return e.vectors[text], nil
}
func (*testEncoder) ModelVersion() string { return embedding.ModelVersion }

func retrievalDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:retrieval-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	_, err = database.Exec(`CREATE TABLE assets (
		id TEXT PRIMARY KEY, team_id TEXT NOT NULL, identity_card_id TEXT,
		asset_type TEXT NOT NULL, name TEXT NOT NULL, slug TEXT NOT NULL,
		summary TEXT NOT NULL DEFAULT '', body_path TEXT,
		source_event_ids TEXT NOT NULL DEFAULT '[]', visibility TEXT NOT NULL DEFAULT 'team',
		version INTEGER NOT NULL DEFAULT 1, updated_at TEXT NOT NULL,
		embedding BLOB, embedding_model_version TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`CREATE TABLE acl_entries (
		id TEXT PRIMARY KEY, asset_id TEXT NOT NULL, grantee_type TEXT NOT NULL,
		grantee_id TEXT NOT NULL, permission TEXT NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func testVector(first float32, second float32) []float32 {
	vector := make([]float32, embedding.Dimensions)
	vector[0], vector[1] = first, second
	return vector
}

func insertCandidate(t *testing.T, database *sql.DB, id, team, visibility, summary string, vector []float32) {
	t.Helper()
	_, err := database.Exec(`INSERT INTO assets
		(id, team_id, asset_type, name, slug, summary, source_event_ids, visibility, version, updated_at, embedding, embedding_model_version)
		VALUES (?, ?, 'l2', ?, ?, ?, '[]', ?, 1, ?, ?, ?)`,
		id, team, id, id, summary, visibility, time.Now().UTC().Format(time.RFC3339Nano), embedding.Float32ToBytes(vector), embedding.ModelVersion)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSearchHybridReturnsPureVectorHitAndFusionUnion(t *testing.T) {
	database := retrievalDB(t)
	insertCandidate(t, database, "semantic", "team-1", acl.VisibilityTeam, "unrelated lexical text", testVector(1, 0))
	insertCandidate(t, database, "lexical", "team-1", acl.VisibilityTeam, "project alpha", testVector(0, 1))
	encoder := &testEncoder{vectors: map[string][]float32{"project": testVector(1, 0)}}

	got, err := SearchHybrid(context.Background(), database, "project", 10, encoder)
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool)
	for _, candidate := range got {
		ids[candidate.ID] = true
	}
	if !ids["semantic"] || !ids["lexical"] {
		t.Fatalf("hybrid search lost union members: %#v", got)
	}
	if !ids["semantic"] {
		t.Fatalf("pure vector hit was not retained in the fused result: %#v", got)
	}
}

func TestSearchHybridIgnoresMalformedAndWrongDimensionVectors(t *testing.T) {
	database := retrievalDB(t)
	insertCandidate(t, database, "good", "team-1", acl.VisibilityTeam, "fallback phrase", testVector(1, 0))
	_, err := database.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, summary, source_event_ids, visibility, version, updated_at, embedding, embedding_model_version)
		VALUES ('bad-bytes', 'team-1', 'l2', 'bad-bytes', 'bad-bytes', 'fallback phrase', '[]', 'team', 1, ?, X'0102', ?)`, time.Now().UTC().Format(time.RFC3339Nano), embedding.ModelVersion)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, summary, source_event_ids, visibility, version, updated_at, embedding, embedding_model_version)
		VALUES ('bad-dim', 'team-1', 'l2', 'bad-dim', 'bad-dim', 'fallback phrase', '[]', 'team', 1, ?, ?, ?)`, time.Now().UTC().Format(time.RFC3339Nano), embedding.Float32ToBytes([]float32{1}), embedding.ModelVersion)
	if err != nil {
		t.Fatal(err)
	}
	encoder := &testEncoder{vectors: map[string][]float32{"no-match": {1, 0}}}
	got, err := SearchHybrid(context.Background(), database, "no-match", 10, encoder)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range got {
		if strings.HasPrefix(candidate.ID, "bad-") {
			t.Fatalf("malformed vector was returned: %#v", candidate)
		}
	}
}

func TestSearchHybridEncoderFailureFallsBackAndOpensBreaker(t *testing.T) {
	database := retrievalDB(t)
	insertCandidate(t, database, "lexical", "team-1", acl.VisibilityTeam, "fallback phrase", testVector(1, 0))
	encoder := &testEncoder{err: errors.New("encoder unavailable")}
	for i := 0; i < 3; i++ {
		got, err := SearchHybrid(context.Background(), database, "fallback", 10, encoder)
		if err != nil || len(got) == 0 || got[0].ID != "lexical" {
			t.Fatalf("call %d did not fall back to FTS: got=%#v err=%v", i+1, got, err)
		}
	}
	calls := encoder.calls
	encoder.err = nil
	encoder.vectors = map[string][]float32{"fallback": testVector(1, 0)}
	if _, err := SearchHybrid(context.Background(), database, "fallback", 10, encoder); err != nil {
		t.Fatal(err)
	}
	if encoder.calls != calls {
		t.Fatalf("open breaker still called encoder: before=%d after=%d", calls, encoder.calls)
	}
}

func TestPipelineFiltersACLAndEnforcesTokenBudget(t *testing.T) {
	database := retrievalDB(t)
	insertCandidate(t, database, "private", "team-1", acl.VisibilityPrivate, "private secret", testVector(1, 0))
	insertCandidate(t, database, "team", "team-1", acl.VisibilityTeam, "visible memory", testVector(1, 0))
	insertCandidate(t, database, "other-team", "team-2", acl.VisibilityTeam, "visible memory", testVector(1, 0))
	_, err := database.Exec(`UPDATE assets SET identity_card_id='card-1' WHERE id='private'`)
	if err != nil {
		t.Fatal(err)
	}
	encoder := &testEncoder{vectors: map[string][]float32{"memory": testVector(1, 0)}}
	result, err := NewPipeline(database, encoder).Search(context.Background(), Request{
		TeamID: "team-1", Query: "memory", Limit: 10, TokenBudget: 1, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.ID == "private" || item.ID == "other-team" {
			t.Fatalf("ACL/team boundary leaked item: %#v", result.Items)
		}
	}
	if !result.Truncated || len(result.Items) != 0 {
		t.Fatalf("token budget was not enforced: %#v", result)
	}

	result, err = NewPipeline(database, encoder).Search(context.Background(), Request{
		TeamID: "team-1", IdentityCardID: "card-1", Query: "memory", Limit: 10, TokenBudget: 10, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	seenPrivate := false
	for _, item := range result.Items {
		seenPrivate = seenPrivate || item.ID == "private"
	}
	if !seenPrivate {
		t.Fatalf("identity scope did not allow matching private asset: %#v", result.Items)
	}
}

func TestFuseRanksStableUnion(t *testing.T) {
	got := fuseRanks(
		[]Candidate{{ID: "shared", Score: 0.2}, {ID: "lexical-only", Score: 0.1}},
		[]Candidate{{ID: "shared", Score: 0.9}, {ID: "vector-only", Score: 0.8}}, 10,
	)
	if len(got) != 3 {
		t.Fatalf("expected union of three IDs, got %#v", got)
	}
	if got[0].ID != "shared" {
		t.Fatalf("shared result should be fused first, got %#v", got)
	}
}
