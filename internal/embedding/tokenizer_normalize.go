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

// validatePrecompiledCharsmap rejects structurally malformed double-array
// tries before any tokenizer input can reach them. CommonPrefixSearch indexes
// units[nodePos^byte] before it checks the unit's label, and Transform reads
// payload[value:] from a leaf value, so an arbitrary key can panic on a
// malformed trie. The only keys ever fed to the search are the UTF-8 byte
// strings produced by normalizePrecompiled: whole graphemes of 1..5 bytes and
// single runes of 1..4 bytes. A NUL byte terminates the search before any
// array access, so it is never probed. Walk exactly that key space from the
// root and require every index it touches to stay in bounds and every leaf
// value to point inside the normalized payload.
func validatePrecompiledCharsmap(data []byte) error {
	if len(data) < 8 {
		return fmt.Errorf("Precompiled charsmap is truncated")
	}
	trieSize := int(binary.LittleEndian.Uint32(data[:4]))
	if trieSize == 0 || trieSize%4 != 0 || trieSize > len(data)-4 {
		return fmt.Errorf("Precompiled charsmap has invalid trie size %d", trieSize)
	}
	unitCount := trieSize / 4
	payload := data[4+trieSize:]
	if len(payload) == 0 {
		return fmt.Errorf("Precompiled charsmap has no normalized payload")
	}
	units := make([]uint32, unitCount)
	for i := range units {
		units[i] = binary.LittleEndian.Uint32(data[4+i*4:])
	}
	offset := func(unit uint32) int {
		return int(unit>>10) << ((unit & (1 << 9)) >> 6)
	}
	rootPos := offset(units[0])
	if rootPos >= unitCount {
		return fmt.Errorf("Precompiled charsmap root offset %d is out of range", rootPos)
	}

	// utf8State tracks where in a UTF-8 byte sequence the walk is, so only
	// bytes a real key can contain at this position are probed.
	type utf8State struct {
		pending int
		min     byte
		max     byte
	}
	// advance returns the state after byte c, or false when c cannot appear
	// at this position in a valid UTF-8 key.
	advance := func(s utf8State, c byte) (utf8State, bool) {
		if s.pending > 0 {
			if c < s.min || c > s.max {
				return utf8State{}, false
			}
			return utf8State{pending: s.pending - 1, min: 0x80, max: 0xBF}, true
		}
		switch {
		case c == 0:
			return utf8State{}, false // NUL terminates the search before any array access
		case c < 0x80:
			return utf8State{}, true
		case c >= 0xC2 && c <= 0xDF:
			return utf8State{1, 0x80, 0xBF}, true
		case c == 0xE0:
			return utf8State{2, 0xA0, 0xBF}, true
		case c >= 0xE1 && c <= 0xEC:
			return utf8State{2, 0x80, 0xBF}, true
		case c == 0xED:
			return utf8State{2, 0x80, 0x9F}, true
		case c >= 0xEE && c <= 0xEF:
			return utf8State{2, 0x80, 0xBF}, true
		case c == 0xF0:
			return utf8State{3, 0x90, 0xBF}, true
		case c >= 0xF1 && c <= 0xF3:
			return utf8State{3, 0x80, 0xBF}, true
		case c == 0xF4:
			return utf8State{3, 0x80, 0x8F}, true
		}
		return utf8State{}, false
	}
	stateKey := func(s utf8State) int {
		switch {
		case s.pending == 0:
			return 0
		case s.pending == 1:
			return 1
		case s.pending == 2 && s.min == 0xA0:
			return 2
		case s.pending == 2 && s.max == 0x9F:
			return 3
		case s.pending == 2:
			return 4
		case s.pending == 3 && s.min == 0x90:
			return 5
		case s.pending == 3 && s.max == 0x8F:
			return 6
		case s.pending == 3:
			return 7
		}
		return 8
	}
	visited := map[[2]int]bool{}
	var walk func(nodePos, depth int, st utf8State) error
	walk = func(nodePos, depth int, st utf8State) error {
		if depth >= 5 { // keys fed to the search are at most 5 bytes
			return nil
		}
		key := [2]int{nodePos, stateKey(st)}
		if visited[key] {
			return nil
		}
		visited[key] = true
		for c := 0; c < 256; c++ {
			next, ok := advance(st, byte(c))
			if !ok {
				continue
			}
			idx := nodePos ^ c
			if idx >= unitCount {
				return fmt.Errorf("Precompiled charsmap byte 0x%02x from node %d escapes the trie array", c, nodePos)
			}
			unit := units[idx]
			// CommonPrefixSearch stops (without probing further) when the
			// unit's label does not match the byte, so only matching units
			// continue the walk.
			if unit&(1<<31) != 0 || byte(unit&0xFF) != byte(c) {
				continue
			}
			child := idx ^ offset(unit)
			if unit&(1<<8) != 0 { // HasLeaf: the child unit holds the value
				if child >= unitCount {
					return fmt.Errorf("Precompiled charsmap transition for byte 0x%02x from node %d is out of range", c, nodePos)
				}
				value := int(units[child] & ((1 << 31) - 1))
				if value > len(payload) {
					return fmt.Errorf("Precompiled charsmap leaf value %d exceeds normalized payload length %d", value, len(payload))
				}
			}
			if err := walk(child, depth+1, next); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(rootPos, 0, utf8State{})
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
			if replacement, matched := transformPrecompiled(normalizer, grapheme); matched {
				normalized.WriteString(replacement) // empty replacement deletes
				continue
			}
		}
		for _, r := range grapheme {
			part := string(r)
			if replacement, matched := transformPrecompiled(normalizer, part); matched {
				normalized.WriteString(replacement)
			} else {
				normalized.WriteString(part)
			}
		}
	}
	return normalized.String()
}

// transformPrecompiled returns the rewrite for chunk when the charsmap matched
// it, distinguishing "no match" (false) from "matched to the empty string"
// (true with an empty replacement, which deletes the chunk).
func transformPrecompiled(normalizer *spm.Precompiled, chunk string) (string, bool) {
	results := normalizer.Trie.CommonPrefixSearch([]byte(chunk))
	if len(results) == 0 {
		return "", false
	}
	payload := []byte(normalizer.Normalized)
	index := results[0]
	index2 := index
	for index2 < len(payload) && payload[index2] != 0 {
		index2++
	}
	return string(payload[index:index2]), true
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
