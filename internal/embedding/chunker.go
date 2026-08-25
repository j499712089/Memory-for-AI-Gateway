package embedding

import (
	"errors"
	"math"
	"strings"
)

var (
	ErrEmptyText  = errors.New("text is required")
	ErrZeroVector = errors.New("document produced a zero vector")
)

const (
	defaultChunkRunes   = 480
	defaultChunkOverlap = 64
)

// SplitText bounds long asset payloads before encoding. The overlap preserves
// context at boundaries while keeping every ONNX invocation within 128 tokens.
func SplitText(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	runes := []rune(text)
	chunks := make([]string, 0, (len(runes)+defaultChunkRunes-1)/defaultChunkRunes)
	for start := 0; start < len(runes); {
		end := start + defaultChunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, strings.TrimSpace(string(runes[start:end])))
		if end == len(runes) {
			break
		}
		start = end - defaultChunkOverlap
	}
	return chunks
}

// EncodeDocument returns one normalized vector for a possibly multi-chunk
// document. Chunk vectors are averaged and normalized, making persistence
// compatible with the existing single assets.embedding BLOB column.
func (s *Service) EncodeDocument(text string) ([]float32, error) {
	chunks := SplitText(text)
	if len(chunks) == 0 {
		return nil, ErrEmptyText
	}
	result := make([]float32, Dimensions)
	for _, chunk := range chunks {
		vector, err := s.Encode(chunk)
		if err != nil {
			return nil, err
		}
		for i, value := range vector {
			result[i] += value
		}
	}
	var norm float64
	for i := range result {
		result[i] /= float32(len(chunks))
		norm += float64(result[i] * result[i])
	}
	if norm == 0 {
		return nil, ErrZeroVector
	}
	norm = math.Sqrt(norm)
	for i := range result {
		result[i] = float32(float64(result[i]) / norm)
	}
	return result, nil
}
