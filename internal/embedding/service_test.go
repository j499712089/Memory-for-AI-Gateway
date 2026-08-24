package embedding

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEncodeReturns384Dimensions(t *testing.T) {
	model := os.Getenv("EMBEDDING_MODEL_PATH")
	tokenizer := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if model == "" {
		model = approvedModelPath
	}
	if tokenizer == "" {
		tokenizer = approvedTokenizerPath
	}
	runtimePath := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	if runtimePath == "" {
		runtimePath = os.Getenv("ONNXRUNTIME_DLL_PATH")
	}
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
	if got := service.tokenizer.PadID(); got != 1 {
		t.Fatalf("approved tokenizer pad id = %d, want artifact contract 1", got)
	}
	start := time.Now()
	vector, err := service.Encode("test")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(vector) != Dimensions {
		t.Fatalf("expected %d dimensions, got %d", Dimensions, len(vector))
	}
	if service.ModelVersion() != ModelVersion {
		t.Fatalf("model version = %q, want %q", service.ModelVersion(), ModelVersion)
	}
	var norm float64
	for _, value := range vector {
		norm += float64(value * value)
	}
	if norm < 0.999 || norm > 1.001 {
		t.Fatalf("embedding norm = %f, want 1", norm)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("encode took %s, want <500ms", elapsed)
	}
	blob := Float32ToBytes(vector)
	decoded, err := BytesToFloat32(blob)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != Dimensions {
		t.Fatalf("blob round trip returned %d dimensions", len(decoded))
	}
	t.Logf("embedding dimensions=%d elapsed=%s blob_bytes=%d model_sha256=%s tokenizer_sha256=%s runtime_sha256=%s", len(vector), elapsed, len(blob), sha256File(t, model), sha256File(t, tokenizer), sha256File(t, runtimePath))
}

const (
	approvedModelPath     = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\model_quantized.onnx`
	approvedTokenizerPath = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json`
)

func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("hash %q: %v", path, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
