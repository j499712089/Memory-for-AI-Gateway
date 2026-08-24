package embedding

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/sugarme/tokenizer/spm"
)

const defaultSequenceLength = 128

type tokenizerModel struct {
	Type                    string          `json:"type"`
	UnkToken                string          `json:"unk_token"`
	UnkID                   int             `json:"unk_id"`
	ContinuingSubwordPrefix string          `json:"continuing_subword_prefix"`
	MaxInputCharsPerWord    int             `json:"max_input_chars_per_word"`
	Vocab                   json.RawMessage `json:"vocab"`
}

type tokenizerNormalizer struct {
	Type                string `json:"type"`
	PrecompiledCharsmap string `json:"precompiled_charsmap"`
	CleanText           bool   `json:"clean_text"`
	HandleChineseChars  bool   `json:"handle_chinese_chars"`
	StripAccents        *bool  `json:"strip_accents"`
	Lowercase           bool   `json:"lowercase"`
}

type tokenizerFile struct {
	Truncation *struct {
		MaxLength int `json:"max_length"`
	} `json:"truncation"`
	Padding *struct {
		PadID int `json:"pad_id"`
	} `json:"padding"`
	Normalizer   *tokenizerNormalizer `json:"normalizer"`
	PreTokenizer *struct {
		Type          string `json:"type"`
		Replacement   string `json:"replacement"`
		AddPrefix     bool   `json:"add_prefix_space"`
		Pretokenizers []struct {
			Type        string `json:"type"`
			Replacement string `json:"replacement"`
			AddPrefix   bool   `json:"add_prefix_space"`
		} `json:"pretokenizers"`
	} `json:"pre_tokenizer"`
	AddedTokens []struct {
		ID      int    `json:"id"`
		Content string `json:"content"`
	} `json:"added_tokens"`
	Model tokenizerModel `json:"model"`
}

type tokenizerKind uint8

const (
	wordPieceKind tokenizerKind = iota
	unigramKind
)

// Tokenizer owns one immutable model-specific encoder. Encode is shared by
// document writes and retrieval queries to keep their vector inputs identical.
type Tokenizer struct {
	kind                 tokenizerKind
	wordPiece            *wordPieceTokenizer
	unigram              *unigramTokenizer
	unknownID            int
	classID              int
	separatorID          int
	padID                int
	sequenceLength       int
	cleanText            bool
	handleChineseChars   bool
	stripAccents         bool
	lowercase            bool
	normalizerType       string
	metaspaceReplacement string
	metaspacePrefixSpace bool
	precompiled          *spm.Precompiled
}

func LoadTokenizer(path string) (*Tokenizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tokenizer: %w", err)
	}
	var file tokenizerFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse tokenizer: %w", err)
	}
	if len(file.Model.Vocab) == 0 {
		return nil, fmt.Errorf("tokenizer model has no vocabulary")
	}
	sequenceLength := defaultSequenceLength
	if file.Truncation != nil && file.Truncation.MaxLength > 0 {
		if file.Truncation.MaxLength != defaultSequenceLength {
			return nil, fmt.Errorf("tokenizer truncation max_length is %d, want %d", file.Truncation.MaxLength, defaultSequenceLength)
		}
		sequenceLength = file.Truncation.MaxLength
	}
	normalizerType, cleanText, handleChinese, stripAccents, lowercase, charsmap, err := normalizerConfig(file.Normalizer)
	if err != nil {
		return nil, err
	}
	if file.Model.Type == "Unigram" && normalizerType != "" && normalizerType != "Precompiled" {
		return nil, fmt.Errorf("Unigram tokenizer normalizer %q is unsupported; expected Precompiled", normalizerType)
	}
	result := &Tokenizer{
		sequenceLength: sequenceLength, cleanText: cleanText,
		handleChineseChars: handleChinese, stripAccents: stripAccents,
		lowercase: lowercase, normalizerType: normalizerType,
		metaspaceReplacement: "▁", metaspacePrefixSpace: true,
	}
	if charsmap != "" {
		data, decodeErr := base64.StdEncoding.DecodeString(charsmap)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode Precompiled charsmap: %w", decodeErr)
		}
		if err := validatePrecompiledCharsmap(data); err != nil {
			return nil, err
		}
		precompiled, createErr := spm.NewPrecompiledFrom(data)
		if createErr != nil {
			return nil, fmt.Errorf("load Precompiled charsmap: %w", createErr)
		}
		result.precompiled = precompiled
	}
	if file.Padding != nil {
		result.padID = file.Padding.PadID
	}
	if file.PreTokenizer != nil && file.PreTokenizer.Type == "Metaspace" {
		result.metaspaceReplacement = file.PreTokenizer.Replacement
		result.metaspacePrefixSpace = file.PreTokenizer.AddPrefix
	} else if file.PreTokenizer != nil {
		for _, pretokenizer := range file.PreTokenizer.Pretokenizers {
			if pretokenizer.Type == "Metaspace" {
				result.metaspaceReplacement = pretokenizer.Replacement
				result.metaspacePrefixSpace = pretokenizer.AddPrefix
				break
			}
		}
	}
	switch file.Model.Type {
	case "WordPiece":
		return result.loadWordPiece(file)
	case "Unigram":
		return result.loadUnigram(file)
	default:
		return nil, fmt.Errorf("unsupported tokenizer model %q", file.Model.Type)
	}
}

func (t *Tokenizer) loadWordPiece(file tokenizerFile) (*Tokenizer, error) {
	var vocab map[string]int
	if err := json.Unmarshal(file.Model.Vocab, &vocab); err != nil || len(vocab) == 0 {
		return nil, fmt.Errorf("WordPiece tokenizer vocabulary must be an object")
	}
	unknownID, ok := vocab[file.Model.UnkToken]
	if !ok {
		return nil, fmt.Errorf("tokenizer vocabulary is missing %q", file.Model.UnkToken)
	}
	t.classID, t.separatorID, t.padID = specialIDs(file.AddedTokens, vocab)
	if t.classID < 0 || t.separatorID < 0 {
		return nil, fmt.Errorf("tokenizer vocabulary must define [CLS] and [SEP]")
	}
	maxChars := file.Model.MaxInputCharsPerWord
	if maxChars <= 0 {
		maxChars = 100
	}
	prefix := file.Model.ContinuingSubwordPrefix
	if prefix == "" {
		prefix = "##"
	}
	t.kind, t.unknownID = wordPieceKind, unknownID
	t.wordPiece = &wordPieceTokenizer{vocab: vocab, unknownID: unknownID, maxInputChars: maxChars, continuingPrefix: prefix}
	return t, nil
}

func (t *Tokenizer) loadUnigram(file tokenizerFile) (*Tokenizer, error) {
	if file.Padding == nil {
		return nil, fmt.Errorf("Unigram tokenizer is missing padding.pad_id")
	}
	pieces, err := parseUnigramVocab(file.Model.Vocab)
	if err != nil {
		return nil, err
	}
	vocabIDs := make(map[string]int, len(pieces))
	for id, piece := range pieces {
		vocabIDs[piece.token] = id
	}
	unknownID := file.Model.UnkID
	if unknownID < 0 || unknownID >= len(pieces) {
		unknownID = vocabIDs["<unk>"]
	}
	classID, separatorID, padID := specialIDs(file.AddedTokens, vocabIDs)
	t.classID, t.separatorID = classID, separatorID
	if t.classID < 0 || t.separatorID < 0 || padID < 0 {
		return nil, fmt.Errorf("Unigram tokenizer must define <s>, </s>, and <pad>")
	}
	if file.Padding.PadID != padID {
		return nil, fmt.Errorf("Unigram padding pad_id=%d conflicts with <pad> vocabulary id=%d", file.Padding.PadID, padID)
	}
	t.padID = file.Padding.PadID
	t.kind, t.unknownID = unigramKind, unknownID
	t.unigram = newUnigramTokenizer(pieces, unknownID)
	return t, nil
}

func specialIDs(added []struct {
	ID      int    `json:"id"`
	Content string `json:"content"`
}, vocab map[string]int) (int, int, int) {
	classID, separatorID, padID := -1, -1, -1
	for _, name := range []string{"[CLS]", "<s>"} {
		if id, ok := vocab[name]; ok {
			classID = id
			break
		}
	}
	for _, name := range []string{"[SEP]", "</s>"} {
		if id, ok := vocab[name]; ok {
			separatorID = id
			break
		}
	}
	for _, name := range []string{"[PAD]", "<pad>"} {
		if id, ok := vocab[name]; ok {
			padID = id
			break
		}
	}
	for _, token := range added {
		switch token.Content {
		case "[CLS]", "<s>":
			classID = token.ID
		case "[SEP]", "</s>":
			separatorID = token.ID
		case "[PAD]", "<pad>":
			padID = token.ID
		}
	}
	return classID, separatorID, padID
}

// Encode returns fixed-length input_ids, attention_mask and token_type_ids.
func (t *Tokenizer) Encode(text string) (ids, mask, types []int64, err error) {
	if t == nil {
		return nil, nil, nil, fmt.Errorf("tokenizer is nil")
	}
	if !validUTF8(text) || isBlank(text) {
		return nil, nil, nil, fmt.Errorf("text is required")
	}
	var tokens []int
	if t.kind == unigramKind {
		tokens = t.unigram.encode(t.normalizeUnigram(text), t.metaspaceReplacement, t.metaspacePrefixSpace)
	} else {
		for _, word := range t.preTokenize(t.normalize(text)) {
			tokens = append(tokens, t.wordPiece.encode(word)...)
		}
	}
	ids = make([]int64, t.sequenceLength)
	mask = make([]int64, t.sequenceLength)
	types = make([]int64, t.sequenceLength)
	for i := range ids {
		ids[i] = int64(t.padID)
	}
	position := 0
	ids[position], mask[position] = int64(t.classID), 1
	position++
	for _, id := range tokens {
		if position >= t.sequenceLength-1 {
			break
		}
		ids[position], mask[position] = int64(id), 1
		position++
	}
	if position >= t.sequenceLength {
		position = t.sequenceLength - 1
	}
	ids[position], mask[position] = int64(t.separatorID), 1
	return ids, mask, types, nil
}

// PadID reports the artifact-declared right-padding token id.
func (t *Tokenizer) PadID() int {
	if t == nil {
		return -1
	}
	return t.padID
}
