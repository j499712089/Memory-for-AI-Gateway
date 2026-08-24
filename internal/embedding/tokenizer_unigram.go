package embedding

import (
	"encoding/json"
	"fmt"
	"unicode"
)

type unigramPiece struct {
	token string
	score float64
}

type unigramNode struct {
	children map[rune]*unigramNode
	pieceID  int
	score    float64
	terminal bool
}

type unigramTokenizer struct {
	root         *unigramNode
	unknownID    int
	unknownScore float64
}

func parseUnigramVocab(raw json.RawMessage) ([]unigramPiece, error) {
	var entries [][]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || len(entries) == 0 {
		return nil, fmt.Errorf("Unigram tokenizer vocabulary must be an array")
	}
	pieces := make([]unigramPiece, len(entries))
	for id, entry := range entries {
		if len(entry) != 2 {
			return nil, fmt.Errorf("Unigram vocabulary entry %d is malformed", id)
		}
		if err := json.Unmarshal(entry[0], &pieces[id].token); err != nil || pieces[id].token == "" {
			return nil, fmt.Errorf("Unigram vocabulary entry %d has no token", id)
		}
		if err := json.Unmarshal(entry[1], &pieces[id].score); err != nil {
			return nil, fmt.Errorf("Unigram vocabulary entry %d has invalid score", id)
		}
	}
	return pieces, nil
}

func newUnigramTokenizer(pieces []unigramPiece, unknownID int) *unigramTokenizer {
	minScore := 0.0
	if len(pieces) > 0 {
		minScore = pieces[0].score
		for _, piece := range pieces[1:] {
			if piece.score < minScore {
				minScore = piece.score
			}
		}
	}
	t := &unigramTokenizer{root: &unigramNode{}, unknownID: unknownID, unknownScore: minScore - 10}
	for id, piece := range pieces {
		node := t.root
		for _, r := range piece.token {
			if node.children == nil {
				node.children = make(map[rune]*unigramNode)
			}
			if node.children[r] == nil {
				node.children[r] = &unigramNode{}
			}
			node = node.children[r]
		}
		if !node.terminal || piece.score > node.score {
			node.pieceID, node.score, node.terminal = id, piece.score, true
		}
	}
	return t
}

// encode uses the Unigram model's maximum-score segmentation. The trie keeps
// lookup proportional to the input length instead of scanning 250k vocabulary
// entries at every character.
func (t *unigramTokenizer) encode(text, replacement string, addPrefixSpace bool) []int {
	if replacement == "" {
		replacement = "▁"
	}
	prepared := metaspace(text, replacement, addPrefixSpace)
	runes := []rune(prepared)
	if len(runes) == 0 {
		return nil
	}
	best := make([]float64, len(runes)+1)
	prev := make([]int, len(runes)+1)
	chosen := make([]int, len(runes)+1)
	for i := range best {
		best[i] = -1e30
		prev[i] = -1
	}
	best[0] = 0
	for i := 0; i < len(runes); i++ {
		if prev[i] < 0 && i != 0 {
			continue
		}
		node := t.root
		for j := i; j < len(runes); j++ {
			if node.children == nil {
				break
			}
			node = node.children[runes[j]]
			if node == nil {
				break
			}
			if node.terminal && best[i]+node.score > best[j+1] {
				best[j+1], prev[j+1], chosen[j+1] = best[i]+node.score, i, node.pieceID
			}
		}
		if best[i]+t.unknownScore > best[i+1] { // SentencePiece's unknown fallback consumes one rune.
			best[i+1], prev[i+1], chosen[i+1] = best[i]+t.unknownScore, i, t.unknownID
		}
	}
	result := make([]int, 0, len(runes))
	for end := len(runes); end > 0; end = prev[end] {
		result = append(result, chosen[end])
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	// SentencePiece fuses adjacent unknown spans into one <unk> token. This
	// matters for emoji/control runs and keeps IDs aligned with tokenizers.
	fused := result[:0]
	for _, id := range result {
		if len(fused) == 0 || id != t.unknownID || fused[len(fused)-1] != t.unknownID {
			fused = append(fused, id)
		}
	}
	return fused
}

func metaspace(text, replacement string, addPrefixSpace bool) string {
	result := make([]rune, 0, len([]rune(text))+1)
	needsPrefix := addPrefixSpace
	for _, r := range text {
		if unicode.IsSpace(r) {
			needsPrefix = true
			continue
		}
		if needsPrefix {
			result = append(result, []rune(replacement)...)
			needsPrefix = false
		}
		result = append(result, r)
	}
	return string(result)
}
