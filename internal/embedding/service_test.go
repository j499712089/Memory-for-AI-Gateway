package embedding

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEncodeReturns384DimensionsUnder50ms(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "model.onnx")
	tokenizer := filepath.Join(dir, "tokenizer.json")
	if err := os.WriteFile(model, []byte("onnx"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenizer, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(model, tokenizer)
	if err != nil {
		t.Fatal(err)
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
	if elapsed >= 50*time.Millisecond {
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
