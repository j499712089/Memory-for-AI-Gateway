package embedding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitText(t *testing.T) {
	if got := SplitText(""); got != nil {
		t.Fatalf("SplitText(\"\") = %#v, want nil", got)
	}
	if got := SplitText("  \n\t "); got != nil {
		t.Fatalf("SplitText(whitespace) = %#v, want nil", got)
	}
	if got := SplitText("  hello world  "); len(got) != 1 || got[0] != "hello world" {
		t.Fatalf("SplitText(short) = %#v, want [hello world]", got)
	}
	long := strings.Repeat("字", 1000)
	runes := []rune(long)
	chunks := SplitText(long)
	if len(chunks) != 3 {
		t.Fatalf("SplitText(1000 runes) produced %d chunks, want 3", len(chunks))
	}
	wantChunks := []string{
		string(runes[0:480]),
		string(runes[416:896]),
		string(runes[832:1000]),
	}
	for i, want := range wantChunks {
		if chunks[i] != want {
			t.Fatalf("chunk %d = %q, want %q", i, chunks[i], want)
		}
	}
	if got := []rune(chunks[1])[:defaultChunkOverlap]; string(got) != string(runes[480-defaultChunkOverlap:480]) {
		t.Fatalf("second chunk does not overlap the first by %d runes", defaultChunkOverlap)
	}
	// Chunks cover the whole input without gaps.
	covered := len([]rune(chunks[0]))
	for i := 1; i < len(chunks); i++ {
		covered += len([]rune(chunks[i])) - defaultChunkOverlap
	}
	if covered != len(runes) {
		t.Fatalf("chunks cover %d runes, want %d", covered, len(runes))
	}
}

func TestEncodeDocument(t *testing.T) {
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

	if _, err := service.EncodeDocument(""); err != ErrEmptyText {
		t.Fatalf("EncodeDocument(\"\") error = %v, want ErrEmptyText", err)
	}
	vector, err := service.EncodeDocument("  test document  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(vector) != Dimensions {
		t.Fatalf("EncodeDocument returned %d dimensions, want %d", len(vector), Dimensions)
	}
	var norm float64
	for _, value := range vector {
		norm += float64(value * value)
	}
	if norm < 0.999 || norm > 1.001 {
		t.Fatalf("EncodeDocument norm = %f, want 1", norm)
	}
}
