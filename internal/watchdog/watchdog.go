package watchdog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"gateway/internal/idgen"
)

type Report struct {
	MissingResponses int
	ReclaimedLeases  int
	BindingRepairs   int
	SequenceGaps     int
	BufferedEvents   int
}

type Watchdog struct {
	database     *sql.DB
	missingAfter time.Duration
	now          func() time.Time
}

func New(database *sql.DB, missingAfter time.Duration) *Watchdog {
	if missingAfter <= 0 {
		missingAfter = 30 * time.Second
	}
	return &Watchdog{database: database, missingAfter: missingAfter, now: time.Now}
}

func (w *Watchdog) Scan(ctx context.Context) (Report, error) {
	var report Report
	if w == nil || w.database == nil {
		return report, fmt.Errorf("watchdog database is nil")
	}
	now := w.now().UTC()
	result, err := w.database.ExecContext(ctx, `UPDATE jobs SET status='pending', lease_token=NULL, lease_until=NULL, worker_id=NULL, heartbeat_at=NULL, started_at=NULL WHERE status='processing' AND lease_until IS NOT NULL AND lease_until < ?`, now.Format(time.RFC3339Nano))
	if err != nil {
		return report, err
	}
	if count, _ := result.RowsAffected(); count > 0 {
		report.ReclaimedLeases = int(count)
	}
	cutoff := now.Add(-w.missingAfter).Format(time.RFC3339Nano)
	rows, err := w.database.QueryContext(ctx, `SELECT turn_id, request_id, conversation_id, COALESCE(team_id,''), COALESCE(session_id,'') FROM turn_ledger WHERE final_event_id IS NULL AND created_at < ? AND recording_state IN ('open','compensating')`, cutoff)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var turnID, requestID, conversationID, teamID, sessionID string
		if err := rows.Scan(&turnID, &requestID, &conversationID, &teamID, &sessionID); err != nil {
			rows.Close()
			return report, err
		}
		payload, _ := json.Marshal(map[string]string{"turn_id": turnID, "request_id": requestID, "conversation_id": conversationID, "session_id": sessionID})
		result, err := w.database.ExecContext(ctx, `INSERT INTO jobs (id, queue, priority, team_id, payload_json, status, partition_key, max_retries, created_at) SELECT ?, 'missing_response', 90, ?, ?, 'pending', ?, 5, ? WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE queue='missing_response' AND json_extract(payload_json,'$.turn_id') = ? AND status IN ('pending','processing'))`, idgen.NewID(), nullable(teamID), string(payload), "request:"+requestID, now.Format(time.RFC3339Nano), turnID)
		if err != nil {
			rows.Close()
			return report, err
		}
		if count, _ := result.RowsAffected(); count > 0 {
			report.MissingResponses++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return report, err
	}
	if gaps, err := scanSequenceGaps(ctx, w.database); err != nil {
		return report, err
	} else {
		report.SequenceGaps = gaps
	}
	if repairs, err := enqueueBindingRepairs(ctx, w.database, now); err != nil {
		return report, err
	} else {
		report.BindingRepairs = repairs
	}
	var buffered int
	if err := w.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM local_buffer WHERE status='pending'`).Scan(&buffered); err == nil {
		report.BufferedEvents = buffered
	}
	return report, nil
}

func scanSequenceGaps(ctx context.Context, database *sql.DB) (int, error) {
	rows, err := database.QueryContext(ctx, `SELECT conversation_id, turn_seq, turn_id FROM turn_ledger ORDER BY conversation_id, turn_seq, turn_id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	last := make(map[string]int)
	gaps := 0
	for rows.Next() {
		var conversation, turnID string
		var seq int
		if err := rows.Scan(&conversation, &seq, &turnID); err != nil {
			return gaps, err
		}
		previous, ok := last[conversation]
		if (!ok && seq != 1) || (ok && seq != previous+1) {
			gaps++
			if _, err := database.ExecContext(ctx, `UPDATE turn_ledger SET gap_detected=1 WHERE turn_id=?`, turnID); err != nil {
				return gaps, err
			}
		}
		last[conversation] = seq
	}
	return gaps, rows.Err()
}

func enqueueBindingRepairs(ctx context.Context, database *sql.DB, now time.Time) (int, error) {
	rows, err := database.QueryContext(ctx, `SELECT id, conversation_id, session_id, COALESCE(team_id,''), COALESCE(agent_id,'') FROM session_bindings WHERE binding_state='binding_missing'`)
	if err != nil {
		return 0, err
	}
	type missingBinding struct {
		id, conversation, session, team, agent string
	}
	var bindings []missingBinding
	for rows.Next() {
		var item missingBinding
		if err := rows.Scan(&item.id, &item.conversation, &item.session, &item.team, &item.agent); err != nil {
			rows.Close()
			return 0, err
		}
		bindings = append(bindings, item)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	repairs := 0
	for _, item := range bindings {
		payload, _ := json.Marshal(map[string]string{"binding_id": item.id, "conversation_id": item.conversation, "session_id": item.session})
		result, err := database.ExecContext(ctx, `INSERT INTO jobs (id, queue, priority, team_id, agent_id, payload_json, partition_key, max_retries, status, created_at)
			SELECT ?, 'binding_fix', 90, ?, ?, ?, ?, 5, 'pending', ?
			WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE queue='binding_fix' AND json_extract(payload_json,'$.binding_id')=? AND status IN ('pending','processing'))`, idgen.NewID(), nullable(item.team), nullable(item.agent), string(payload), "binding:"+item.conversation, now.Format(time.RFC3339Nano), item.id)
		if err != nil {
			return repairs, err
		}
		count, _ := result.RowsAffected()
		if count > 0 {
			repairs++
		}
	}
	return repairs, nil
}

func (w *Watchdog) Run(ctx context.Context, interval time.Duration) error {
	if w == nil || w.database == nil {
		return fmt.Errorf("watchdog database is nil")
	}
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := w.Scan(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
