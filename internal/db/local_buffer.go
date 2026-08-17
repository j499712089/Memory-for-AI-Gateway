package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gateway/internal/hashutil"
)

// BufferEventPayload is the durable serialized form of one gateway event that
// could not be written to SQLite. It is persisted as a file under
// `90_运行数据/本地持久化缓冲` plus (best-effort) a row in the local_buffer
// ledger, and replayed back into SQLite by the buffer_replay worker or the
// startup recovery pass (Specify §1.5 / §1.8).
type BufferEventPayload struct {
	// BufferKey is the stable identity used for deduplication: the client
	// idempotency key when one is present, otherwise the gateway request id.
	BufferKey string `json:"buffer_key"`

	EventID        string `json:"event_id"`
	TurnID         string `json:"turn_id"`
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	SessionID      string `json:"session_id,omitempty"`
	TeamID         string `json:"team_id,omitempty"`
	Direction      string `json:"direction"`
	Sequence       int    `json:"sequence"`
	EventType      string `json:"event_type"`
	Status         string `json:"status"`
	ContentHash    string `json:"content_hash,omitempty"`
	ContentPath    string `json:"content_path,omitempty"`
	ErrorMsg       string `json:"error_msg,omitempty"`

	// Inbound replay material.
	RequestBody []byte `json:"request_body,omitempty"`

	// Injection snapshot replay material.
	InjectionManifestVersion string   `json:"injection_manifest_version,omitempty"`
	InjectionText            string   `json:"injection_text,omitempty"`
	InjectionSources         []string `json:"injection_sources,omitempty"`

	CreatedAt string `json:"created_at,omitempty"`
}

// LocalBufferDir returns the durable local buffer directory under the memory
// root (Constitution §11: `90_运行数据/本地持久化缓冲`).
func LocalBufferDir(memoryRoot string) string {
	return filepath.Join(memoryRoot, "90_运行数据", "本地持久化缓冲")
}

// sanitizeBufferComponent strips path separators and other characters that
// could escape the buffer directory. BufferKey is derived from a client
// idempotency header, so it must never influence the filesystem path.
func sanitizeBufferComponent(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	out := builder.String()
	if out == "" || out == "." || out == ".." {
		out = "key"
	}
	if len(out) > 96 {
		out = out[:96]
	}
	return out
}

// BufferRecordID derives the deterministic local_buffer ledger id for a
// payload. It is stable across retries of the same logical event so the
// degraded path never accumulates duplicate ledger rows (Specify §1.7).
func BufferRecordID(payload BufferEventPayload) string {
	key := sanitizeBufferComponent(payload.BufferKey)
	return "buffer:" + key + ":" + payload.Direction + ":" + payload.EventType + ":" + strconv.Itoa(payload.Sequence)
}

// BufferFileName derives the deterministic buffer file name for a payload.
func BufferFileName(payload BufferEventPayload) string {
	key := sanitizeBufferComponent(payload.BufferKey)
	return fmt.Sprintf("%s-%s-%s-%d.jsonl", key, payload.Direction, payload.EventType, payload.Sequence)
}

// WriteLocalBuffer durably buffers one event that could not be written to
// SQLite. It atomically writes a file under the local buffer directory and,
// best-effort, inserts a ledger row into the local_buffer table. The file is
// the source of truth; the ledger row is the index used by startup recovery
// and the buffer_replay worker.
//
// The operation is idempotent per BufferKey: a retry of the same logical
// event overwrites the same file path and INSERT OR IGNORE keeps a single
// ledger row, so replayed requests never duplicate L0 records.
func WriteLocalBuffer(ctx context.Context, database *sql.DB, memoryRoot string, payload BufferEventPayload) (BufferRecord, error) {
	if memoryRoot == "" {
		return BufferRecord{}, fmt.Errorf("local buffer memory root is empty")
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
		return BufferRecord{}, fmt.Errorf("local buffer event type is required")
	}
	if payload.CreatedAt == "" {
		payload.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return BufferRecord{}, fmt.Errorf("marshal buffer payload: %w", err)
	}

	dir := LocalBufferDir(memoryRoot)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return BufferRecord{}, fmt.Errorf("create local buffer dir: %w", err)
	}
	fileName := BufferFileName(payload)
	finalPath := filepath.Join(dir, fileName)
	tmpPath := finalPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return BufferRecord{}, fmt.Errorf("write buffer temp file: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return BufferRecord{}, fmt.Errorf("buffer atomic rename: %w", err)
	}

	sha := "sha256:" + hashutil.SHA256Bytes(data)
	relative, err := filepath.Rel(memoryRoot, finalPath)
	if err != nil {
		return BufferRecord{}, fmt.Errorf("buffer relative path: %w", err)
	}
	record := BufferRecord{
		ID:          BufferRecordID(payload),
		RequestID:   payload.RequestID,
		PayloadJSON: string(data),
		FilePath:    filepath.ToSlash(relative),
		SHA256:      sha,
	}

	if database != nil {
		// Best-effort ledger row. The file is the source of truth; the row is
		// the index used by startup recovery and the buffer_replay worker. A
		// retry of the same logical event (same BufferKey) refreshes the row
		// to match the overwritten file while keeping its status, so recovery
		// never sees a hash mismatch and no duplicate rows accumulate.
		//
		// The insert runs on a non-cancellable context: this method is called
		// with an already-cancelled request context when a terminal event is
		// recorded after a client disconnect, and losing the ledger row would
		// orphan the file — the buffered event would never be replayed back
		// into SQLite, leaving a ghost turn with no terminal status.
		_, _ = database.ExecContext(context.WithoutCancel(ctx), `
			INSERT INTO local_buffer (
				id, event_id, request_id, turn_id, payload_json, status,
				file_path, sha256, created_at
			) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				event_id = excluded.event_id,
				request_id = excluded.request_id,
				turn_id = excluded.turn_id,
				payload_json = excluded.payload_json,
				file_path = excluded.file_path,
				sha256 = excluded.sha256,
				status = CASE WHEN local_buffer.status = 'pending'
					THEN 'pending' ELSE local_buffer.status END
		`, record.ID, payload.EventID, payload.RequestID, payload.TurnID, string(data), record.FilePath, sha, payload.CreatedAt)
	}
	return record, nil
}
