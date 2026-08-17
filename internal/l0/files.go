package l0

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// RegisterEventFile registers an event file in the event_files ledger
// Prevents duplicate writes on process restart
func RegisterEventFile(db *sql.DB, eventID, filePath string, fileSize int64, fileHash string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err := db.Exec(`
		INSERT OR IGNORE INTO event_files (event_id, file_path, file_hash, size_bytes, written_at)
		VALUES (?, ?, ?, ?, ?)
	`, eventID, filePath, fileHash, fileSize, now)

	if err != nil {
		return fmt.Errorf("register event file: %w", err)
	}

	return nil
}

// CheckEventFileExists checks if an event file already exists
func CheckEventFileExists(db *sql.DB, eventID string) (bool, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM event_files WHERE event_id = ?
	`, eventID).Scan(&count)

	if err != nil {
		return false, fmt.Errorf("check event file: %w", err)
	}

	return count > 0, nil
}

// VerifyFileHash verifies the hash of a file matches the expected hash
// Used for startup integrity checks
func VerifyFileHash(filePath, expectedHash string) (bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false, fmt.Errorf("hash file: %w", err)
	}

	actualHash := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	return actualHash == expectedHash, nil
}

// ScanInconsistentFiles scans for files with mismatched hashes
// Called during startup recovery
func ScanInconsistentFiles(db *sql.DB, memoryRoot string) ([]string, error) {
	rows, err := db.Query(`
		SELECT file_path, file_hash FROM event_files
		ORDER BY written_at DESC
		LIMIT 1000
	`)
	if err != nil {
		return nil, fmt.Errorf("query event files: %w", err)
	}
	defer rows.Close()

	var inconsistent []string

	for rows.Next() {
		var filePath, fileHash string
		if err := rows.Scan(&filePath, &fileHash); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		fullPath := filepath.Join(memoryRoot, filePath)
		matches, err := VerifyFileHash(fullPath, fileHash)
		if err != nil {
			return nil, fmt.Errorf("verify hash for %s: %w", filePath, err)
		}

		if !matches {
			inconsistent = append(inconsistent, filePath)
		}
	}

	return inconsistent, rows.Err()
}
