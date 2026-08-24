## Remediation Evidence

Remediation aligns the approved multilingual `Unigram` tokenizer with its exported `Precompiled` normalizer. The loader decodes and validates `precompiled_charsmap`, rejects missing or unsupported normalizers, checks `padding.pad_id=1`, and uses the same tokenizer path for embedding writes and retrieval.

### Approved assets

- Model: `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\model_quantized.onnx`
- Model SHA-256: `66fc00f5f29afcaff34092e1bdd20008ca3918265a82fb9695a551e510cc4ebc`
- Tokenizer: `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json`
- Tokenizer SHA-256: `b60b6b43406a48bf3638526314f3d232d97058bc93472ff2de930d43686fa441`
- Runtime: `F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\onnxruntime.dll`
- Runtime SHA-256: `69d8e6d3879a3b4001cdc74c8ed9ccc7e7f799a5b847059738323404519ec471`
- Normalizer: `Precompiled`; charsmap base64 length `316720`; decoded bytes `237539`; SHA-256 `0942789e0111452f1cc446f70036e26d24458c3c7858c53cc12ca4d87531279b`
- Model: `Unigram`; truncation: `128`; padding id: `1`

### Differential goldens

The Go contract test `TestApprovedMultilingualUnigramDifferentialGoldens` matches trusted `tokenizers` outputs exactly:

```text
Hello, world! -> [0 35378 4 8999 38 2]
你好，世界！ -> [0 6 124084 4 3221 38 2]
hello world -> [0 33600 31 8999 2]
Café -> [0 61427 2]
foo<TAB>bar -> [0 5775 31 1909 2]
foo<LF>bar -> [0 5775 31 1909 2]
NUL<0>x -> [0 541 11176 3 425 2]
𠀀 -> [0 6 3 2]
🧪🧪 -> [0 6 3 2]
abc🧪xyz -> [0 1563 238 3 50878 169 2]
😀 -> [0 21119 2]
é -> [0 393 2]
ＡＢＣ -> [0 47457 2]
U+0301 -> [0 3309 2]
```

The same test verifies fixed shape `128`, right padding with id `1`, and the long Chinese input truncating before `</s>`.

### Verification

- `go test ./internal/embedding -run '^TestApprovedMultilingualUnigramDifferentialGoldens$' -count=1 -v`: PASS; charsmap and all listed goldens printed.
- `go test ./internal/embedding -run '^TestEncodeReturns384Dimensions$' -count=1 -v`: PASS; `384` float32 values, L2 norm `1`, `1536` byte round trip, observed encode `51.389ms` (the assertion is `<500ms`, matching the test name).
- `go test ./... -count=1`: PASS.
- `go vet ./...`: PASS.
- Missing-asset gate: `EMBEDDING_MODEL_PATH=C:\missing\approved-model.onnx go test ./internal/embedding -run '^TestEncodeReturns384Dimensions$' -count=1 -v` exits `1` with `approved asset gate failed`; no skip path is used by the approved gate.

### Commit

This evidence is for the fresh remediation commit recorded with the issue comment. Independent QA remains the acceptance authority; this issue must stay `in_review`.
