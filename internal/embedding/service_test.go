package embedding

import (
	"os"
	"testing"
	"time"
)

func TestEncodeReturns384DimensionsUnder50ms(t *testing.T) {
	model := os.Getenv("EMBEDDING_MODEL_PATH")
	tokenizer := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if model == "" {
		model = `F:\AI\models\all-MiniLM-L6-v2\model_quantized.onnx`
	}
	if tokenizer == "" {
		tokenizer = `F:\AI\models\all-MiniLM-L6-v2\tokenizer.json`
	}
	if _, err := os.Stat(model); err != nil {
		t.Skipf("embedding model unavailable: %v", err)
	}
	if _, err := os.Stat(tokenizer); err != nil {
		t.Skipf("embedding tokenizer unavailable: %v", err)
	}
	service, err := NewService(model, tokenizer)
	if err != nil {
		t.Skipf("embedding runtime unavailable: %v", err)
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
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("encode took %s", elapsed)
	}
	blob := Float32ToBytes(vector)
	decoded, err := BytesToFloat32(blob)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != Dimensions {
		t.Fatalf("blob round trip returned %d dimensions", len(decoded))
	}
	t.Logf("embedding dimensions=%d elapsed=%s blob_bytes=%d", len(vector), elapsed, len(blob))
}
