## ALL-154 implementation evidence

Commit: `d12818f`

Implemented against `ALL-158-completion-report.md` and
`ALL-153-revised-contract.md`:

- `internal/embedding/tokenizer.go`: loads the exported BertNormalizer and
  WordPiece vocabulary, emits 128-token `input_ids`, `attention_mask`, and
  `token_type_ids`.
- `internal/embedding/service.go`: loads `onnxruntime_go` v1.35.0, runs the
  model, applies attention-mask mean pooling and L2 normalization, and exposes
  one shared encoder for writes and queries.
- `internal/embedding/chunker.go`: bounded overlapping chunks with normalized
  document-vector aggregation.
- `internal/db/db.go` and `internal/db/assets_repo.go`: team DB startup now
  runs the idempotent asset migration, including `embedding` and
  `embedding_model_version`.
- `internal/retrieval/hybrid.go`: removes the query-side hash encoder, uses the
  injected embedding service, unions semantic and FTS candidates, and logs
  vector-to-FTS degradation.
- `internal/embedding/worker.go`: writes model-versioned vectors from the same
  service used by retrieval.
- `docs/embedding-runtime.md`: records the required model/DLL paths and
  explicit runtime environment variables.

Verification:

- `go test ./...` passed.
- `go vet ./...` passed.
- With `ONNXRUNTIME_SHARED_LIBRARY_PATH` set to the repository test runtime,
  `go test ./internal/embedding -run 'TestTokenizer|TestEncodeReturns' -v`
  passed; encode produced 384 dimensions in 4.6 ms and a 1536-byte FP32 blob.
- With the same runtime, semantic English validity and cross-path consistency
  tests passed (`TestSemanticValidityEnglish`, `TestEncodingConsistency`).

Deployment note: the selected multilingual model directory and matching
`onnxruntime.dll` are not present in this checkout. The gateway logs an
explicit startup warning and keeps FTS available until those assets are
installed.
