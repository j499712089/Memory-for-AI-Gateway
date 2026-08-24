package embedding

import (
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEncodeReturns384Dimensions(t *testing.T) {
	model := os.Getenv("EMBEDDING_MODEL_PATH")
	tokenizer := os.Getenv("EMBEDDING_TOKENIZER_PATH")
	if model == "" {
		model = approvedModelPath
	}
	if tokenizer == "" {
		tokenizer = approvedTokenizerPath
	}
	runtimePath := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	if runtimePath == "" {
		runtimePath = os.Getenv("ONNXRUNTIME_DLL_PATH")
	}
	if runtimePath == "" {
		runtimePath = filepath.Join(filepath.Dir(model), defaultRuntimeDLL)
	}
	for name, path := range map[string]string{"model": model, "tokenizer": tokenizer, "onnxruntime": runtimePath} {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			t.Fatalf("approved asset gate failed for %s=%q: %v", name, path, err)
		}
	}
	service, err := NewService(model, tokenizer)
	if err != nil {
		t.Fatalf("approved embedding gate failed: %v", err)
	}
	defer service.Close()
	if got := service.tokenizer.PadID(); got != 1 {
		t.Fatalf("approved tokenizer pad id = %d, want artifact contract 1", got)
	}
	start := time.Now()
	vector, err := service.Encode("test")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(vector) != Dimensions {
		t.Fatalf("expected %d dimensions, got %d", Dimensions, len(vector))
	}
	if service.ModelVersion() != ModelVersion {
		t.Fatalf("model version = %q, want %q", service.ModelVersion(), ModelVersion)
	}
	var norm float64
	for _, value := range vector {
		norm += float64(value * value)
	}
	if norm < 0.999 || norm > 1.001 {
		t.Fatalf("embedding norm = %f, want 1", norm)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("encode took %s, want <500ms", elapsed)
	}
	blob := Float32ToBytes(vector)
	decoded, err := BytesToFloat32(blob)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != Dimensions {
		t.Fatalf("blob round trip returned %d dimensions", len(decoded))
	}
	t.Logf("embedding dimensions=%d elapsed=%s blob_bytes=%d model_sha256=%s tokenizer_sha256=%s runtime_sha256=%s", len(vector), elapsed, len(blob), sha256File(t, model), sha256File(t, tokenizer), sha256File(t, runtimePath))
}

// TestMeanPoolExcludesPadding proves the attention-mask weighted mean ignores
// padded tokens. The expected vector is computed independently in the test; a
// mutation that lets a masked token participate (mask == 0 treated as attended)
// changes the pooled direction and fails the assertions below.
func TestMeanPoolExcludesPadding(t *testing.T) {
	const dimensions = 4
	// Token 1 is padding. Its vector is not collinear with the attended tokens,
	// so if it ever participates, the normalized mean changes direction.
	data := []float32{
		1, 0, 0, 0, // token 0, mask 1
		0, 2, 0, 0, // token 1, mask 0 (padding)
		0, 3, 0, 0, // token 2, mask 1
	}
	mask := []int64{1, 0, 1}

	got, err := meanPoolAndNormalize(data, mask, 3, dimensions)
	if err != nil {
		t.Fatal(err)
	}

	// Independent expected vector: mean over the attended tokens only, then L2
	// normalization, computed here rather than by the function under test.
	var mean [dimensions]float32
	for token := 0; token < 3; token++ {
		if mask[token] == 0 {
			continue
		}
		for i := 0; i < dimensions; i++ {
			mean[i] += data[token*dimensions+i]
		}
	}
	for i := range mean {
		mean[i] /= 2 // two attended tokens
	}
	var norm float64
	for _, v := range mean {
		norm += float64(v * v)
	}
	norm = math.Sqrt(norm)
	var expected [dimensions]float32
	for i, v := range mean {
		expected[i] = float32(float64(v) / norm)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("mean pool component %d = %v, want %v", i, got[i], expected[i])
		}
	}

	// Mutation probe: if padding is marked attended (mask != 0), the mean must
	// change. A predicate that treated mask == 0 as attended would return the
	// same vector here and fail.
	withPadding := []int64{1, 2, 1}
	mutated, err := meanPoolAndNormalize(data, withPadding, 3, dimensions)
	if err != nil {
		t.Fatal(err)
	}
	same := true
	for i := range expected {
		if mutated[i] != expected[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatalf("padding participated in the mean: mask %v produced the same vector as mask %v", withPadding, mask)
	}
}

const (
	approvedModelPath     = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\model_quantized.onnx`
	approvedTokenizerPath = `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json`
)

func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("hash %q: %v", path, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
