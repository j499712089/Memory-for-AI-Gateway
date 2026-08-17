package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gateway/internal/db"
	"gateway/internal/hashutil"
	"gateway/internal/idgen"
	"gateway/internal/l0"
	"gateway/internal/turn"
)

type OutboxEntry struct {
	ID          string
	EventID     string
	RequestID   string
	PayloadJSON string
	RetryCount  int
}

// EnqueueCompensation links an outbox failure to the high-priority worker
// queue while retaining the original request payload for replay.
func EnqueueCompensation(ctx context.Context, queue *Queue, kind, teamID, requestID string, payload any) (string, error) {
	if queue == nil {
		return "", fmt.Errorf("queue is nil")
	}
	return queue.Enqueue(ctx, Job{Queue: kind, TeamID: teamID, Payload: payload, PartitionKey: "request:" + requestID})
}

// ReplayOutbox drains due pending outbox rows through handler. A failed
// handler leaves the row pending and records an exponential retry schedule.
func ReplayOutbox(ctx context.Context, database *sql.DB, handler func(context.Context, OutboxEntry) error) (int, error) {
	if database == nil {
		return 0, fmt.Errorf("outbox database is nil")
	}
	rows, err := database.QueryContext(ctx, `SELECT id, COALESCE(event_id,''), request_id, payload_json, retry_count FROM outbox WHERE status='pending' AND (next_retry_at IS NULL OR next_retry_at <= ?) ORDER BY created_at`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var entry OutboxEntry
		if err := rows.Scan(&entry.ID, &entry.EventID, &entry.RequestID, &entry.PayloadJSON, &entry.RetryCount); err != nil {
			return count, err
		}
		claim, err := database.ExecContext(ctx, `UPDATE outbox SET status='processing' WHERE id=? AND status='pending'`, entry.ID)
		if err != nil {
			return count, err
		}
		if affected, _ := claim.RowsAffected(); affected != 1 {
			continue
		}
		if handler == nil {
			handler = func(context.Context, OutboxEntry) error { return nil }
		}
		if err := handler(ctx, entry); err != nil {
			delay := RetryDelay(entry.RetryCount + 1)
			var maxRetries int
			if queryErr := database.QueryRowContext(ctx, `SELECT max_retries FROM outbox WHERE id=?`, entry.ID).Scan(&maxRetries); queryErr != nil {
				return count, queryErr
			}
			status := "pending"
			if entry.RetryCount+1 > maxRetries {
				status = "dead_letter"
			}
			_, updateErr := database.ExecContext(ctx, `UPDATE outbox SET status=?, retry_count=retry_count+1, last_error=?, next_retry_at=? WHERE id=?`, status, err.Error(), time.Now().UTC().Add(delay).Format(time.RFC3339Nano), entry.ID)
			if updateErr != nil {
				return count, updateErr
			}
			continue
		}
		if _, err := database.ExecContext(ctx, `UPDATE outbox SET status='done', processed_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), entry.ID); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}

func EnqueueOutbox(ctx context.Context, database *sql.DB, requestID, eventID string, payload any) error {
	if database == nil {
		return fmt.Errorf("outbox database is nil")
	}
	if requestID == "" || eventID == "" {
		return fmt.Errorf("request id and event id are required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = database.ExecContext(ctx, `INSERT OR IGNORE INTO outbox (id, event_id, request_id, payload_json, status, retry_count, max_retries, next_retry_at, created_at) VALUES (?, ?, ?, ?, 'pending', 0, 10, ?, ?)`, idgen.NewID(), eventID, requestID, string(data), now, now)
	return err
}

// EnqueueBufferReplay enqueues a high-priority buffer_replay compensation job
// for a locally buffered event (Specify §1.5.1). It is idempotent per buffer
// record id, so retrying a degraded request never stacks duplicate jobs.
func EnqueueBufferReplay(ctx context.Context, database *sql.DB, bufferID, requestID string) error {
	if database == nil {
		return fmt.Errorf("buffer replay database is nil")
	}
	payload, err := json.Marshal(map[string]string{"buffer_id": bufferID, "request_id": requestID})
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// No next_retry_at reference: the column is added lazily by EnsureQueueSchema
	// when the worker queue is wired, and the watchdog inserts jobs without it.
	_, err = database.ExecContext(ctx, `
		INSERT INTO jobs (id, queue, priority, payload_json, status, retry_count, max_retries, partition_key, created_at)
		SELECT ?, 'buffer_replay', 90, ?, 'pending', 0, 5, ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM jobs
			WHERE queue = 'buffer_replay'
			  AND json_extract(payload_json, '$.buffer_id') = ?
			  AND status IN ('pending', 'processing')
		)
	`, idgen.NewID(), string(payload), "buffer:"+bufferID, now, bufferID)
	if err != nil {
		return fmt.Errorf("enqueue buffer replay: %w", err)
	}
	return nil
}

// ReplayBufferEvent replays one locally buffered event back into SQLite. It
// is idempotent: every write is skipped when the target row already exists,
// so a buffered request that was retried never duplicates L0 records. It is
// the handler for buffer_replay jobs and can also be wired as the
// BufferHandler of db.Recover for the startup replay pass (§1.8.3).
func ReplayBufferEvent(ctx context.Context, database *sql.DB, memoryRoot string, record db.BufferRecord) error {
	if database == nil {
		return fmt.Errorf("buffer replay database is nil")
	}
	if memoryRoot == "" {
		return fmt.Errorf("buffer replay memory root is empty")
	}
	payload, err := readBufferPayload(memoryRoot, record)
	if err != nil {
		return err
	}
	if err := ensureTurnLedger(ctx, database, payload); err != nil {
		return err
	}
	if payload.EventType == "inbound_persisted" {
		return replayInboundEvent(ctx, database, memoryRoot, payload)
	}
	return replayTerminalEvent(ctx, database, payload)
}

func readBufferPayload(memoryRoot string, record db.BufferRecord) (db.BufferEventPayload, error) {
	var payload db.BufferEventPayload
	if record.FilePath == "" {
		return payload, fmt.Errorf("buffered record has no file path")
	}
	fullPath := filepath.Join(memoryRoot, filepath.FromSlash(record.FilePath))
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return payload, fmt.Errorf("read buffered event: %w", err)
	}
	if record.SHA256 != "" && "sha256:"+hashutil.SHA256Bytes(data) != record.SHA256 {
		return payload, fmt.Errorf("buffered event hash mismatch")
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, fmt.Errorf("parse buffered event: %w", err)
	}
	return payload, nil
}

func ensureTurnLedger(ctx context.Context, database *sql.DB, payload db.BufferEventPayload) error {
	var exists int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM turn_ledger WHERE turn_id = ?`, payload.TurnID).Scan(&exists); err != nil {
		return fmt.Errorf("check turn ledger: %w", err)
	}
	if exists > 0 {
		return nil
	}
	if err := turn.WriteTurnLedger(database, payload.TurnID, payload.RequestID, payload.ConversationID, payload.SessionID, payload.TeamID, 1); err != nil {
		return fmt.Errorf("recreate turn ledger: %w", err)
	}
	return nil
}

func replayInboundEvent(ctx context.Context, database *sql.DB, memoryRoot string, payload db.BufferEventPayload) error {
	var exists int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM turn_events WHERE id = ?`, payload.EventID).Scan(&exists); err != nil {
		return fmt.Errorf("check inbound event: %w", err)
	}
	if exists > 0 {
		return nil
	}
	if len(payload.RequestBody) == 0 {
		return fmt.Errorf("buffered inbound event has no request body")
	}
	l0Path, contentHash, err := l0.WriteRequestFile(memoryRoot, payload.TurnID, payload.EventID, json.RawMessage(payload.RequestBody))
	if err != nil {
		return fmt.Errorf("rewrite L0 request: %w", err)
	}
	if _, err := turn.WriteInboundEventWithID(database, payload.EventID, payload.TurnID, payload.RequestID, payload.ConversationID, payload.SessionID, payload.TeamID, contentHash, l0Path); err != nil {
		return fmt.Errorf("replay inbound event: %w", err)
	}
	if info, err := os.Stat(filepath.Join(memoryRoot, l0Path)); err == nil {
		_ = l0.RegisterEventFile(database, payload.EventID, l0Path, info.Size(), contentHash)
	}
	replayInjectionSnapshot(ctx, database, payload)
	return nil
}

func replayInjectionSnapshot(ctx context.Context, database *sql.DB, payload db.BufferEventPayload) {
	var exists int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM injection_snapshots WHERE request_id = ?`, payload.RequestID).Scan(&exists); err != nil || exists > 0 {
		return
	}
	sources, _ := json.Marshal(payload.InjectionSources)
	manifest := payload.InjectionManifestVersion
	if manifest == "" {
		manifest = "phase3-v1"
	}
	usedTokens := (len([]rune(payload.InjectionText)) + 3) / 4
	_, _ = database.ExecContext(ctx, `
		INSERT INTO injection_snapshots (id, request_id, turn_id, manifest_version, source_ids_json, token_budget, used_tokens, truncated)
		VALUES (?, ?, ?, ?, ?, 0, ?, 0)
	`, idgen.NewID(), payload.RequestID, payload.TurnID, manifest, string(sources), usedTokens)
}

func replayTerminalEvent(ctx context.Context, database *sql.DB, payload db.BufferEventPayload) error {
	var finalStatus sql.NullString
	if err := database.QueryRowContext(ctx, `SELECT final_status FROM turn_ledger WHERE turn_id = ?`, payload.TurnID).Scan(&finalStatus); err != nil {
		return fmt.Errorf("check final status: %w", err)
	}
	if finalStatus.Valid && finalStatus.String != "" {
		return nil
	}
	if payload.ContentHash != "" && payload.ContentPath != "" {
		_ = turn.SetTurnResponse(database, payload.TurnID, payload.ContentHash, payload.ContentPath)
	}
	status := turn.TerminalStatus(payload.EventType)
	if _, err := turn.WriteTerminalStatusWithID(database, payload.EventID, payload.TurnID, payload.RequestID, payload.ConversationID, payload.SessionID, payload.TeamID, status, payload.ErrorMsg); err != nil {
		return fmt.Errorf("replay terminal event: %w", err)
	}
	if payload.ContentHash != "" && payload.ContentPath != "" {
		_ = turn.SetEventContent(database, payload.EventID, payload.ContentHash, payload.ContentPath)
	}
	return nil
}

// ReplayPendingBuffers drains every pending local_buffer row back into SQLite
// and returns how many rows were replayed. It is the periodic counterpart of
// the startup recovery pass: the gateway calls it on its maintenance loop so a
// degraded write heals even when no dedicated worker process is running.
func ReplayPendingBuffers(ctx context.Context, database *sql.DB, memoryRoot string) (int, error) {
	if database == nil {
		return 0, fmt.Errorf("buffer replay database is nil")
	}
	if memoryRoot == "" {
		return 0, fmt.Errorf("buffer replay memory root is empty")
	}
	return db.ReplayBuffers(ctx, database, memoryRoot, func(ctx context.Context, record db.BufferRecord) error {
		return ReplayBufferEvent(ctx, database, memoryRoot, record)
	})
}

// ReplayBufferByID replays one buffered event identified by its local_buffer
// id and moves the row to 'replayed' on success (or 'failed' with an
// incremented retry count on error). It is the body of the buffer_replay
// compensation job. A row that vanished is a no-op: some other pass already
// replayed it.
func ReplayBufferByID(ctx context.Context, database *sql.DB, memoryRoot, bufferID string) error {
	if database == nil {
		return fmt.Errorf("buffer replay database is nil")
	}
	if bufferID == "" {
		return fmt.Errorf("buffer id is required")
	}
	var record db.BufferRecord
	err := database.QueryRowContext(ctx, `SELECT id, COALESCE(request_id,''), payload_json, file_path, sha256 FROM local_buffer WHERE id = ?`, bufferID).
		Scan(&record.ID, &record.RequestID, &record.PayloadJSON, &record.FilePath, &record.SHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load buffered record %s: %w", bufferID, err)
	}
	if err := ReplayBufferEvent(ctx, database, memoryRoot, record); err != nil {
		_, _ = database.ExecContext(ctx, `UPDATE local_buffer SET status='failed', retry_count=retry_count+1 WHERE id=?`, bufferID)
		return err
	}
	_, _ = database.ExecContext(ctx, `UPDATE local_buffer SET status='replayed', replayed_at=? WHERE id=? AND status='pending'`, time.Now().UTC().Format(time.RFC3339Nano), bufferID)
	return nil
}
