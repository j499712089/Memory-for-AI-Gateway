## Local Embedding Runtime

The gateway loads the selected `paraphrase-multilingual-MiniLM-L12-v2` ONNX
model and its tokenizer from:

`F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\model_quantized.onnx`

Place the matching CPU runtime beside that model as:

`F:\AI\models\paraphrase-multilingual-MiniLM-L12-v2\onnxruntime.dll`

Alternatively set `ONNXRUNTIME_SHARED_LIBRARY_PATH` (or the compatibility
alias `ONNXRUNTIME_DLL_PATH`) to an absolute DLL path before starting the
gateway. The service refuses to start the embedding worker when the model,
tokenizer, or runtime cannot be loaded; retrieval records an observable FTS
degradation log until the dependency is restored.
