package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"gateway/internal/idgen"
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
