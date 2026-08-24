package embedding

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	defaultRuntimeDLL = "onnxruntime.dll"

	// onnxruntime_go v1.35.0 is compiled against ORT_API_VERSION 29 (see
	// onnxruntime_c_api.h). ONNX Runtime bumps that number in lockstep with its
	// own release (1.x -> API x), so a library older than 1.29 cannot satisfy
	// this build. The model directory ships an onnxruntime.dll that passes it.
	requiredORTAPIVersion = 29
	minORTMajor           = 1
	minORTMinor           = 29
)

// runtimeMu guards the one-time process-wide onnxruntime environment setup.
var runtimeMu sync.Mutex

// initializeRuntime loads the onnxruntime shared library and initializes the
// global environment. The library is resolved explicitly from
// ONNXRUNTIME_SHARED_LIBRARY_PATH / ONNXRUNTIME_DLL_PATH or, failing that,
// from the sibling of the model file. It is an error when neither exists, so an
// incompatible onnxruntime.dll from the system PATH is never silently picked
// up. After a successful load the library version is asserted against what
// onnxruntime_go requires, failing loudly with both versions on a mismatch.
func initializeRuntime(modelPath string) error {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if ort.IsInitialized() {
		return nil
	}
	sharedPath, err := resolveRuntimeLibrary(modelPath)
	if err != nil {
		return err
	}
	ort.SetSharedLibraryPath(sharedPath)
	if err := ort.InitializeEnvironment(ort.WithLogLevelWarning()); err != nil {
		return fmt.Errorf("initialize onnxruntime with %s (requires ORT_API_VERSION %d): %w",
			sharedPath, requiredORTAPIVersion, err)
	}
	loaded := ort.GetVersion()
	major, minor, perr := parseORTVersion(loaded)
	if perr != nil || major < minORTMajor || (major == minORTMajor && minor < minORTMinor) {
		return fmt.Errorf("onnxruntime version mismatch: loaded %q from %s, "+
			"onnxruntime_go v1.35.0 requires ORT >= %d.%d (ORT_API_VERSION %d)",
			loaded, sharedPath, minORTMajor, minORTMinor, requiredORTAPIVersion)
	}
	log.Printf("onnxruntime %s loaded from %s", loaded, sharedPath)
	return nil
}

// resolveRuntimeLibrary picks the onnxruntime shared library to load. The
// explicit environment variables win; otherwise the DLL is expected next to the
// model file. A missing library is a hard error, never a silent fallback to the
// system PATH.
func resolveRuntimeLibrary(modelPath string) (string, error) {
	if p := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH"); p != "" {
		return p, nil
	}
	if p := os.Getenv("ONNXRUNTIME_DLL_PATH"); p != "" {
		return p, nil
	}
	candidate := filepath.Join(filepath.Dir(modelPath), defaultRuntimeDLL)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("%s not found next to model %s; "+
		"set ONNXRUNTIME_SHARED_LIBRARY_PATH to its absolute path",
		defaultRuntimeDLL, modelPath)
}

// parseORTVersion extracts the major and minor components from an ONNX Runtime
// version string such as "1.29.0" or "1.17.1".
func parseORTVersion(v string) (major, minor int, err error) {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("invalid onnxruntime version %q", v)
	}
	if major, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, fmt.Errorf("invalid onnxruntime version %q: %w", v, err)
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, fmt.Errorf("invalid onnxruntime version %q: %w", v, err)
	}
	return major, minor, nil
}
