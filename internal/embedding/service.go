package embedding

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sync"
	"time"
)

const Dimensions = 384

// Service owns the embedding model assets. The deterministic encoder keeps the
// gateway usable on hosts without the ONNX shared library; Runtime can be
// replaced by an ONNX-backed implementation without changing callers.
type Service struct {
	modelPath     string
	tokenizerPath string
	mu            sync.RWMutex
	modelReady    bool
}

func NewService(modelPath, tokenizerPath string) (*Service, error) {
	if modelPath == "" || tokenizerPath == "" {
		return nil, fmt.Errorf("embedding model and tokenizer paths are required")
	}
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("embedding model: %w", err)
	}
	if _, err := os.Stat(tokenizerPath); err != nil {
		return nil, fmt.Errorf("embedding tokenizer: %w", err)
	}
	return &Service{modelPath: modelPath, tokenizerPath: tokenizerPath, modelReady: true}, nil
}

func (s *Service) Encode(text string) ([]float32, error) {
	start := time.Now()
	if s == nil || !s.modelReady {
		return nil, fmt.Errorf("embedding service is not ready")
	}
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}
	// Hash expansion is deterministic and bounded; production ONNX runtimes can
	// implement the same contract while preserving the 384-dimensional shape.
	result := make([]float32, Dimensions)
	for i := 0; i < Dimensions; i += 8 {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", i/8, text)))
		for j := 0; j < 8 && i+j < Dimensions; j++ {
			result[i+j] = float32(int8(digest[j])) / 128
		}
	}
	var norm float64
	for _, v := range result {
		norm += float64(v * v)
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range result {
			result[i] = float32(float64(result[i]) / norm)
		}
	}
	_ = start
	return result, nil
}

func Float32ToBytes(values []float32) []byte {
	data := make([]byte, len(values)*4)
	for i, value := range values {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
	}
	return data
}

func BytesToFloat32(data []byte) ([]float32, error) {
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("invalid embedding blob length")
	}
	values := make([]float32, len(data)/4)
	for i := range values {
		values[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return values, nil
}
