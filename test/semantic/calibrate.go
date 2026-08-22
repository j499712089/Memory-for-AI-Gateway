package semantic

import (
	"fmt"
	"gateway/internal/embedding"
	"math"
	"os"
)

// CalibrationResult contains threshold calibration results for a language
type CalibrationResult struct {
	Language        string
	SynonymScores   []float64
	UnrelatedScores []float64
	MinSynonym      float64
	MaxUnrelated    float64
	Separation      float64
	UnkCounts       []UnkCount // For Chinese: track [UNK] token frequency
}

// UnkCount tracks unknown tokens in a text sample
type UnkCount struct {
	Text     string
	UnkCount int
	Total    int
}

// CalibrateThresholds runs probe text pairs through the embedding service
// and computes recommended thresholds based on observed cosine similarities.
func CalibrateThresholds(service *embedding.Service) (map[string]CalibrationResult, error) {
	if service == nil {
		return nil, fmt.Errorf("embedding service is required")
	}

	results := make(map[string]CalibrationResult)

	// Calibrate English thresholds
	enResult, err := calibrateLanguage(service, "en", EnglishSynonymous, EnglishUnrelated)
	if err != nil {
		return nil, fmt.Errorf("english calibration failed: %w", err)
	}
	results["en"] = enResult

	// Calibrate Chinese thresholds
	zhResult, err := calibrateLanguage(service, "zh", ChineseSynonymous, ChineseUnrelated)
	if err != nil {
		return nil, fmt.Errorf("chinese calibration failed: %w", err)
	}
	results["zh"] = zhResult

	return results, nil
}

func calibrateLanguage(service *embedding.Service, lang string, synonymPairs, unrelatedPairs []TextPair) (CalibrationResult, error) {
	result := CalibrationResult{
		Language:        lang,
		SynonymScores:   make([]float64, 0, len(synonymPairs)),
		UnrelatedScores: make([]float64, 0, len(unrelatedPairs)),
		MinSynonym:      math.MaxFloat64,
		MaxUnrelated:    -math.MaxFloat64,
	}

	// Compute synonym pair similarities
	for _, pair := range synonymPairs {
		vecA, err := service.Encode(pair.A)
		if err != nil {
			return result, fmt.Errorf("encode failed for '%s': %w", pair.A, err)
		}
		vecB, err := service.Encode(pair.B)
		if err != nil {
			return result, fmt.Errorf("encode failed for '%s': %w", pair.B, err)
		}
		sim := cosine(vecA, vecB)
		result.SynonymScores = append(result.SynonymScores, sim)
		if sim < result.MinSynonym {
			result.MinSynonym = sim
		}
	}

	// Compute unrelated pair similarities
	for _, pair := range unrelatedPairs {
		vecA, err := service.Encode(pair.A)
		if err != nil {
			return result, fmt.Errorf("encode failed for '%s': %w", pair.A, err)
		}
		vecB, err := service.Encode(pair.B)
		if err != nil {
			return result, fmt.Errorf("encode failed for '%s': %w", pair.B, err)
		}
		sim := cosine(vecA, vecB)
		result.UnrelatedScores = append(result.UnrelatedScores, sim)
		if sim > result.MaxUnrelated {
			result.MaxUnrelated = sim
		}
	}

	// Compute separation
	result.Separation = result.MinSynonym - result.MaxUnrelated

	return result, nil
}

// PrintCalibrationReport outputs calibration results to stdout in human-readable format
func PrintCalibrationReport(results map[string]CalibrationResult) {
	fmt.Println("=== Semantic Similarity Threshold Calibration Report ===\n")

	for _, lang := range []string{"en", "zh"} {
		result, ok := results[lang]
		if !ok {
			continue
		}

		fmt.Printf("## %s (Language: %s)\n", langName(lang), lang)
		fmt.Println()

		fmt.Printf("Synonymous pairs (%d samples):\n", len(result.SynonymScores))
		for i, score := range result.SynonymScores {
			fmt.Printf("  %.4f\n", score)
		}
		fmt.Printf("  → Min: %.4f\n", result.MinSynonym)
		fmt.Println()

		fmt.Printf("Unrelated pairs (%d samples):\n", len(result.UnrelatedScores))
		for i, score := range result.UnrelatedScores {
			fmt.Printf("  %.4f\n", score)
		}
		fmt.Printf("  → Max: %.4f\n", result.MaxUnrelated)
		fmt.Println()

		fmt.Printf("Separation: %.4f\n", result.Separation)
		fmt.Println()

		// Print recommendations
		fmt.Println("Recommended thresholds:")
		if result.Separation >= 0.15 {
			// Safe margins: synonym_min = min - 0.05, unrelated_max = max + 0.05
			synMin := result.MinSynonym - 0.05
			unrelMax := result.MaxUnrelated + 0.05
			fmt.Printf("  synonym_min: %.2f\n", synMin)
			fmt.Printf("  unrelated_max: %.2f\n", unrelMax)
			fmt.Printf("  separation_min: 0.15\n")
			fmt.Printf("  ✓ Thresholds are VALID (separation >= 0.15)\n")
		} else {
			fmt.Printf("  ✗ INSUFFICIENT SEPARATION (%.4f < 0.15)\n", result.Separation)
			fmt.Printf("  Model cannot reliably distinguish synonymous from unrelated text.\n")
			if lang == "zh" {
				fmt.Printf("  This is likely due to [UNK] token collapse in CJK text.\n")
				fmt.Printf("  Consider switching to a multilingual or Chinese-optimized model.\n")
			}
		}
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
	}
}

func langName(code string) string {
	switch code {
	case "en":
		return "English"
	case "zh":
		return "Chinese"
	default:
		return code
	}
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		aa, bb := float64(a[i]), float64(b[i])
		dot += aa * bb
		normA += aa * aa
		normB += bb * bb
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / math.Sqrt(normA*normB)
}

// RunCalibration is the main entry point for the calibration script.
// It can be invoked from a test or a standalone CLI tool.
func RunCalibration(modelPath, tokenizerPath string) error {
	// Validate paths
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("model file not found: %w", err)
	}
	if _, err := os.Stat(tokenizerPath); err != nil {
		return fmt.Errorf("tokenizer file not found: %w", err)
	}

	// Initialize embedding service
	service, err := embedding.NewService(modelPath, tokenizerPath)
	if err != nil {
		return fmt.Errorf("failed to initialize embedding service: %w", err)
	}

	// Run calibration
	results, err := CalibrateThresholds(service)
	if err != nil {
		return fmt.Errorf("calibration failed: %w", err)
	}

	// Print report
	PrintCalibrationReport(results)

	return nil
}
