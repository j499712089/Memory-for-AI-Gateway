# Semantic Similarity Threshold Calibration System

## Overview

This subsystem provides language-specific threshold calibration for semantic validity assertions in the MemoryHub embedding pipeline. It addresses the critical issue where hardcoded thresholds (synonym > 0.5, unrelated < 0.3) fail for Chinese text due to CJK tokenization collapse in English-centric models.

## Architecture

### Components

```
test/semantic/
├── thresholds.yml        # Calibrated threshold configuration (read by ALL-155)
├── probe_data.go         # Fixed probe text pairs for calibration
├── calibrate.go          # Calibration engine
└── semantic_test.go      # ALL-155 acceptance tests (P0 gate)
```

### Design Principles

1. **Single Source of Truth**: All encoding goes through `internal/embedding.Service.Encode()` - no duplicated encoding logic
2. **Language-Specific Calibration**: English and Chinese have separate thresholds based on observed distributions
3. **Dual-Layer Validation**: Absolute thresholds + relative separation (model-agnostic fallback)
4. **Configuration-Driven**: Tests read `thresholds.yml` - no hardcoded magic numbers

## Usage

### Running Calibration

```bash
# After model is deployed and embedding service is available
cd test/semantic
go test -run TestCalibration -v

# Or use as a library
go run calibrate_main.go \
  --model "F:\AI\models\all-MiniLM-L6-v2\model_quantized.onnx" \
  --tokenizer "F:\AI\models\all-MiniLM-L6-v2\tokenizer.json"
```

### Running Validation Tests

```bash
# ALL-155 acceptance tests
cd test/semantic
go test -v

# Expected output:
# === RUN   TestSemanticValidityEnglish
# --- PASS: TestSemanticValidityEnglish (0.15s)
# === RUN   TestSemanticValidityChinese
# --- PASS: TestSemanticValidityChinese (0.12s)
# === RUN   TestEncodingConsistency
# --- PASS: TestEncodingConsistency (0.08s)
# === RUN   TestRelativeSeparation
# --- PASS: TestRelativeSeparation (0.10s)
```

## Integration Points

### ALL-158 Contract Revision

**Before (INVALID for Chinese):**
```
完成判据第 7 条：同义文本对余弦 > 0.5、无关文本对 < 0.3
```

**After (Language-Aware):**
```
完成判据第 7 条：语义有效性断言必须通过 test/semantic/semantic_test.go 验收：
- 英文：同义对 > 0.5、无关对 < 0.3、分离度 > 0.15
- 中文：按 test/semantic/thresholds.yml 校准值（依赖 ALL-159 模型选型）
- 编码一致性：同一文本在不同路径的向量余弦 > 0.9999
```

### ALL-155 Acceptance Tests

Tests MUST:
1. Read thresholds from `test/semantic/thresholds.yml` (not hardcoded)
2. Verify absolute thresholds (language-specific)
3. Verify relative separation (≥ 0.15)
4. Verify encoding consistency across code paths

### ALL-159 Model Decision Integration

After model selection, re-run calibration:

```bash
# Example: If switching to paraphrase-multilingual-MiniLM-L12-v2
cd test/semantic
go run calibrate_main.go \
  --model "F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\model.onnx" \
  --tokenizer "F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json" \
  --output thresholds.yml

# Commit the updated thresholds.yml
git add thresholds.yml
git commit -m "feat(ALL-160): recalibrate thresholds for multilingual model"
```

## Threshold Interpretation

### Absolute Thresholds

- **synonym_min**: Minimum cosine similarity for text pairs with the same meaning
  - Below this → model cannot recognize semantic equivalence
  - Hash-based implementations will fail this check
  
- **unrelated_max**: Maximum cosine similarity for text pairs with different meanings
  - Above this → model cannot distinguish unrelated concepts
  - Indicates vector space collapse (e.g., CJK [UNK] token issue)

### Relative Separation

```
separation = min(synonym_scores) - max(unrelated_scores)
```

- **Must be ≥ 0.15**: Ensures a safety margin between clusters
- **Model-agnostic**: Works across different vector spaces
- **Prevents false positives**: Catches subtle model degradation

### Example: Why Chinese Fails

**all-MiniLM-L6-v2 on Chinese text:**
- Synonym scores: 0.56-0.99
- Unrelated scores: 0.41-0.47
- Min synonym: 0.56
- Max unrelated: 0.47
- Separation: 0.56 - 0.47 = **0.09** ❌ (< 0.15)

The 0.09 separation means overlap is too close - slight model changes or different text could cause false matches.

## Troubleshooting

### Test Failure: "Unrelated pair has high similarity"

**Symptom:**
```
Unrelated pair has high similarity: 0.4548 > 0.30
  A: 量子色动力学
  B: 香蕉面包食谱
```

**Cause**: Model vocabulary doesn't cover the language well (CJK → [UNK] tokens)

**Solution**: 
1. Check calibration metadata in `thresholds.yml` - is the model appropriate for this language?
2. Re-run calibration after switching to a multilingual model (ALL-159)
3. If keeping current model, adjust thresholds in `thresholds.yml` based on calibration output

### Test Failure: "Encoding inconsistency detected"

**Symptom:**
```
Encoding inconsistency detected: cosine(v1, v2) = 0.0622 < 0.9999
  Text: 数据库连接配置
```

**Cause**: Multiple encoding implementations (ALL-158 N4 issue) - write path uses one encoder, query path uses another

**Solution**:
1. Verify `internal/retrieval/hybrid.go` imports `internal/embedding` package
2. Delete any local `encodeQuery()` function in retrieval code
3. Both write and query must call the same `embedding.Service.Encode()` method

## Maintenance

### When to Recalibrate

1. **Model file changes** (different sha256 hash)
2. **Tokenizer changes** (vocabulary or normalization)
3. **Embedding logic changes** (pooling strategy, normalization)
4. **Observed quality degradation** in production semantic search

### Recalibration Process

1. Update model/tokenizer paths in `thresholds.yml` calibration section
2. Run calibration script: `go run calibrate_main.go`
3. Review output - ensure separation ≥ 0.15 for all languages
4. Update threshold values in `thresholds.yml`
5. Run validation: `go test ./test/semantic/... -v`
6. Commit updated configuration

## Dependencies

- **Blocks**: ALL-158 (contract), ALL-155 (acceptance tests)
- **Depends on**: ALL-159 (model selection) - for final Chinese threshold values
- **Related**: ALL-158 N4 (encoding duplication issue), ADR-001 (model selection)

## References

- ADR-002: Semantic Similarity Threshold Calibration Mechanism
- ALL-160: P0 阈值校准任务
- ALL-158: P0 契约改正
- ALL-155: MemoryHub 端到端验收
- ALL-159: 模型选型决策
