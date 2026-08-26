package embedding

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyTokenizerPath is the WordPiece reference fixture whose exported shape
// TestTokenizerMatchesExportedEnglishWordPieceShape pins. It is decoupled from
// the approved Unigram gate: EMBEDDING_LEGACY_TOKENIZER_PATH selects it, so
// pointing EMBEDDING_TOKENIZER_PATH at the approved asset no longer skips the
// shape check, and a missing or mismatched fixture fails non-zero instead of
// silently passing with exit 0.
const legacyTokenizerPath = `F:\AI\models\all-MiniLM-L6-v2\tokenizer.json`

func TestTokenizerMatchesExportedEnglishWordPieceShape(t *testing.T) {
	path := os.Getenv("EMBEDDING_LEGACY_TOKENIZER_PATH")
	if path == "" {
		path = legacyTokenizerPath
	}
	tokenizer, err := LoadTokenizer(path)
	if err != nil {
		t.Fatalf("legacy WordPiece fixture unavailable: %v", err)
	}
	if tokenizer.kind != wordPieceKind {
		t.Fatalf("tokenizer at %s is not the legacy WordPiece fixture (kind=%d)", path, tokenizer.kind)
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

// TestApprovedMultilingualUnigramHeldOutDifferentials pins the approved Unigram
// tokenizer against tokenizers 0.22.2 outputs for cases the original golden
// table never exercised: control characters, the replacement character,
// special tokens in isolation and embedded, whitespace around special tokens,
// combining marks, ZWJ sequences, and non-BMP emoji. Every expected vector was
// produced by the Python oracle and is non-skippable.
func TestApprovedMultilingualUnigramHeldOutDifferentials(t *testing.T) {
	path := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if path == "" {
		path = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json`
	}
	tokenizer, err := LoadTokenizer(path)
	if err != nil {
		t.Fatalf("approved tokenizer gate failed: %v", err)
	}
	cases := []struct {
		text string
		want []int64
	}{
		{text: "control\x01middle\x7fend", want: []int64{0, 6226, 22000, 19298, 3611, 2}},
		{text: "\ufffd", want: []int64{0, 2}},
		{text: "x\ufffdy", want: []int64{0, 1022, 113, 2}},
		{text: "<mask>", want: []int64{0, 250001, 2}},
		{text: "x<mask>y", want: []int64{0, 1022, 250001, 113, 2}},
		{text: "foo <mask> bar", want: []int64{0, 5775, 31, 250001, 1909, 2}},
		{text: "foo<mask>bar", want: []int64{0, 5775, 31, 250001, 1909, 2}},
		{text: "<s>", want: []int64{0, 0, 2}},
		{text: "</s>", want: []int64{0, 2, 2}},
		{text: "x</s>y", want: []int64{0, 1022, 2, 113, 2}},
		{text: "ab<s>cd", want: []int64{0, 1563, 0, 56329, 2}},
		{text: "<s><mask>", want: []int64{0, 0, 250001, 2}},
		{text: "<mask><s>", want: []int64{0, 250001, 0, 2}},
		{text: "<s><mask></s>", want: []int64{0, 0, 250001, 2, 2}},
		{text: "x<unk>y", want: []int64{0, 1022, 3, 113, 2}},
		{text: "x<pad>y", want: []int64{0, 1022, 1, 113, 2}},
		{text: "x \t\n<mask>y", want: []int64{0, 1022, 250001, 113, 2}},
		{text: "x\u000b<mask>y", want: []int64{0, 1022, 250001, 113, 2}},
		{text: "x\u2028<mask>y", want: []int64{0, 1022, 250001, 113, 2}},
		{text: "e\u0301", want: []int64{0, 393, 2}},
		{text: "A\u030a", want: []int64{0, 8839, 2}},
		{text: "👩\u200d💻", want: []int64{0, 6, 244785, 6, 246382, 2}},
		{text: "ab😀cd", want: []int64{0, 1563, 244218, 71574, 2}},
		{text: "\u00a0x\u2003y", want: []int64{0, 1022, 113, 2}},
		{text: "foo\u00a0bar", want: []int64{0, 5775, 31, 1909, 2}},
		{text: "<mask> foo", want: []int64{0, 250001, 5775, 31, 2}},
		{text: "foo <mask>", want: []int64{0, 5775, 31, 250001, 2}},
		{text: "foo <s> bar", want: []int64{0, 5775, 31, 0, 1909, 2}},
		{text: "x<mask><s>y", want: []int64{0, 1022, 250001, 0, 113, 2}},
		{text: "<mask><pad>", want: []int64{0, 250001, 1, 2}},
		{text: "\u00a0<mask>\u2003", want: []int64{0, 250001, 2}},
		{text: "\u200d<mask>\u00a0", want: []int64{0, 250001, 2}},
		{text: "hello\u00a0<mask>\u2003world", want: []int64{0, 33600, 31, 250001, 8999, 2}},
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
		t.Logf("held-out differential %q -> %v", tc.text, tc.want)
	}
}

func TestLoadTokenizerRejectsUnigramWithoutPrecompiledNormalizer(t *testing.T) {
	path := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if path == "" {
		path = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json`
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read approved tokenizer: %v", err)
	}
	var base tokenizerFile
	if err := json.Unmarshal(original, &base); err != nil {
		t.Fatalf("parse approved tokenizer: %v", err)
	}
	if base.Model.Type != "Unigram" || base.Normalizer == nil || base.Normalizer.Type != "Precompiled" {
		t.Fatalf("approved tokenizer is not the Unigram/Precompiled fixture (model=%q normalizer=%v)", base.Model.Type, base.Normalizer)
	}
	writeMutated := func(t *testing.T, mutate func(*tokenizerFile)) string {
		t.Helper()
		var file tokenizerFile
		if err := json.Unmarshal(original, &file); err != nil {
			t.Fatal(err)
		}
		mutate(&file)
		data, err := json.Marshal(&file)
		if err != nil {
			t.Fatal(err)
		}
		mutated := filepath.Join(t.TempDir(), "tokenizer.json")
		if err := os.WriteFile(mutated, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return mutated
	}
	t.Run("normalizer removed", func(t *testing.T) {
		mutated := writeMutated(t, func(file *tokenizerFile) {
			file.Normalizer = nil
		})
		if _, err := LoadTokenizer(mutated); err == nil || !strings.Contains(err.Error(), "expected Precompiled") {
			t.Fatalf("LoadTokenizer with normalizer removed = %v, want expected-Precompiled error", err)
		}
	})
	t.Run("normalizer swapped", func(t *testing.T) {
		mutated := writeMutated(t, func(file *tokenizerFile) {
			file.Normalizer.Type = "BertNormalizer"
			file.Normalizer.PrecompiledCharsmap = ""
		})
		if _, err := LoadTokenizer(mutated); err == nil || !strings.Contains(err.Error(), "expected Precompiled") {
			t.Fatalf("LoadTokenizer with normalizer swapped = %v, want expected-Precompiled error", err)
		}
	})
	t.Run("added token normalized", func(t *testing.T) {
		mutated := writeMutated(t, func(file *tokenizerFile) {
			for i := range file.AddedTokens {
				file.AddedTokens[i].Normalized = true
				break
			}
		})
		if _, err := LoadTokenizer(mutated); err == nil || !strings.Contains(err.Error(), "normalized=true") {
			t.Fatalf("LoadTokenizer with normalized added token = %v, want normalized=true error", err)
		}
	})
}

// TestLoadTokenizerRejectsMalformedPrecompiledCharsmap proves LoadTokenizer
// rejects structurally invalid double-array tries at load time, before any
// input can reach the tokenizer and panic inside the trie search.
func TestLoadTokenizerRejectsMalformedPrecompiledCharsmap(t *testing.T) {
	path := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if path == "" {
		path = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json`
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read approved tokenizer: %v", err)
	}
	var base tokenizerFile
	if err := json.Unmarshal(original, &base); err != nil {
		t.Fatalf("parse approved tokenizer: %v", err)
	}
	if base.Model.Type != "Unigram" || base.Normalizer == nil {
		t.Fatalf("approved tokenizer is not the Unigram fixture (model=%q normalizer=%v)", base.Model.Type, base.Normalizer)
	}
	writeCharsmap := func(t *testing.T, trie []uint32, payload []byte) string {
		t.Helper()
		raw := make([]byte, 0, 4+len(trie)*4+len(payload))
		header := make([]byte, 4)
		binary.LittleEndian.PutUint32(header, uint32(len(trie)*4))
		raw = append(raw, header...)
		for _, unit := range trie {
			buf := make([]byte, 4)
			binary.LittleEndian.PutUint32(buf, unit)
			raw = append(raw, buf...)
		}
		raw = append(raw, payload...)
		var file tokenizerFile
		if err := json.Unmarshal(original, &file); err != nil {
			t.Fatal(err)
		}
		file.Normalizer.PrecompiledCharsmap = base64.StdEncoding.EncodeToString(raw)
		data, err := json.Marshal(&file)
		if err != nil {
			t.Fatal(err)
		}
		mutated := filepath.Join(t.TempDir(), "tokenizer.json")
		if err := os.WriteFile(mutated, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return mutated
	}
	t.Run("trie index escapes", func(t *testing.T) {
		// A single root unit with no children: any probed byte leaves the array.
		mutated := writeCharsmap(t, []uint32{0}, []byte{0})
		if _, err := LoadTokenizer(mutated); err == nil || !strings.Contains(err.Error(), "escapes the trie array") {
			t.Fatalf("LoadTokenizer with escaping trie = %v, want trie-array error", err)
		}
	})
	t.Run("leaf value out of range", func(t *testing.T) {
		// The 0x101 unit matches byte 0x01 and has a leaf whose value 257
		// points past the 1-byte payload.
		mutated := writeCharsmap(t, []uint32{0, 0x101}, []byte{0})
		if _, err := LoadTokenizer(mutated); err == nil || !strings.Contains(err.Error(), "exceeds normalized payload length") {
			t.Fatalf("LoadTokenizer with out-of-range leaf = %v, want payload-length error", err)
		}
	})
}
