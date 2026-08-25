package embedding

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestServiceString(t *testing.T) {
	if got := (*Service)(nil).String(); got != "embedding service unavailable" {
		t.Fatalf("nil Service.String() = %q, want unavailable", got)
	}
	service := &Service{modelPath: `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\model_quantized.onnx`}
	if got := service.String(); got != "embedding model model_quantized.onnx" {
		t.Fatalf("Service.String() = %q, want base-name form", got)
	}
}

func TestLogEmbeddingFallback(t *testing.T) {
	var buf bytes.Buffer
	original := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(original)
	logEmbeddingFallback("model unavailable")
	if !strings.Contains(buf.String(), "embedding vector retrieval degraded to FTS: model unavailable") {
		t.Fatalf("log output = %q, want fallback message", buf.String())
	}
}
