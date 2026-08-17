package l0

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gateway/internal/paths"
)

// WriteRequestFile writes an L0 request file atomically
// Uses .tmp + atomic rename pattern
func WriteRequestFile(memoryRoot, turnID, eventID string, requestData any) (string, string, error) {
	// Build path: L0_原始记录/{date}/{turnID}-request-{eventID}.jsonl
	date := time.Now().Format("2006-01-02")
	dir := filepath.Join(memoryRoot, "L0_原始记录", date)

	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", fmt.Errorf("create L0 directory: %w", err)
	}

	// Verify path safety
	if err := paths.AssertSafePath(memoryRoot, dir); err != nil {
		return "", "", fmt.Errorf("unsafe path: %w", err)
	}

	filename := fmt.Sprintf("%s-request-%s.jsonl", turnID, eventID)
	finalPath := filepath.Join(dir, filename)
	tmpPath := finalPath + ".tmp"

	// Marshal to JSON
	data, err := json.Marshal(requestData)
	if err != nil {
		return "", "", fmt.Errorf("marshal request: %w", err)
	}

	// Write to temp file
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return "", "", fmt.Errorf("write temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath) // Cleanup temp file on failure
		return "", "", fmt.Errorf("atomic rename: %w", err)
	}

	// Calculate content hash
	hash := sha256.Sum256(data)
	contentHash := "sha256:" + hex.EncodeToString(hash[:])

	// Return relative path for database storage
	relativePath := filepath.Join("L0_原始记录", date, filename)

	return relativePath, contentHash, nil
}

// WriteResponseFile writes an L0 response file atomically
func WriteResponseFile(memoryRoot, turnID, eventID string, responseData any) (string, string, error) {
	date := time.Now().Format("2006-01-02")
	dir := filepath.Join(memoryRoot, "L0_原始记录", date)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", fmt.Errorf("create L0 directory: %w", err)
	}

	if err := paths.AssertSafePath(memoryRoot, dir); err != nil {
		return "", "", fmt.Errorf("unsafe path: %w", err)
	}

	filename := fmt.Sprintf("%s-response-%s.jsonl", turnID, eventID)
	finalPath := filepath.Join(dir, filename)
	tmpPath := finalPath + ".tmp"

	data, err := json.Marshal(responseData)
	if err != nil {
		return "", "", fmt.Errorf("marshal response: %w", err)
	}

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return "", "", fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("atomic rename: %w", err)
	}

	hash := sha256.Sum256(data)
	contentHash := "sha256:" + hex.EncodeToString(hash[:])

	relativePath := filepath.Join("L0_原始记录", date, filename)

	return relativePath, contentHash, nil
}

// WriteCheckpointFile writes a streaming checkpoint file
func WriteCheckpointFile(memoryRoot, turnID string, deltaBuffer []byte) (string, error) {
	date := time.Now().Format("2006-01-02")
	checkpointDir := filepath.Join(memoryRoot, "90_运行数据", "检查点", date)

	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return "", fmt.Errorf("create checkpoint directory: %w", err)
	}

	if err := paths.AssertSafePath(memoryRoot, checkpointDir); err != nil {
		return "", fmt.Errorf("unsafe path: %w", err)
	}

	filename := fmt.Sprintf("%s-checkpoint-%d.jsonl", turnID, time.Now().Unix())
	finalPath := filepath.Join(checkpointDir, filename)
	tmpPath := finalPath + ".tmp"

	if err := os.WriteFile(tmpPath, deltaBuffer, 0644); err != nil {
		return "", fmt.Errorf("write checkpoint temp: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("atomic rename checkpoint: %w", err)
	}

	return finalPath, nil
}
