package embedding

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const defaultSequenceLength = 128

type tokenizerModel struct {
	Type                    string         `json:"type"`
	UnkToken                string         `json:"unk_token"`
	ContinuingSubwordPrefix string         `json:"continuing_subword_prefix"`
	MaxInputCharsPerWord    int            `json:"max_input_chars_per_word"`
	Vocab                   map[string]int `json:"vocab"`
}

type tokenizerFile struct {
	Truncation *struct {
		MaxLength int `json:"max_length"`
	} `json:"truncation"`
	Normalizer *struct {
		Type               string `json:"type"`
		CleanText          bool   `json:"clean_text"`
		HandleChineseChars bool   `json:"handle_chinese_chars"`
		StripAccents       *bool  `json:"strip_accents"`
		Lowercase          bool   `json:"lowercase"`
	} `json:"normalizer"`
	AddedTokens []struct {
		ID      int    `json:"id"`
		Content string `json:"content"`
	} `json:"added_tokens"`
	Model tokenizerModel `json:"model"`
}

// Tokenizer is the WordPiece/BertNormalizer tokenizer exported with the ONNX
// model. It deliberately keeps no mutable per-request state.
type Tokenizer struct {
	vocab                map[string]int
	unknownID            int
	classID              int
	separatorID          int
	padID                int
	maxInputCharsPerWord int
	continuingPrefix     string
	sequenceLength       int
	cleanText            bool
	handleChineseChars   bool
	stripAccents         bool
	lowercase            bool
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
	if file.Model.Type != "WordPiece" || len(file.Model.Vocab) == 0 {
		return nil, fmt.Errorf("tokenizer model must be WordPiece with a vocabulary")
	}
	unknownID, ok := file.Model.Vocab[file.Model.UnkToken]
	if !ok {
		return nil, fmt.Errorf("tokenizer vocabulary is missing %q", file.Model.UnkToken)
	}
	classID, classOK := file.Model.Vocab["[CLS]"]
	separatorID, separatorOK := file.Model.Vocab["[SEP]"]
	padID, padOK := file.Model.Vocab["[PAD]"]
	for _, token := range file.AddedTokens {
		switch token.Content {
		case "[CLS]":
			classID, classOK = token.ID, true
		case "[SEP]":
			separatorID, separatorOK = token.ID, true
		case "[PAD]":
			padID, padOK = token.ID, true
		}
	}
	if !classOK || !separatorOK || !padOK {
		return nil, fmt.Errorf("tokenizer vocabulary must define [CLS], [SEP], and [PAD]")
	}
	maxChars := file.Model.MaxInputCharsPerWord
	if maxChars <= 0 {
		maxChars = 100
	}
	sequenceLength := defaultSequenceLength
	if file.Truncation != nil && file.Truncation.MaxLength > 0 {
		sequenceLength = file.Truncation.MaxLength
	}
	stripAccents := false
	lowercase := false
	cleanText := true
	handleChineseChars := true
	if file.Normalizer != nil {
		lowercase = file.Normalizer.Lowercase
		cleanText = file.Normalizer.CleanText
		handleChineseChars = file.Normalizer.HandleChineseChars
		if file.Normalizer.StripAccents != nil {
			stripAccents = *file.Normalizer.StripAccents
		} else {
			stripAccents = lowercase
		}
	}
	prefix := file.Model.ContinuingSubwordPrefix
	if prefix == "" {
		prefix = "##"
	}
	return &Tokenizer{
		vocab:                file.Model.Vocab,
		unknownID:            unknownID,
		classID:              classID,
		separatorID:          separatorID,
		padID:                padID,
		maxInputCharsPerWord: maxChars,
		continuingPrefix:     prefix,
		sequenceLength:       sequenceLength,
		cleanText:            cleanText,
		handleChineseChars:   handleChineseChars,
		stripAccents:         stripAccents,
		lowercase:            lowercase,
	}, nil
}

// Encode returns fixed-length input_ids, attention_mask and token_type_ids.
func (t *Tokenizer) Encode(text string) (ids, mask, types []int64, err error) {
	if t == nil {
		return nil, nil, nil, fmt.Errorf("tokenizer is nil")
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil, nil, fmt.Errorf("text is required")
	}
	words := t.preTokenize(t.normalize(text))
	ids = make([]int64, t.sequenceLength)
	mask = make([]int64, t.sequenceLength)
	types = make([]int64, t.sequenceLength)
	for i := range ids {
		ids[i] = int64(t.padID)
	}
	position := 0
	ids[position], mask[position] = int64(t.classID), 1
	position++
	for _, word := range words {
		for _, id := range t.wordPiece(word) {
			if position >= t.sequenceLength-1 {
				break
			}
			ids[position], mask[position] = int64(id), 1
			position++
		}
		if position >= t.sequenceLength-1 {
			break
		}
	}
	if position >= t.sequenceLength {
		position = t.sequenceLength - 1
	}
	ids[position], mask[position] = int64(t.separatorID), 1
	return ids, mask, types, nil
}

func (t *Tokenizer) normalize(text string) string {
	var b strings.Builder
	for _, r := range text {
		if t.cleanText && (r == 0 || r == '\ufffd' || unicode.IsControl(r)) {
			continue
		}
		if t.cleanText && unicode.IsSpace(r) {
			r = ' '
		}
		if t.handleChineseChars && isChinese(r) {
			b.WriteByte(' ')
			b.WriteRune(r)
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	value := b.String()
	if t.lowercase {
		value = strings.ToLower(value)
	}
	if t.stripAccents {
		value = stripDiacritics(value)
	}
	return value
}

func (t *Tokenizer) preTokenize(text string) []string {
	var words []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			words = append(words, string(current))
			current = current[:0]
		}
	}
	for _, r := range text {
		if unicode.IsSpace(r) {
			flush()
		} else if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			flush()
			words = append(words, string(r))
		} else {
			current = append(current, r)
		}
	}
	flush()
	return words
}

func (t *Tokenizer) wordPiece(word string) []int {
	runes := []rune(word)
	if len(runes) == 0 || len(runes) > t.maxInputCharsPerWord {
		return []int{t.unknownID}
	}
	pieces := make([]int, 0, len(runes))
	for start := 0; start < len(runes); {
		end := len(runes)
		found := false
		for start < end {
			piece := string(runes[start:end])
			if start > 0 {
				piece = t.continuingPrefix + piece
			}
			if id, ok := t.vocab[piece]; ok {
				pieces = append(pieces, id)
				start = end
				found = true
				break
			}
			end--
		}
		if !found {
			return []int{t.unknownID}
		}
	}
	return pieces
}

func isChinese(r rune) bool {
	return (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3400 && r <= 0x4dbf) ||
		(r >= 0x20000 && r <= 0x2a6df) || (r >= 0x2a700 && r <= 0x2b73f) ||
		(r >= 0x2b740 && r <= 0x2b81f) || (r >= 0x2b820 && r <= 0x2ceaf) ||
		(r >= 0xf900 && r <= 0xfaff)
}

func stripDiacritics(value string) string {
	// BertNormalizer removes combining marks after NFD decomposition.
	var b strings.Builder
	for _, r := range norm.NFD.String(value) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func validUTF8(value string) bool { return utf8.ValidString(value) }
