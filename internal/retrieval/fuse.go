package retrieval

import (
	"sort"
	"strings"
	"time"
)

type Candidate struct {
	ID             string
	TeamID         string
	IdentityCardID string
	AssetType      string
	Layer          string
	Name           string
	Summary        string
	Snippet        string
	Visibility     string
	SourceEventIDs []string
	Score          float64
	Version        int
	UpdatedAt      time.Time
}

type Limits struct {
	MaxResults int
	MaxChars   int
	MaxTokens  int
	Deadline   time.Time
}

type Result struct {
	Items      []Candidate `json:"results"`
	Truncated  bool        `json:"truncated"`
	UsedTokens int         `json:"used_tokens"`
}

func defaultLimits(limits Limits) Limits {
	if limits.MaxResults <= 0 {
		limits.MaxResults = 20
	}
	if limits.MaxResults > 20 {
		limits.MaxResults = 20
	}
	if limits.MaxChars <= 0 {
		limits.MaxChars = 8 * 1024
	}
	if limits.MaxTokens <= 0 {
		limits.MaxTokens = 3000
	}
	return limits
}

func layerWeight(layer string) float64 {
	switch strings.ToLower(layer) {
	case "l4":
		return 4
	case "l3":
		return 3
	case "l2":
		return 2
	case "l1":
		return 1
	case "l0":
		return 0
	default:
		return 0
	}
}

func estimateTokens(value string) int {
	return (len([]rune(value)) + 3) / 4
}

// Fuse performs deterministic score fusion and applies hard count/character/
// token limits. Duplicate asset IDs keep the strongest candidate.
func Fuse(candidates []Candidate, limits Limits) Result {
	limits = defaultLimits(limits)
	byID := make(map[string]Candidate, len(candidates))
	for _, candidate := range candidates {
		if candidate.ID == "" {
			continue
		}
		candidate.Score += layerWeight(candidate.Layer) * 0.01
		if old, exists := byID[candidate.ID]; !exists || candidate.Score > old.Score {
			byID[candidate.ID] = candidate
		}
	}
	ordered := make([]Candidate, 0, len(byID))
	for _, candidate := range byID {
		ordered = append(ordered, candidate)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Score != ordered[j].Score {
			return ordered[i].Score > ordered[j].Score
		}
		if !ordered[i].UpdatedAt.Equal(ordered[j].UpdatedAt) {
			return ordered[i].UpdatedAt.After(ordered[j].UpdatedAt)
		}
		return ordered[i].ID < ordered[j].ID
	})
	result := Result{}
	chars := 0
	for _, candidate := range ordered {
		if !limits.Deadline.IsZero() && time.Now().After(limits.Deadline) {
			result.Truncated = true
			break
		}
		if len(result.Items) >= limits.MaxResults {
			result.Truncated = true
			break
		}
		text := candidate.Snippet
		if text == "" {
			text = candidate.Summary
		}
		candidateTokens := estimateTokens(text)
		if chars+len([]rune(text)) > limits.MaxChars || result.UsedTokens+candidateTokens > limits.MaxTokens {
			result.Truncated = true
			continue
		}
		result.Items = append(result.Items, candidate)
		chars += len([]rune(text))
		result.UsedTokens += candidateTokens
	}
	return result
}
