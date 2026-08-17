package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AssertSafePath verifies that userPath resolves below basePath. Absolute
// paths are accepted here only when they are already inside basePath; this
// keeps the check usable by internal writers that have built an absolute path
// while SafeJoin remains strict for database/request-controlled paths.
func AssertSafePath(basePath, userPath string) error {
	if strings.TrimSpace(basePath) == "" {
		return fmt.Errorf("base path is empty")
	}
	base, err := filepath.Abs(filepath.Clean(basePath))
	if err != nil {
		return fmt.Errorf("resolve base path: %w", err)
	}

	candidate := userPath
	if !filepath.IsAbs(candidate) && filepath.VolumeName(candidate) == "" {
		candidate = filepath.Join(base, candidate)
	}
	candidate, err = filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return fmt.Errorf("resolve candidate path: %w", err)
	}
	if outsideBase(base, candidate) {
		return fmt.Errorf("path traversal detected: %s", userPath)
	}

	// A lexical check does not account for a symlink in an existing path
	// component. Resolve the existing portions of both paths before comparing
	// them, while retaining any not-yet-created suffix.
	resolvedBase, err := resolveExisting(base)
	if err != nil {
		return fmt.Errorf("resolve base path: %w", err)
	}
	resolvedCandidate, err := resolveExisting(candidate)
	if err != nil {
		return fmt.Errorf("resolve candidate path: %w", err)
	}
	if outsideBase(resolvedBase, resolvedCandidate) {
		return fmt.Errorf("path escapes base through symlink: %s", userPath)
	}
	return nil
}

// SafeJoin joins a relative path supplied by a request or database row and
// returns an absolute, cleaned path below basePath.
func SafeJoin(basePath, userPath string) (string, error) {
	if strings.TrimSpace(basePath) == "" {
		return "", fmt.Errorf("base path is empty")
	}
	if filepath.IsAbs(userPath) || filepath.VolumeName(userPath) != "" {
		return "", fmt.Errorf("absolute path is not allowed: %s", userPath)
	}
	if err := AssertSafePath(basePath, userPath); err != nil {
		return "", err
	}
	base, err := filepath.Abs(filepath.Clean(basePath))
	if err != nil {
		return "", fmt.Errorf("resolve base path: %w", err)
	}
	return filepath.Clean(filepath.Join(base, userPath)), nil
}

func outsideBase(base, candidate string) bool {
	rel, err := filepath.Rel(base, candidate)
	if err != nil || filepath.IsAbs(rel) {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolveExisting(path string) (string, error) {
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	missing := make([]string, 0, 4)
	current := path
	for {
		resolved, evalErr := filepath.EvalSymlinks(current)
		if evalErr == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Abs(filepath.Clean(resolved))
		}
		if !os.IsNotExist(evalErr) {
			return "", evalErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path, nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
