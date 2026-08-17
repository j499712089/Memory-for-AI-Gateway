package paths

import (
	"fmt"
	"path/filepath"
	"strings"
)

// AssertSafePath checks if a path is safe (no directory traversal)
func AssertSafePath(basePath, userPath string) error {
	// Clean both paths
	base := filepath.Clean(basePath)
	full := filepath.Join(base, userPath)
	full = filepath.Clean(full)

	// Check if full path is under base path
	if !strings.HasPrefix(full, base) {
		return fmt.Errorf("path traversal detected: %s", userPath)
	}

	return nil
}

// SafeJoin joins paths and ensures the result is under the base path
func SafeJoin(basePath string, userPath string) (string, error) {
	if err := AssertSafePath(basePath, userPath); err != nil {
		return "", err
	}
	return filepath.Join(basePath, userPath), nil
}
