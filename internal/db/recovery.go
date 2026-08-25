package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gateway/internal/hashutil"
	"gateway/internal/l0"
)

type RecoveryOptions struct {
	OutboxHandler func(context.Context, OutboxRecord) error
	BufferHandler func(context.Context, BufferRecord) error
}

type OutboxRecord struct {
	ID          string
	EventID     string
	RequestID   string
	PayloadJSON string
	RetryCount  int
}

type BufferRecord struct {
	ID          string
	RequestID   string
	PayloadJSON string
	FilePath    string
	SHA256      string
}

type RecoveryReport struct {
	ReclaimedLeases   int
	ReplayedOutbox    int
	ReplayedBuffers   int
	InconsistentFiles []string
	RemovedTempFiles  int
	GapCount          int
	IntegrityOK       bool
}

// Recover performs the ordered startup recovery pass. Every operation is
// idempotent, so a process can safely retry the pass after an interrupted boot.
func Recover(ctx context.Context, database *sql.DB, memoryRoot string, options RecoveryOptions) (RecoveryReport, error) {
	var report RecoveryReport
	if database == nil {
		return report, fmt.Errorf("recovery database is nil")
	}
	now := time.Now().UTC()
	if count, err := database.ExecContext(ctx, `UPDATE jobs SET status='pending', lease_token=NULL, lease_until=NULL, worker_id=NULL, heartbeat_at=NULL, started_at=NULL WHERE status='processing' AND lease_until IS NOT NULL AND lease_until <= ?`, now.Format(time.RFC3339Nano)); err != nil {
		return report, fmt.Errorf("recover leases: %w", err)
	} else if rows, _ := count.RowsAffected(); rows > 0 {
		report.ReclaimedLeases = int(rows)
	}

	if err := replayOutbox(ctx, database, options.OutboxHandler, &report); err != nil {
		return report, err
	}
	replayed, err := ReplayBuffers(ctx, database, memoryRoot, options.BufferHandler)
	if err != nil {
		return report, err
	}
	report.ReplayedBuffers = replayed
	if memoryRoot != "" {
		_ = filepath.WalkDir(memoryRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry == nil || entry.IsDir() {
				return nil
			}
			if strings.HasSuffix(entry.Name(), ".tmp") {
				relative, relErr := filepath.Rel(memoryRoot, path)
				if relErr == nil {
					_, relErr = safeRecoveryPath(memoryRoot, relative)
				}
				if relErr == nil {
					if os.Remove(path) == nil {
						report.RemovedTempFiles++
					}
				}
			}
			return nil
		})
		if files, err := scanInconsistentFiles(ctx, database, memoryRoot); err == nil {
			report.InconsistentFiles = files
		} else {
			return report, err
		}
	}
	var integrity string
	if err := database.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return report, err
	}
	report.IntegrityOK = strings.EqualFold(integrity, "ok")
	report.GapCount, _ = markSequenceGaps(ctx, database)
	return report, nil
}

func scanInconsistentFiles(ctx context.Context, database *sql.DB, memoryRoot string) ([]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT file_path, file_hash FROM event_files ORDER BY written_at DESC LIMIT 1000`)
	if err != nil {
		return nil, fmt.Errorf("query event files: %w", err)
	}
	defer rows.Close()
	var inconsistent []string
	for rows.Next() {
		var filePath, fileHash string
		if err := rows.Scan(&filePath, &fileHash); err != nil {
			return nil, err
		}
		fullPath, err := safeRecoveryPath(memoryRoot, filePath)
		if err != nil {
			inconsistent = append(inconsistent, filePath)
			continue
		}
		matches, err := l0.VerifyFileHash(fullPath, fileHash)
		if err != nil {
			return nil, fmt.Errorf("verify hash for %s: %w", filePath, err)
		}
		if !matches {
			inconsistent = append(inconsistent, filePath)
		}
	}
	return inconsistent, rows.Err()
}

func replayOutbox(ctx context.Context, database *sql.DB, handler func(context.Context, OutboxRecord) error, report *RecoveryReport) error {
	rows, err := database.QueryContext(ctx, `SELECT id, COALESCE(event_id,''), request_id, payload_json, retry_count FROM outbox WHERE status='pending' AND (next_retry_at IS NULL OR next_retry_at <= ?) ORDER BY created_at`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("query outbox recovery: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry OutboxRecord
		if err := rows.Scan(&entry.ID, &entry.EventID, &entry.RequestID, &entry.PayloadJSON, &entry.RetryCount); err != nil {
			return err
		}
		claim, err := database.ExecContext(ctx, `UPDATE outbox SET status='processing' WHERE id=? AND status='pending'`, entry.ID)
		if err != nil {
			return err
		}
		if affected, _ := claim.RowsAffected(); affected != 1 {
			continue
		}
		if handler == nil {
			var exists int
			if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM turn_events WHERE id=?`, entry.EventID).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				_, _ = database.ExecContext(ctx, `UPDATE outbox SET status='failed', last_error=?, processed_at=? WHERE id=?`, "referenced event is missing", time.Now().UTC().Format(time.RFC3339Nano), entry.ID)
				continue
			}
		} else if err := handler(ctx, entry); err != nil {
			var maxRetries int
			if queryErr := database.QueryRowContext(ctx, `SELECT max_retries FROM outbox WHERE id=?`, entry.ID).Scan(&maxRetries); queryErr != nil {
				return queryErr
			}
			status := "pending"
			if entry.RetryCount+1 > maxRetries {
				status = "dead_letter"
			}
			_, updateErr := database.ExecContext(ctx, `UPDATE outbox SET status=?, retry_count=retry_count+1, last_error=?, next_retry_at=? WHERE id=?`, status, err.Error(), time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano), entry.ID)
			if updateErr != nil {
				return updateErr
			}
			continue
		}
		if _, err := database.ExecContext(ctx, `UPDATE outbox SET status='done', processed_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), entry.ID); err != nil {
			return err
		}
		report.ReplayedOutbox++
	}
	return rows.Err()
}

// ReconcileBufferFiles indexes buffered event files under the local buffer
// directory that have no local_buffer ledger row. The buffer file is the
// source of truth (§1.5) and the ledger row is the index the replay chain
// scans; when the best-effort ledger INSERT races a busy database and is
// dropped, the file would otherwise be orphaned and its event never replayed.
// Reconciliation runs before every replay sweep (startup recovery and the
// periodic maintenance loop) so degraded events always re-enter SQLite and
// never vanish silently (ALL-81).
func ReconcileBufferFiles(ctx context.Context, database *sql.DB, memoryRoot string) (int, error) {
	if database == nil {
		return 0, fmt.Errorf("buffer reconciliation database is nil")
	}
	dir := LocalBufferDir(memoryRoot)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read local buffer dir: %w", err)
	}
	indexed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		var payload BufferEventPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			continue
		}
		if payload.BufferKey == "" {
			payload.BufferKey = payload.RequestID
		}
		if payload.Direction == "" {
			payload.Direction = "inbound"
		}
		if payload.Sequence == 0 {
			payload.Sequence = 1
		}
		if payload.EventType == "" {
			continue
		}
		if payload.CreatedAt == "" {
			payload.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		relative, err := filepath.Rel(memoryRoot, fullPath)
		if err != nil {
			continue
		}
		sha := "sha256:" + hashutil.SHA256Bytes(data)
		result, err := database.ExecContext(ctx, `
			INSERT OR IGNORE INTO local_buffer (
				id, event_id, request_id, turn_id, payload_json, status,
				file_path, sha256, created_at
			) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?)
		`, BufferRecordID(payload), payload.EventID, payload.RequestID, payload.TurnID, string(data), filepath.ToSlash(relative), sha, payload.CreatedAt)
		if err != nil {
			return indexed, fmt.Errorf("index buffered file %s: %w", entry.Name(), err)
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			indexed++
		}
	}
	return indexed, nil
}

// ReplayBuffers replays all pending local_buffer rows back into SQLite through
// handler, marking each row 'replayed' on success or 'failed' when the handler
// rejects it. It is the per-row worker for both the startup recovery pass and
// the periodic maintenance loop. Orphaned buffer files are reconciled into the
// ledger first so events whose best-effort index INSERT was dropped under
// contention still get replayed.
//
// A nil handler leaves pending rows untouched: without a replay implementation
// the events are NOT dropped — they stay pending for a later pass that does
// have one. This is the contract that keeps the degraded path from silently
// losing memory: only a handler that actually persisted the event (or verified
// it already exists) may move a row out of 'pending'.
func ReplayBuffers(ctx context.Context, database *sql.DB, memoryRoot string, handler func(context.Context, BufferRecord) error) (int, error) {
	if database == nil {
		return 0, fmt.Errorf("buffer replay database is nil")
	}
	if _, err := ReconcileBufferFiles(ctx, database, memoryRoot); err != nil {
		return 0, err
	}
	rows, err := database.QueryContext(ctx, `SELECT id, COALESCE(request_id,''), payload_json, file_path, sha256 FROM local_buffer WHERE status='pending' ORDER BY created_at`)
	if err != nil {
		return 0, fmt.Errorf("query buffer recovery: %w", err)
	}
	defer rows.Close()
	replayed := 0
	for rows.Next() {
		var entry BufferRecord
		if err := rows.Scan(&entry.ID, &entry.RequestID, &entry.PayloadJSON, &entry.FilePath, &entry.SHA256); err != nil {
			return replayed, err
		}
		fullPath, err := safeRecoveryPath(memoryRoot, entry.FilePath)
		if err != nil {
			_, _ = database.ExecContext(ctx, `UPDATE local_buffer SET status='failed', retry_count=retry_count+1 WHERE id=?`, entry.ID)
			continue
		}
		matches, err := l0.VerifyFileHash(fullPath, entry.SHA256)
		if err != nil || !matches {
			_, _ = database.ExecContext(ctx, `UPDATE local_buffer SET status='failed', retry_count=retry_count+1 WHERE id=?`, entry.ID)
			continue
		}
		if handler != nil {
			if err := handler(ctx, entry); err != nil {
				_, _ = database.ExecContext(ctx, `UPDATE local_buffer SET status='failed', retry_count=retry_count+1 WHERE id=?`, entry.ID)
				continue
			}
		} else {
			// No replay implementation available: leave the row pending for a
			// later pass. Marking it 'replayed' here would drop the event.
			continue
		}
		if _, err := database.ExecContext(ctx, `UPDATE local_buffer SET status='replayed', replayed_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), entry.ID); err != nil {
			return replayed, err
		}
		replayed++
	}
	return replayed, rows.Err()
}

func safeRecoveryPath(root, relative string) (string, error) {
	if root == "" || relative == "" {
		return "", fmt.Errorf("recovery path is empty")
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, full)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("recovery path escapes memory root")
	}
	return full, nil
}

func markSequenceGaps(ctx context.Context, database *sql.DB) (int, error) {
	rows, err := database.QueryContext(ctx, `SELECT conversation_id, turn_seq, turn_id FROM turn_ledger ORDER BY conversation_id, turn_seq`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	last := map[string]int{}
	gaps := 0
	for rows.Next() {
		var conversation, turnID string
		var seq int
		if err := rows.Scan(&conversation, &seq, &turnID); err != nil {
			return gaps, err
		}
		if previous, ok := last[conversation]; !ok && seq != 1 {
			gaps++
			_, _ = database.ExecContext(ctx, `UPDATE turn_ledger SET gap_detected=1 WHERE turn_id=?`, turnID)
		} else if ok && seq != previous+1 {
			gaps++
			_, _ = database.ExecContext(ctx, `UPDATE turn_ledger SET gap_detected=1 WHERE turn_id=?`, turnID)
		}
		last[conversation] = seq
	}
	return gaps, rows.Err()
}

// Keep hashutil linked here for callers that want the same canonical digest
// while constructing local buffer records.
var _ = hashutil.SHA256Bytes
