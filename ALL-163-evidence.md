# ALL-163 Evidence: multilingual ONNX asset provisioning

## Verdict

- **Asset provisioning: PASS.** The approved `paraphrase-multilingual-MiniLM-L12-v2` ONNX asset, matching tokenizer, and CPU runtime DLL are present at the agreed location.
- **ALL-154 end-to-end Go smoke: BLOCKED by adapter compatibility.** The checked-in Go tokenizer only accepts a WordPiece vocabulary map. The approved multilingual tokenizer is SentencePiece Unigram and encodes its vocabulary as an array, so `embedding.NewService` rejects it before ONNX session creation. This is a code compatibility gap, not a missing or corrupt model asset.
- Do not substitute `all-MiniLM-L6-v2`; it is the rejected English-only artifact.

## Target and provenance

Target directory:

```text
F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\
```

Files provisioned:

| File | Bytes | SHA-256 |
|---|---:|---|
| `model_quantized.onnx` | 118,308,126 | `66FC00F5F29AFCAFF34092E1BDD20008CA3918265A82FB9695A551E510CC4EBC` |
| `tokenizer.json` | 17,082,913 | `B60B6B43406A48BF3638526314F3D232D97058BC93472FF2DE930D43686FA441` |
| `onnxruntime.dll` | 16,149,344 | `69D8E6D3879A3B4001CDC74C8ED9CCC7E7F799A5B847059738323404519EC471` |

The model and tokenizer were downloaded from the `Xenova/paraphrase-multilingual-MiniLM-L12-v2` Hugging Face repository. The ONNX response reported `Content-Length: 118308126` and ETag `1bdde20dc7b6cefb4b4f81ef3d1f8c3090d8db5a452fd1db9fbfdc0dab422105`. The DLL is the Windows x64 runtime shipped in `github.com/yalue/onnxruntime_go@v1.35.0/test_data/onnxruntime.dll`, copied beside the model as required by `internal/embedding/service.go`.

## ONNX contract verification

Python `onnxruntime` loaded the model with `CPUExecutionProvider`:

```text
inputs:
  input_ids:       [batch_size, sequence_length], tensor(int64)
  attention_mask:  [batch_size, sequence_length], tensor(int64)
  token_type_ids:  [batch_size, sequence_length], tensor(int64)
output:
  last_hidden_state: [batch_size, sequence_length, 384], tensor(float)
tokenizer truncation: max_length=128, strategy=LongestFirst, direction=Right
tokenizer model: Unigram; vocab representation: array
```

The dynamic ONNX sequence dimension is intentionally bound to 128 by the tokenizer contract. The model loaded successfully and accepted all three required int64 inputs.

## Reproducible smoke tests

### Model inference (passed)

Run from PowerShell:

```powershell
python -c "import numpy as np, onnxruntime as ort; from tokenizers import Tokenizer; t=Tokenizer.from_file(r'F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json'); t.enable_truncation(max_length=128); t.enable_padding(length=128,pad_id=1,pad_token='<pad>'); e=t.encode('记忆系统支持中文语义检索'); s=ort.InferenceSession(r'F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\model_quantized.onnx',providers=['CPUExecutionProvider']); o=s.run(None, {'input_ids':np.asarray([e.ids],dtype=np.int64), 'attention_mask':np.asarray([e.attention_mask],dtype=np.int64), 'token_type_ids':np.asarray([e.type_ids],dtype=np.int64)})[0]; print(o.shape)"
```

Observed result: input arrays `[1,128]`, output `[1,128,384]`, pooled vector dimension `384`, L2 norm `1.0`, elapsed `13.95 ms`.

### Go runtime DLL (passed with existing WordPiece fixture)

```powershell
$env:EMBEDDING_MODEL_PATH='F:\AI\models\all-MiniLM-L6-v2\model_quantized.onnx'
$env:EMBEDDING_TOKENIZER_PATH='F:\AI\models\all-MiniLM-L6-v2\tokenizer.json'
$env:ONNXRUNTIME_SHARED_LIBRARY_PATH='F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\onnxruntime.dll'
go test ./internal/embedding -run TestEncodeReturns384DimensionsUnder50ms -v
```

Observed result: `PASS`, 384 dimensions, 1536-byte embedding blob, encode `3.979ms`. This proves the colocated DLL is loadable by the Go adapter.

### Go target-model load (blocked, reproducible)

```powershell
$env:EMBEDDING_MODEL_PATH='F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\model_quantized.onnx'
$env:EMBEDDING_TOKENIZER_PATH='F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\tokenizer.json'
$env:ONNXRUNTIME_SHARED_LIBRARY_PATH='F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\onnxruntime.dll'
go test ./internal/embedding -run TestEncodeReturns384DimensionsUnder50ms -v
```

Observed result: the test skips with `parse tokenizer: json: cannot unmarshal array into Go struct field tokenizerModel.model.vocab of type map[string]int`. `internal/embedding/tokenizer.go` currently requires `Model.Type == "WordPiece"`; the approved tokenizer reports `Model.Type == "Unigram"`.

## Required follow-up

ALL-154 can consume the provisioned files after the tokenizer adapter supports the approved Unigram/SentencePiece JSON format (including the 128-token contract). Until then, keep the implementation's end-to-end acceptance blocked and do not weaken the Chinese-query criteria or swap models.

Evidence collected: 2026-08-23 (Asia/Shanghai).
