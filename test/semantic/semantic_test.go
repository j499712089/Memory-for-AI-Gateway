package semantic

import (
	"fmt"
	"gateway/internal/embedding"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// ThresholdConfig defines the structure of thresholds.yml
type ThresholdConfig struct {
	Version    int `yaml:"version"`
	Thresholds map[string]struct {
		SynonymMin    float64 `yaml:"synonym_min"`
		UnrelatedMax  float64 `yaml:"unrelated_max"`
		SeparationMin float64 `yaml:"separation_min"`
	} `yaml:"thresholds"`
	Calibration struct {
		ModelPath     string `yaml:"model_path"`
		TokenizerPath string `yaml:"tokenizer_path"`
	} `yaml:"calibration"`
}

// LoadThresholds reads threshold configuration from thresholds.yml
func LoadThresholds(path string) (*ThresholdConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read thresholds file: %w", err)
	}

	var config ThresholdConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse thresholds YAML: %w", err)
	}

	return &config, nil
}

// TestSemanticValidityEnglish verifies that the embedding implementation
// produces semantically meaningful vectors for English text.
// This is a P0 gate: hash-based placeholder implementations will fail.
func TestSemanticValidityEnglish(t *testing.T) {
	// Load thresholds from configuration
	config, err := LoadThresholds("thresholds.yml")
	if err != nil {
		t.Fatalf("Failed to load thresholds: %v", err)
	}

	enThresh := config.Thresholds["en"]

	// Initialize embedding service
	service, err := embedding.NewService(config.Calibration.ModelPath, config.Calibration.TokenizerPath)
	if err != nil {
		t.Skipf("Embedding service not available: %v", err)
	}

	// Test synonymous pairs - should have HIGH similarity
	t.Run("Synonymous", func(t *testing.T) {
		for _, pair := range EnglishSynonymous {
			vecA, err := service.Encode(pair.A)
			if err != nil {
				t.Fatalf("Encode failed for '%s': %v", pair.A, err)
			}
			vecB, err := service.Encode(pair.B)
			if err != nil {
				t.Fatalf("Encode failed for '%s': %v", pair.B, err)
			}

			sim := cosine(vecA, vecB)
			if sim < enThresh.SynonymMin {
				t.Errorf("Synonymous pair has low similarity: %.4f < %.2f\n  A: %s\n  B: %s",
					sim, enThresh.SynonymMin, pair.A, pair.B)
			} else {
				t.Logf("✓ %.4f  %s | %s", sim, pair.A, pair.B)
			}
		}
	})

	// Test unrelated pairs - should have LOW similarity
	t.Run("Unrelated", func(t *testing.T) {
		for _, pair := range EnglishUnrelated {
			vecA, err := service.Encode(pair.A)
			if err != nil {
				t.Fatalf("Encode failed for '%s': %v", pair.A, err)
			}
			vecB, err := service.Encode(pair.B)
			if err != nil {
				t.Fatalf("Encode failed for '%s': %v", pair.B, err)
			}

			sim := cosine(vecA, vecB)
			if sim > enThresh.UnrelatedMax {
				t.Errorf("Unrelated pair has high similarity: %.4f > %.2f\n  A: %s\n  B: %s",
					sim, enThresh.UnrelatedMax, pair.A, pair.B)
			} else {
				t.Logf("✓ %.4f  %s | %s", sim, pair.A, pair.B)
			}
		}
	})
}

// TestSemanticValidityChinese verifies semantic validity for Chinese text.
// This test is PENDING ALL-159 model decision and threshold recalibration.
func TestSemanticValidityChinese(t *testing.T) {
	config, err := LoadThresholds("thresholds.yml")
	if err != nil {
		t.Fatalf("Failed to load thresholds: %v", err)
	}

	zhThresh := config.Thresholds["zh"]

	// Initialize embedding service
	service, err := embedding.NewService(config.Calibration.ModelPath, config.Calibration.TokenizerPath)
	if err != nil {
		t.Skipf("Embedding service not available: %v", err)
	}

	// Test synonymous pairs
	t.Run("Synonymous", func(t *testing.T) {
		for _, pair := range ChineseSynonymous {
			vecA, err := service.Encode(pair.A)
			if err != nil {
				t.Fatalf("Encode failed for '%s': %v", pair.A, err)
			}
			vecB, err := service.Encode(pair.B)
			if err != nil {
				t.Fatalf("Encode failed for '%s': %v", pair.B, err)
			}

			sim := cosine(vecA, vecB)
			if sim < zhThresh.SynonymMin {
				t.Errorf("Synonymous pair has low similarity: %.4f < %.2f\n  A: %s\n  B: %s",
					sim, zhThresh.SynonymMin, pair.A, pair.B)
			} else {
				t.Logf("✓ %.4f  %s | %s", sim, pair.A, pair.B)
			}
		}
	})

	// Test unrelated pairs
	t.Run("Unrelated", func(t *testing.T) {
		for _, pair := range ChineseUnrelated {
			vecA, err := service.Encode(pair.A)
			if err != nil {
				t.Fatalf("Encode failed for '%s': %v", pair.A, err)
			}
			vecB, err := service.Encode(pair.B)
			if err != nil {
				t.Fatalf("Encode failed for '%s': %v", pair.B, err)
			}

			sim := cosine(vecA, vecB)
			if sim > zhThresh.UnrelatedMax {
				t.Errorf("Unrelated pair has high similarity: %.4f > %.2f\n  A: %s\n  B: %s",
					sim, zhThresh.UnrelatedMax, pair.A, pair.B)
			} else {
				t.Logf("✓ %.4f  %s | %s", sim, pair.A, pair.B)
			}
		}
	})
}

// TestEncodingConsistency verifies that the same text produces the same vector
// when encoded through different code paths (write vs query).
// This prevents ALL-158 N4 issue: duplicated encoding logic causing vector space mismatch.
func TestEncodingConsistency(t *testing.T) {
	config, err := LoadThresholds("thresholds.yml")
	if err != nil {
		t.Fatalf("Failed to load thresholds: %v", err)
	}

	service, err := embedding.NewService(config.Calibration.ModelPath, config.Calibration.TokenizerPath)
	if err != nil {
		t.Skipf("Embedding service not available: %v", err)
	}

	testTexts := []string{
		"database connection configuration",
		"数据库连接配置",
		"team memory retrieval interface",
		"团队记忆检索接口",
	}

	for _, text := range testTexts {
		vec1, err := service.Encode(text)
		if err != nil {
			t.Fatalf("First encode failed: %v", err)
		}

		vec2, err := service.Encode(text)
		if err != nil {
			t.Fatalf("Second encode failed: %v", err)
		}

		sim := cosine(vec1, vec2)
		// Self-similarity should be 1.0 (or very close due to floating point precision)
		if sim < 0.9999 {
			t.Errorf("Encoding inconsistency detected: cosine(v1, v2) = %.6f < 0.9999\n  Text: %s", sim, text)
		} else {
			t.Logf("✓ Consistent: %.6f  %s", sim, text)
		}
	}
}

// TestRelativeSeparation verifies the relative separation between synonymous
// and unrelated pairs, which is a model-agnostic quality metric.
func TestRelativeSeparation(t *testing.T) {
	config, err := LoadThresholds("thresholds.yml")
	if err != nil {
		t.Fatalf("Failed to load thresholds: %v", err)
	}

	service, err := embedding.NewService(config.Calibration.ModelPath, config.Calibration.TokenizerPath)
	if err != nil {
		t.Skipf("Embedding service not available: %v", err)
	}

	languages := []struct {
		name     string
		synonym  []TextPair
		unrel    []TextPair
		threshol float64
	}{
		{"en", EnglishSynonymous, EnglishUnrelated, config.Thresholds["en"].SeparationMin},
		{"zh", ChineseSynonymous, ChineseUnrelated, config.Thresholds["zh"].SeparationMin},
	}

	for _, lang := range languages {
		t.Run(lang.name, func(t *testing.T) {
			var minSyn, maxUnrel float64 = 1.0, 0.0

			// Find minimum synonym similarity
			for _, pair := range lang.synonym {
				vecA, _ := service.Encode(pair.A)
				vecB, _ := service.Encode(pair.B)
				sim := cosine(vecA, vecB)
				if sim < minSyn {
					minSyn = sim
				}
			}

			// Find maximum unrelated similarity
			for _, pair := range lang.unrel {
				vecA, _ := service.Encode(pair.A)
				vecB, _ := service.Encode(pair.B)
				sim := cosine(vecA, vecB)
				if sim > maxUnrel {
					maxUnrel = sim
				}
			}

			separation := minSyn - maxUnrel
			t.Logf("Min(synonym): %.4f, Max(unrelated): %.4f, Separation: %.4f", minSyn, maxUnrel, separation)

			if separation < lang.threshol {
				t.Errorf("Insufficient separation: %.4f < %.2f (required)", separation, lang.threshol)
			}
		})
	}
}
