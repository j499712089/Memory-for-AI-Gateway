package worker

import (
	"context"
	"fmt"
	"time"

	"gateway/internal/idgen"
)

func RetryDelay(retryCount int) time.Duration {
	if retryCount < 1 {
		retryCount = 1
	}
	const maxDelay = 30 * time.Second
	delay := time.Second
	for attempt := 1; attempt < retryCount && delay < maxDelay; attempt++ {
		delay *= 2
		if delay >= maxDelay {
			return maxDelay
		}
	}
	return delay
}

func (q *Queue) Fail(ctx context.Context, claim *Claim, message string) error {
	if q == nil || q.database == nil {
		return fmt.Errorf("worker queue is nil")
	}
	if claim == nil {
		return fmt.Errorf("claim is nil")
	}
	conn, err := q.database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire failed job connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin failed job transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	var retryCount, maxRetries int
	var queue, payload string
	err = conn.QueryRowContext(ctx, `SELECT queue, payload_json, retry_count, max_retries FROM jobs WHERE id=? AND status='processing' AND lease_token=?`, claim.ID, claim.LeaseToken).Scan(&queue, &payload, &retryCount, &maxRetries)
	if err != nil {
		return fmt.Errorf("load failed job: %w", err)
	}
	retryCount++
	now := q.now().UTC()
	if retryCount > maxRetries {
		result, err := conn.ExecContext(ctx, `UPDATE jobs SET status='dead_letter', retry_count=?, dead_letter_reason=?, finished_at=?, lease_token=NULL, lease_until=NULL, worker_id=NULL, heartbeat_at=NULL WHERE id=? AND status='processing' AND lease_token=?`, retryCount, message, now.Format(time.RFC3339Nano), claim.ID, claim.LeaseToken)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return fmt.Errorf("job failure rejected")
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO dead_letters (id, job_id, queue, reason, error_trace, payload_json, retries, enqueued_at, dead_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, idgen.NewID(), claim.ID, queue, message, message, payload, retryCount, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	} else {
		next := now.Add(RetryDelay(retryCount))
		result, err := conn.ExecContext(ctx, `UPDATE jobs SET status='pending', retry_count=?, next_retry_at=?, dead_letter_reason=?, lease_token=NULL, lease_until=NULL, worker_id=NULL, heartbeat_at=NULL WHERE id=? AND status='processing' AND lease_token=?`, retryCount, next.Format(time.RFC3339Nano), message, claim.ID, claim.LeaseToken)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return fmt.Errorf("job retry rejected")
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}
