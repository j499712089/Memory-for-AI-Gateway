package embedding

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gateway/internal/db"
	"gateway/internal/worker"
)

func embeddingTestDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, ".runtime", "memory-gateway.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database.Global, "../../schema/schema.sql"); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	if _, err := database.Global.Exec(`INSERT OR IGNORE INTO teams (id, name, slug) VALUES ('team-1', 'Team 1', 'team-1')`); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	return database, root
}

func TestEmbeddingWorkerRegisterAndEnqueue(t *testing.T) {
	database, _ := embeddingTestDB(t)
	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	RegisterWorker(processor, nil, "")
	var registered bool
	for _, name := range processor.RegisteredQueues() {
		if name == "embedding" {
			registered = true
		}
	}
	if !registered {
		t.Fatalf("embedding handler not registered; queues=%v", processor.RegisteredQueues())
	}
	if err := Enqueue(context.Background(), queue, "team-1", "asset-1"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	var queueName, status string
	if err := database.Global.QueryRow(`SELECT queue, status FROM jobs WHERE queue='embedding'`).Scan(&queueName, &status); err != nil {
		t.Fatalf("read enqueued job: %v", err)
	}
	if queueName != "embedding" || status != "pending" {
		t.Fatalf("job = (%s,%s), want (embedding,pending)", queueName, status)
	}
}

func TestEmbeddingWorkerProcessOnce(t *testing.T) {
	model := os.Getenv("EMBEDDING_MODEL_PATH")
	tokenizer := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if model == "" {
		model = approvedModelPath
	}
	if tokenizer == "" {
		tokenizer = approvedTokenizerPath
	}
	runtimePath := os.Getenv("ONNXRUNTIME_DLL_PATH")
	if runtimePath == "" {
		runtimePath = filepath.Join(filepath.Dir(model), defaultRuntimeDLL)
	}
	for name, path := range map[string]string{"model": model, "tokenizer": tokenizer, "onnxruntime": runtimePath} {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			t.Fatalf("approved asset gate failed for %s=%q: %v", name, path, err)
		}
	}
	service, err := NewService(model, tokenizer)
	if err != nil {
		t.Fatalf("approved embedding gate failed: %v", err)
	}
	defer service.Close()

	database, root := embeddingTestDB(t)
	teamsDir := filepath.Join(root, "teams")
	teamDB, err := db.OpenTeamDB(teamsDir, "team-1")
	if err != nil {
		t.Fatalf("open team db: %v", err)
	}
	defer teamDB.Close()
	if _, err := teamDB.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, summary, status) VALUES ('asset-1', 'team-1', 'team_asset', 'Test asset', 'test-asset', 'summary text', 'draft')`); err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	RegisterWorker(processor, service, teamsDir)
	if err := Enqueue(context.Background(), queue, "team-1", "asset-1"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	processed, err := processor.ProcessOnce(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("process once: %v", err)
	}
	if !processed {
		t.Fatal("worker did not process the embedding job")
	}
	var status string
	if err := database.Global.QueryRow(`SELECT status FROM jobs WHERE queue='embedding'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "done" {
		t.Fatalf("embedding job status = %s, want done", status)
	}
	var embeddingBytes []byte
	if err := teamDB.QueryRow(`SELECT embedding FROM assets WHERE id='asset-1'`).Scan(&embeddingBytes); err != nil {
		t.Fatal(err)
	}
	if len(embeddingBytes) != Dimensions*4 {
		t.Fatalf("asset embedding bytes = %d, want %d", len(embeddingBytes), Dimensions*4)
	}
}
