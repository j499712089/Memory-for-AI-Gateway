package test

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"gateway/internal/db"
	"gateway/internal/embedding"
	"gateway/internal/retrieval"
)

func approvedEmbeddingService(t *testing.T) *embedding.Service {
	t.Helper()
	model := os.Getenv("EMBEDDING_MODEL_PATH")
	if model == "" {
		model = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\model_quantized.onnx`
	}
	tokenizer := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if tokenizer == "" {
		tokenizer = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json`
	}
	service, err := embedding.NewService(model, tokenizer)
	if err != nil {
		t.Fatalf("production embedding service unavailable: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

// TestMemoryHubE2E exercises the durable path with the production encoder:
// asset write, chunk/document encoding, persistence, restart, semantic-only
// retrieval, duplicate idempotency, Chinese query, ACL, source IDs and budget.
func TestMemoryHubE2E(t *testing.T) {
	service := approvedEmbeddingService(t)
	root := t.TempDir()
	teamsDir := filepath.Join(root, "90_运行数据", "teams")
	teamDB, err := db.OpenTeamDB(teamsDir, "team-e2e")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	asset := db.Asset{
		ID: "asset-e2e", TeamID: "team-e2e", AssetType: "l1", Name: "数据库连接配置",
		Slug: "database-connection", Summary: "数据库连接配置与连接池超时策略",
		SourceEventIDs: []string{"event-e2e-1", "event-e2e-2"}, Status: "approved", Visibility: "team",
	}
	if err := db.CreateAsset(ctx, teamDB, asset, "agent-e2e"); err != nil {
		t.Fatal(err)
	}
	created, err := db.CreateL1Asset(ctx, teamDB, db.Asset{
		ID: "duplicate-e2e", TeamID: "team-e2e", AssetType: "l1", Name: asset.Name,
		Slug: asset.Slug, Summary: asset.Summary, SourceEventIDs: asset.SourceEventIDs,
	}, "agent-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate L1 write was not idempotent")
	}

	vector, err := service.EncodeDocument(asset.Summary)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateAssetEmbedding(ctx, teamDB, asset.ID, embedding.Float32ToBytes(vector), service.ModelVersion()); err != nil {
		t.Fatal(err)
	}
	if err := teamDB.Close(); err != nil {
		t.Fatal(err)
	}

	teamDB, err = db.OpenTeamDB(teamsDir, "team-e2e")
	if err != nil {
		t.Fatal(err)
	}
	defer teamDB.Close()
	var persisted []byte
	var modelVersion string
	if err := teamDB.QueryRow(`SELECT embedding, embedding_model_version FROM assets WHERE id=?`, asset.ID).Scan(&persisted, &modelVersion); err != nil {
		t.Fatal(err)
	}
	decoded, err := embedding.BytesToFloat32(persisted)
	if err != nil || len(decoded) != embedding.Dimensions {
		t.Fatalf("persisted vector invalid: len=%d err=%v", len(decoded), err)
	}
	queryVector, err := service.Encode(asset.Summary)
	if err != nil {
		t.Fatal(err)
	}
	if similarity := cosineForE2E(decoded, queryVector); similarity < 0.9999 {
		t.Fatalf("N4 write/query vector mismatch: cosine=%f", similarity)
	}
	if modelVersion != service.ModelVersion() {
		t.Fatalf("persisted model version=%q want=%q", modelVersion, service.ModelVersion())
	}

	result, err := retrieval.SearchWithEmbedding(ctx, teamDB, service, retrieval.Request{
		TeamID: "team-e2e", Query: "数据库连接池超时", Limit: 5, TokenBudget: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) == 0 || result.Items[0].ID != asset.ID {
		t.Fatalf("Chinese semantic query missed asset: %+v", result)
	}
	if result.Items[0].SourceEventIDs[0] != "event-e2e-1" || result.UsedTokens > 100 {
		t.Fatalf("source IDs or token budget missing: %+v", result)
	}

	unauthorized, err := retrieval.SearchWithEmbedding(ctx, teamDB, service, retrieval.Request{
		TeamID: "other-team", Query: "数据库连接池超时", Limit: 5, TokenBudget: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unauthorized.Items) != 0 {
		t.Fatalf("cross-team query leaked assets: %+v", unauthorized)
	}
}

func cosineForE2E(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		normA += x * x
		normB += y * y
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / math.Sqrt(normA*normB)
}
