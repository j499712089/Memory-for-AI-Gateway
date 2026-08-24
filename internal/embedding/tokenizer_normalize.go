package embedding

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
	"github.com/sugarme/tokenizer/spm"
	"golang.org/x/text/unicode/norm"
)

func validatePrecompiledCharsmap(data []byte) error {
	if len(data) < 8 {
		return fmt.Errorf("Precompiled charsmap is truncated")
	}
	trieSize := int(binary.LittleEndian.Uint32(data[:4]))
	if trieSize == 0 || trieSize%4 != 0 || trieSize > len(data)-4 {
		return fmt.Errorf("Precompiled charsmap has invalid trie size %d", trieSize)
	}
	if len(data[4+trieSize:]) == 0 {
		return fmt.Errorf("Precompiled charsmap has no normalized payload")
	}
	return nil
}

func normalizerConfig(n *tokenizerNormalizer) (string, bool, bool, bool, bool, string, error) {
	if n == nil {
		return "", true, true, false, false, "", nil
	}
	if n.Type == "Precompiled" && n.PrecompiledCharsmap == "" {
		return "", false, false, false, false, "", fmt.Errorf("Precompiled normalizer is missing precompiled_charsmap")
	}
	if n.Type != "" && n.Type != "Precompiled" && n.Type != "BertNormalizer" {
		return "", false, false, false, false, "", fmt.Errorf("unsupported tokenizer normalizer %q", n.Type)
	}
	strip := false
	if n.StripAccents != nil {
		strip = *n.StripAccents
	}
	return n.Type, n.CleanText, n.HandleChineseChars, strip, n.Lowercase, n.PrecompiledCharsmap, nil
}

func isBlank(value string) bool { return strings.TrimSpace(value) == "" }

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

func (t *Tokenizer) normalizeUnigram(text string) string {
	if t.precompiled == nil {
		return text
	}
	// The approved multilingual artifact embeds SentencePiece's exact
	// normalization rewrite table. Do not replace it with generic NFKC: the
	// charsmap contains model-specific control, compatibility, and script rules.
	return normalizePrecompiled(t.precompiled, text)
}

func normalizePrecompiled(normalizer *spm.Precompiled, text string) string {
	var normalized strings.Builder
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		grapheme := graphemes.Str()
		// SentencePiece first attempts a whole-grapheme rewrite for short
		// clusters. For longer clusters it applies the same table per rune.
		if len(grapheme) < 6 {
			if replacement := normalizer.Transform(grapheme); replacement != "" {
				normalized.WriteString(replacement)
				continue
			}
		}
		for _, r := range grapheme {
			part := string(r)
			if replacement := normalizer.Transform(part); replacement != "" {
				normalized.WriteString(replacement)
			} else {
				normalized.WriteString(part)
			}
		}
	}
	return normalized.String()
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

func isChinese(r rune) bool {
	return (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3400 && r <= 0x4dbf) || (r >= 0x20000 && r <= 0x2a6df) || (r >= 0x2a700 && r <= 0x2b73f) || (r >= 0x2b740 && r <= 0x2b81f) || (r >= 0x2b820 && r <= 0x2ceaf) || (r >= 0xf900 && r <= 0xfaff)
}

func stripDiacritics(value string) string {
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
