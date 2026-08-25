package embedding

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	Dimensions   = 384
	ModelVersion = "paraphrase-multilingual-MiniLM-L12-v2"
)

// Service owns one ONNX session and its matching tokenizer. The
// session is shared by workers and retrieval queries so both paths use the
// exact same encoder and vector space.
type Service struct {
	modelPath     string
	tokenizerPath string
	tokenizer     *Tokenizer
	session       *ort.DynamicAdvancedSession
	inputNames    []string
	outputName    string
	mu            sync.Mutex
}

func NewService(modelPath, tokenizerPath string) (*Service, error) {
	if modelPath == "" || tokenizerPath == "" {
		return nil, fmt.Errorf("embedding model and tokenizer paths are required")
	}
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("embedding model: %w", err)
	}
	tokenizer, err := LoadTokenizer(tokenizerPath)
	if err != nil {
		return nil, err
	}
	if err := initializeRuntime(modelPath); err != nil {
		return nil, fmt.Errorf("initialize onnxruntime: %w", err)
	}
	inputNames, outputName, err := modelIO(modelPath)
	if err != nil {
		return nil, err
	}
	session, err := ort.NewDynamicAdvancedSession(modelPath, inputNames, []string{outputName}, nil)
	if err != nil {
		return nil, fmt.Errorf("create ONNX session: %w", err)
	}
	return &Service{
		modelPath: modelPath, tokenizerPath: tokenizerPath, tokenizer: tokenizer,
		session: session, inputNames: inputNames, outputName: outputName,
	}, nil
}

func modelIO(path string) ([]string, string, error) {
	inputs, outputs, err := ort.GetInputOutputInfo(path)
	if err != nil {
		return nil, "", fmt.Errorf("inspect ONNX model: %w", err)
	}
	if len(outputs) == 0 {
		return nil, "", fmt.Errorf("ONNX model has no outputs")
	}
	if len(outputs[0].Dimensions) > 0 {
		last := outputs[0].Dimensions[len(outputs[0].Dimensions)-1]
		if last > 0 && last != Dimensions {
			return nil, "", fmt.Errorf("ONNX output dimension is %d, want %d", last, Dimensions)
		}
	}
	inputNames := make([]string, 0, len(inputs))
	for _, input := range inputs {
		if input.Name == "input_ids" || input.Name == "attention_mask" || input.Name == "token_type_ids" {
			inputNames = append(inputNames, input.Name)
		}
	}
	if len(inputNames) == 0 {
		return nil, "", fmt.Errorf("ONNX model has no supported text inputs")
	}
	return inputNames, outputs[0].Name, nil
}

// Encode runs tokenizer -> ONNX -> attention-mask weighted mean pooling -> L2
// normalization, matching sentence-transformers' mean-pooling contract.
func (s *Service) Encode(text string) ([]float32, error) {
	if s == nil || s.session == nil || s.tokenizer == nil {
		return nil, fmt.Errorf("embedding service is not ready")
	}
	ids, mask, types, err := s.tokenizer.Encode(text)
	if err != nil {
		return nil, err
	}
	inputValues := make([]ort.Value, 0, len(s.inputNames))
	owned := make([]ort.Value, 0, len(s.inputNames))
	for _, name := range s.inputNames {
		var values []int64
		switch name {
		case "input_ids":
			values = ids
		case "attention_mask":
			values = mask
		case "token_type_ids":
			values = types
		}
		tensor, tensorErr := ort.NewTensor[int64](ort.Shape{1, int64(len(values))}, values)
		if tensorErr != nil {
			for _, value := range owned {
				_ = value.Destroy()
			}
			return nil, fmt.Errorf("create %s tensor: %w", name, tensorErr)
		}
		owned = append(owned, tensor)
		inputValues = append(inputValues, tensor)
	}
	defer func() {
		for _, value := range owned {
			_ = value.Destroy()
		}
	}()

	s.mu.Lock()
	outputs := []ort.Value{nil}
	runErr := s.session.Run(inputValues, outputs)
	s.mu.Unlock()
	if runErr != nil {
		return nil, fmt.Errorf("run ONNX model: %w", runErr)
	}
	if outputs[0] == nil {
		return nil, fmt.Errorf("ONNX model returned no output")
	}
	defer outputs[0].Destroy()
	output, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("ONNX output %q is not float32 tensor", s.outputName)
	}
	data := output.GetData()
	shape := output.GetShape()
	if len(shape) == 2 && len(data) >= Dimensions {
		result := append([]float32(nil), data[:Dimensions]...)
		return normalize(result)
	}
	sequenceLength := len(mask)
	if len(shape) == 3 && shape[1] > 0 {
		sequenceLength = int(shape[1])
	}
	if len(data) < sequenceLength*Dimensions {
		return nil, fmt.Errorf("ONNX output has %d values, want at least %d", len(data), sequenceLength*Dimensions)
	}
	return meanPoolAndNormalize(data, mask, sequenceLength, Dimensions)
}

// meanPoolAndNormalize computes the attention-mask weighted mean over the token
// dimension of the ONNX output, then L2-normalizes the pooled vector, matching
// sentence-transformers' mean-pooling contract. Padded tokens (mask == 0) must
// not contribute to the mean.
func meanPoolAndNormalize(data []float32, mask []int64, sequenceLength, dimensions int) ([]float32, error) {
	result := make([]float32, dimensions)
	var count float32
	for token := 0; token < sequenceLength; token++ {
		if mask[token] == 0 {
			continue
		}
		count++
		row := data[token*dimensions : (token+1)*dimensions]
		for i, value := range row {
			result[i] += value
		}
	}
	if count == 0 {
		return nil, fmt.Errorf("tokenizer produced an empty attention mask")
	}
	for i := range result {
		result[i] /= count
	}
	return normalize(result)
}

func normalize(result []float32) ([]float32, error) {
	var norm float64
	for _, value := range result {
		norm += float64(value * value)
	}
	if norm == 0 {
		return nil, fmt.Errorf("ONNX model returned a zero vector")
	}
	norm = math.Sqrt(norm)
	for i := range result {
		result[i] = float32(float64(result[i]) / norm)
	}
	return result, nil
}

// Close releases this service's session. The process-wide ONNX environment is
// intentionally retained because other services may share it.
func (s *Service) Close() error {
	if s == nil || s.session == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.session.Destroy()
	s.session = nil
	return err
}

func (s *Service) ModelVersion() string { return ModelVersion }

func (s *Service) String() string {
	if s == nil {
		return "embedding service unavailable"
	}
	return "embedding model " + filepath.Base(s.modelPath)
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

func logEmbeddingFallback(reason string) {
	log.Printf("embedding vector retrieval degraded to FTS: %s", reason)
}
