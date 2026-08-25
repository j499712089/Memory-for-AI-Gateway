package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gateway/internal/idgen"
)

type Job struct {
	ID           string
	Queue        string
	Priority     int
	TeamID       string
	AgentID      string
	AssetID      string
	AssetType    string
	Payload      any
	PartitionKey string
	MaxRetries   int
}

type Claim struct {
	ID           string
	Queue        string
	Priority     int
	TeamID       string
	AgentID      string
	AssetID      string
	AssetType    string
	PayloadJSON  string
	PartitionKey string
	LeaseToken   string
	LeaseUntil   time.Time
	WorkerID     string
	RetryCount   int
	MaxRetries   int
}

type Queue struct {
	database      *sql.DB
	leaseDuration time.Duration
	now           func() time.Time
	initErr       error
}

func NewQueue(database *sql.DB, leaseDuration time.Duration) *Queue {
	if leaseDuration <= 0 {
		leaseDuration = 30 * time.Second
	}
	queue := &Queue{database: database, leaseDuration: leaseDuration, now: time.Now}
	queue.initErr = EnsureQueueSchema(database)
	return queue
}

func EnsureQueueSchema(database *sql.DB) error {
	if database == nil {
		return fmt.Errorf("worker database is nil")
	}
	if _, err := database.Exec(`
		CREATE TABLE IF NOT EXISTS l1_refine_handoffs (
			team_id TEXT NOT NULL,
			turn_id TEXT NOT NULL,
			job_id TEXT UNIQUE,
			payload_json TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','enqueued')),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			enqueued_at TEXT,
			PRIMARY KEY (team_id, turn_id)
		)
	`); err != nil {
		return fmt.Errorf("ensure l1 refine handoff schema: %w", err)
	}
	var hasNextRetry bool
	rows, err := database.Query(`PRAGMA table_info(jobs)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "next_retry_at" {
			hasNextRetry = true
		}
	}
	rows.Close()
	if !hasNextRetry {
		if _, err := database.Exec(`ALTER TABLE jobs ADD COLUMN next_retry_at TEXT`); err != nil {
			return fmt.Errorf("add job retry schedule: %w", err)
		}
	}
	return nil
}

func DefaultPriority(queue string) int {
	switch queue {
	case "embedding":
		return 70
	case "compensation", "missing_response", "buffer_replay", "binding_fix":
		return 90
	case "l1_refine":
		return 80
	case "wiki_build":
		return 60
	case "codegraph_incremental":
		return 50
	case "l2_promote", "l3_promote", "l4_promote", "skill_review":
		return 40
	case "git_commit":
		return 20
	default:
		return 50
	}
}

func (q *Queue) Enqueue(ctx context.Context, job Job) (string, error) {
	if q == nil || q.database == nil {
		return "", fmt.Errorf("worker queue is nil")
	}
	if q.initErr != nil {
		return "", q.initErr
	}
	if strings.TrimSpace(job.Queue) == "" {
		return "", fmt.Errorf("job queue is required")
	}
	if job.ID == "" {
		job.ID = idgen.NewID()
	}
	if job.Priority == 0 {
		job.Priority = DefaultPriority(job.Queue)
	}
	if job.PartitionKey == "" {
		job.PartitionKey = job.TeamID + ":" + job.AgentID + ":" + job.AssetID
	}
	if job.PartitionKey == "" {
		job.PartitionKey = job.ID
	}
	if job.MaxRetries <= 0 {
		job.MaxRetries = 5
	}
	payload, err := json.Marshal(job.Payload)
	if err != nil {
		return "", fmt.Errorf("marshal job payload: %w", err)
	}
	now := q.now().UTC().Format(time.RFC3339Nano)
	_, err = q.database.ExecContext(ctx, `INSERT INTO jobs (id, queue, priority, team_id, agent_id, asset_id, asset_type, payload_json, status, retry_count, max_retries, partition_key, created_at, next_retry_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?, ?, ?)`, job.ID, job.Queue, job.Priority, nullable(job.TeamID), nullable(job.AgentID), nullable(job.AssetID), nullable(job.AssetType), string(payload), job.MaxRetries, job.PartitionKey, now, now)
	if err != nil {
		return "", fmt.Errorf("enqueue job: %w", err)
	}
	return job.ID, nil
}

// Claim atomically selects the highest-priority due job and leases it to
// workerID. When queues is non-empty, only jobs in those queues are eligible;
// an empty allow-list selects any job. The queue filter lets a worker process
// claim only the job types it has handlers for, so compensation / replay /
// missing-response jobs the worker cannot execute stay pending instead of
// being dead-lettered.
func (q *Queue) Claim(ctx context.Context, workerID string, queues ...string) (*Claim, error) {
	if q == nil || q.database == nil {
		return nil, fmt.Errorf("worker queue is nil")
	}
	if q.initErr != nil {
		return nil, q.initErr
	}
	if workerID == "" {
		return nil, fmt.Errorf("worker id is required")
	}
	conn, err := q.database.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	now := q.now().UTC()
	args := []any{now.Format(time.RFC3339Nano)}
	queueFilter := ""
	if len(queues) > 0 {
		placeholders := make([]string, len(queues))
		for index, name := range queues {
			placeholders[index] = "?"
			args = append(args, name)
		}
		queueFilter = " AND j.queue IN (" + strings.Join(placeholders, ",") + ")"
	}
	rows, err := conn.QueryContext(ctx, `SELECT j.id, j.queue, j.priority, COALESCE(j.team_id,''), COALESCE(j.agent_id,''), COALESCE(j.asset_id,''), COALESCE(j.asset_type,''), j.payload_json, j.partition_key, j.retry_count, j.max_retries FROM jobs j WHERE j.status = 'pending' AND (j.next_retry_at IS NULL OR j.next_retry_at <= ?) AND NOT EXISTS (SELECT 1 FROM jobs active WHERE active.status = 'processing' AND active.partition_key = j.partition_key)`+queueFilter+` ORDER BY j.priority DESC, j.created_at ASC, j.id ASC`, args...)
	if err != nil {
		return nil, err
	}
	var selected *Claim
	for rows.Next() {
		candidate := &Claim{}
		if err := rows.Scan(&candidate.ID, &candidate.Queue, &candidate.Priority, &candidate.TeamID, &candidate.AgentID, &candidate.AssetID, &candidate.AssetType, &candidate.PayloadJSON, &candidate.PartitionKey, &candidate.RetryCount, &candidate.MaxRetries); err != nil {
			rows.Close()
			return nil, err
		}
		selected = candidate
		break
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if selected == nil {
		if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
			return nil, err
		}
		committed = true
		return nil, nil
	}
	selected.WorkerID = workerID
	selected.LeaseToken = idgen.NewID()
	selected.LeaseUntil = now.Add(q.leaseDuration)
	result, err := conn.ExecContext(ctx, `UPDATE jobs SET status='processing', lease_token=?, lease_until=?, worker_id=?, heartbeat_at=?, started_at=COALESCE(started_at, ?) WHERE id=? AND status='pending' AND (next_retry_at IS NULL OR next_retry_at <= ?)`, selected.LeaseToken, selected.LeaseUntil.Format(time.RFC3339Nano), workerID, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), selected.ID, now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, fmt.Errorf("job claim lost race")
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, err
	}
	committed = true
	return selected, nil
}

func (q *Queue) Complete(ctx context.Context, claim *Claim) error {
	if q == nil || q.database == nil {
		return fmt.Errorf("worker queue is nil")
	}
	if claim == nil {
		return fmt.Errorf("claim is nil")
	}
	result, err := q.database.ExecContext(ctx, `UPDATE jobs SET status='done', finished_at=?, lease_token=NULL, lease_until=NULL, worker_id=NULL WHERE id=? AND status='processing' AND lease_token=?`, q.now().UTC().Format(time.RFC3339Nano), claim.ID, claim.LeaseToken)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("job completion rejected")
	}
	return nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
