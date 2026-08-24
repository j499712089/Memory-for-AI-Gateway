package embedding

import (
	"unicode"
	"unicode/utf8"
)

// addedMatch is one leftmost-longest non-overlapping match of an added token
// in raw text, with byte offsets.
type addedMatch struct {
	start int
	stop  int
	index int
}

// addedSplit is one piece of text after carving added tokens out of the raw
// string. isToken marks an added token that must be emitted as its own id;
// otherwise the text is a residual that is normalized and encoded with the
// Unigram model.
type addedSplit struct {
	text    string
	id      int
	isToken bool
}

// encodeUnigram tokenizes text through the approved Unigram pipeline: added
// tokens are carved out of the raw string first (matching tokenizers'
// extract_and_normalize), each residual is normalized with the Precompiled
// charsmap and encoded with the Unigram model, and the carved token ids are
// interleaved in their original positions.
func (t *Tokenizer) encodeUnigram(text string) []int {
	var tokens []int
	for _, split := range t.carveAddedTokens(text) {
		if split.isToken {
			tokens = append(tokens, split.id)
			continue
		}
		tokens = append(tokens, t.unigram.encode(
			t.normalizeUnigram(split.text),
			t.metaspaceReplacement,
			t.metaspacePrefixSpace,
		)...)
	}
	return tokens
}

// carveAddedTokens implements the first pass of extract_and_normalize: split
// the raw string on added tokens with normalized == false using leftmost
// longest non-overlapping matches, then expand each match over the adjacent
// whitespace when lstrip or rstrip requests it.
func (t *Tokenizer) carveAddedTokens(text string) []addedSplit {
	if text == "" {
		return nil
	}
	var splits []addedSplit
	startOffset := 0
	for _, match := range matchAddedTokens(text, t.addedTokens) {
		token := t.addedTokens[match.index]
		start, stop := match.start, match.stop
		if token.LStrip {
			// Absorb the whitespace run immediately before the token, but
			// never past the token already matched before it (start_offset).
			newStart := spaceLeftmostAtEnd(text[:start])
			if newStart > startOffset {
				start = newStart
			} else {
				start = startOffset
			}
		}
		if token.RStrip {
			stop += spaceRightmostAtStart(text[stop:])
		}
		if startOffset < start {
			splits = append(splits, addedSplit{text: text[startOffset:start]})
		}
		splits = append(splits, addedSplit{text: text[start:stop], id: token.ID, isToken: true})
		startOffset = stop
	}
	if startOffset < len(text) {
		splits = append(splits, addedSplit{text: text[startOffset:]})
	}
	return splits
}

// matchAddedTokens returns leftmost-longest non-overlapping matches of any
// added token content in text, ordered by start offset. Byte offsets are safe
// for slicing: patterns and text are both valid UTF-8, so a match can never
// begin in the middle of a rune.
func matchAddedTokens(text string, tokens []addedToken) []addedMatch {
	var matches []addedMatch
	for i := 0; i < len(text); {
		best, bestLen := -1, 0
		for j, token := range tokens {
			content := token.Content
			if len(content) <= bestLen || len(content) > len(text)-i {
				continue
			}
			if text[i:i+len(content)] == content {
				best, bestLen = j, len(content)
			}
		}
		if best >= 0 {
			matches = append(matches, addedMatch{start: i, stop: i + bestLen, index: best})
			i += bestLen
		} else {
			_, size := utf8.DecodeRuneInString(text[i:])
			i += size
		}
	}
	return matches
}

// spaceLeftmostAtEnd returns the byte offset where the trailing run of Unicode
// whitespace in s begins; s.len() when s has no trailing whitespace.
func spaceLeftmostAtEnd(s string) int {
	i := len(s)
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		if !unicode.IsSpace(r) {
			break
		}
		i -= size
	}
	return i
}

// spaceRightmostAtStart returns the byte length of the leading run of Unicode
// whitespace in s.
func spaceRightmostAtStart(s string) int {
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !unicode.IsSpace(r) {
			break
		}
		i += size
	}
	return i
}
