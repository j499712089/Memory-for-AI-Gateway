package embedding

import (
	"os"
	"testing"
)

func TestTokenizerMatchesExportedEnglishWordPieceShape(t *testing.T) {
	path := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if path == "" {
		path = `F:\AI\models\all-MiniLM-L6-v2\tokenizer.json`
	}
	tokenizer, err := LoadTokenizer(path)
	if err != nil {
		t.Skipf("tokenizer unavailable: %v", err)
	}
	ids, mask, types, err := tokenizer.Encode("Hello, world!")
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{101, 7592, 1010, 2088, 999, 102}
	for i, expected := range want {
		if ids[i] != expected || mask[i] != 1 || types[i] != 0 {
			t.Fatalf("token %d = (%d,%d,%d), want (%d,1,0)", i, ids[i], mask[i], types[i], expected)
		}
	}
	if len(ids) != 128 || len(mask) != 128 || len(types) != 128 {
		t.Fatalf("fixed shape = (%d,%d,%d), want 128", len(ids), len(mask), len(types))
	}
}
