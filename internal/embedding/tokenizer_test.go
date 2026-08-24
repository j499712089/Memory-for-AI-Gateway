package embedding

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	if tokenizer.kind != wordPieceKind {
		t.Skipf("tokenizer at %s is not the legacy WordPiece fixture", path)
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

func TestApprovedMultilingualUnigramDifferentialGoldens(t *testing.T) {
	path := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if path == "" {
		path = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json`
	}
	tokenizer, err := LoadTokenizer(path)
	if err != nil {
		t.Fatalf("approved tokenizer gate failed: %v", err)
	}
	if tokenizer.PadID() != 1 {
		t.Fatalf("approved tokenizer pad id = %d, want artifact contract 1", tokenizer.PadID())
	}
	if tokenizer.precompiled == nil {
		t.Fatal("approved tokenizer did not load its Precompiled charsmap")
	}
	charsmapDigest := sha256.Sum256(tokenizer.precompiled.PrecompiledCharsmap)
	t.Logf("precompiled_charsmap encoded_chars=%d decoded_bytes=%d sha256=%s", charsmapEncodedLength(path), len(tokenizer.precompiled.PrecompiledCharsmap), fmt.Sprintf("%x", charsmapDigest))
	cases := []struct {
		text string
		want []int64
	}{
		{text: "Hello, world!", want: []int64{0, 35378, 4, 8999, 38, 2}},
		{text: "你好，世界！", want: []int64{0, 6, 124084, 4, 3221, 38, 2}},
		{text: "hello world", want: []int64{0, 33600, 31, 8999, 2}},
		{text: "Café", want: []int64{0, 61427, 2}},
		{text: "foo\tbar", want: []int64{0, 5775, 31, 1909, 2}},
		{text: "foo\nbar", want: []int64{0, 5775, 31, 1909, 2}},
		{text: "NUL\x00x", want: []int64{0, 541, 11176, 3, 425, 2}},
		{text: "𠀀", want: []int64{0, 6, 3, 2}},
		{text: "🧪🧪", want: []int64{0, 6, 3, 2}},
		{text: "abc🧪xyz", want: []int64{0, 1563, 238, 3, 50878, 169, 2}},
		{text: "😀", want: []int64{0, 21119, 2}},
		{text: "é", want: []int64{0, 393, 2}},
		{text: "ＡＢＣ", want: []int64{0, 47457, 2}},
		{text: "\u0301", want: []int64{0, 3309, 2}},
		{text: "这是一个很长的中文句子用于测试截断行为。", want: []int64{0, 6, 100013, 2165, 83944, 32095, 27683, 1344, 48412, 49125, 94763, 19563, 23724, 30, 2}},
	}
	for _, tc := range cases {
		ids, mask, types, err := tokenizer.Encode(tc.text)
		if err != nil {
			t.Fatalf("Encode(%q): %v", tc.text, err)
		}
		if len(ids) != defaultSequenceLength || len(mask) != defaultSequenceLength || len(types) != defaultSequenceLength {
			t.Fatalf("Encode(%q) shape = (%d,%d,%d), want 128", tc.text, len(ids), len(mask), len(types))
		}
		for i, want := range tc.want {
			if ids[i] != want || mask[i] != 1 || types[i] != 0 {
				t.Fatalf("Encode(%q) token %d = (%d,%d,%d), want (%d,1,0)", tc.text, i, ids[i], mask[i], types[i], want)
			}
		}
		for i := len(tc.want); i < len(ids); i++ {
			if ids[i] != 1 || mask[i] != 0 || types[i] != 0 {
				t.Fatalf("Encode(%q) padding %d = (%d,%d,%d), want (1,0,0)", tc.text, i, ids[i], mask[i], types[i])
			}
		}
		t.Logf("differential golden %q -> %v", tc.text, tc.want)
	}
}

func charsmapEncodedLength(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var file tokenizerFile
	if err := json.Unmarshal(data, &file); err != nil || file.Normalizer == nil {
		return 0
	}
	return len(file.Normalizer.PrecompiledCharsmap)
}

func TestApprovedMultilingualUnigramTruncatesBeforeSeparator(t *testing.T) {
	path := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if path == "" {
		path = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json`
	}
	tokenizer, err := LoadTokenizer(path)
	if err != nil {
		t.Fatalf("approved tokenizer gate failed: %v", err)
	}
	ids, mask, _, err := tokenizer.Encode(strings.Repeat("中文文本 ", 100))
	if err != nil {
		t.Fatal(err)
	}
	if ids[0] != 0 || ids[defaultSequenceLength-1] != 2 || mask[defaultSequenceLength-1] != 1 {
		t.Fatalf("truncation must retain <s> and </s> at fixed shape")
	}
	for i := 0; i < defaultSequenceLength-1; i++ {
		if mask[i] != 1 {
			t.Fatalf("truncated token %d is not attended", i)
		}
	}
}
