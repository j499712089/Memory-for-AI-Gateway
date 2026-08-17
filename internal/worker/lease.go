package worker

import (
	"context"
	"fmt"
	"time"
)

func (q *Queue) Heartbeat(ctx context.Context, claim *Claim) error {
	if q == nil || q.database == nil {
		return fmt.Errorf("worker queue is nil")
	}
	if claim == nil {
		return fmt.Errorf("claim is nil")
	}
	now := q.now().UTC()
	until := now.Add(q.leaseDuration)
	result, err := q.database.ExecContext(ctx, `UPDATE jobs SET heartbeat_at=?, lease_until=? WHERE id=? AND status='processing' AND lease_token=?`, now.Format(time.RFC3339Nano), until.Format(time.RFC3339Nano), claim.ID, claim.LeaseToken)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("lease heartbeat rejected")
	}
	claim.LeaseUntil = until
	return nil
}

func (q *Queue) ReclaimExpired(ctx context.Context) (int, error) {
	if q == nil || q.database == nil {
		return 0, fmt.Errorf("worker queue is nil")
	}
	now := q.now().UTC().Format(time.RFC3339Nano)
	result, err := q.database.ExecContext(ctx, `UPDATE jobs SET status='pending', lease_token=NULL, lease_until=NULL, worker_id=NULL, heartbeat_at=NULL, started_at=NULL WHERE status='processing' AND lease_until IS NOT NULL AND lease_until <= ?`, now)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}
