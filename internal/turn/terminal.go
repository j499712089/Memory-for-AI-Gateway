package turn

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"gateway/internal/idgen"
)

// TerminalStatus represents the final status of a turn
type TerminalStatus string

const (
	StatusComplete  TerminalStatus = "complete"
	StatusPartial   TerminalStatus = "partial"
	StatusError     TerminalStatus = "error"
	StatusCancelled TerminalStatus = "cancelled"
)

// WriteTerminalStatus writes exactly one terminal status event and updates turn_ledger
// Ensures no duplicate terminal statuses per turn
func WriteTerminalStatus(db *sql.DB, turnID, requestID, conversationID, sessionID, teamID string, status TerminalStatus, errorMsg string) error {
	_, err := WriteTerminalStatusWithID(db, idgen.NewEventID(), turnID, requestID, conversationID, sessionID, teamID, status, errorMsg)
	return err
}

// WriteTerminalStatusWithID records the only terminal event for a turn and
// returns its ID for outbox and L0 file bookkeeping.
//
// The write is idempotent ("幂等续写"): the first real terminal wins. A
// placeholder 'cancelled' written by the watchdog missing_response pass (or by
// the active-cancel path before any bytes flowed) is superseded in place when
// the real terminal (complete/partial/error) later arrives, so a slow stream
// that was prematurely flagged as cancelled still records its true outcome.
// Any other duplicate write is a no-op that returns the existing terminal id.
func WriteTerminalStatusWithID(db *sql.DB, eventID, turnID, requestID, conversationID, sessionID, teamID string, status TerminalStatus, errorMsg string) (string, error) {
	if status != StatusComplete && status != StatusPartial && status != StatusError && status != StatusCancelled {
		return "", fmt.Errorf("unsupported terminal status: %s", status)
	}

	metadata := map[string]string{}
	if errorMsg != "" {
		metadata["error"] = errorMsg
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("marshal terminal metadata: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	terminalEventID := eventID
	err = withImmediateTx(db, func(ctx context.Context, conn *sql.Conn) error {
		var existingEventID, existingStatus string
		err := conn.QueryRowContext(ctx, `SELECT COALESCE(final_event_id,''), COALESCE(final_status,'') FROM turn_ledger WHERE turn_id = ?`, turnID).Scan(&existingEventID, &existingStatus)
		if err == sql.ErrNoRows {
			return fmt.Errorf("turn ledger not found")
		}
		if err != nil {
			return fmt.Errorf("check existing terminal status: %w", err)
		}

		if existingStatus != "" {
			if existingStatus == string(StatusCancelled) && status != StatusCancelled {
				// The watchdog/active-cancel placeholder must yield to the real
				// terminal. Update the existing event row in place so the turn
				// keeps exactly one terminal event (V5.7), and flip the ledger
				// to the real status.
				eventState := "ok"
				if status == StatusError {
					eventState = "error"
				}
				if _, err := conn.ExecContext(ctx, `
					UPDATE turn_events
					SET event_type = ?, status = ?, metadata_json = ?
					WHERE id = ?
				`, string(status), eventState, string(metadataJSON), existingEventID); err != nil {
					return fmt.Errorf("supersede cancelled terminal: %w", err)
				}
				result, err := conn.ExecContext(ctx, `
					UPDATE turn_ledger
					SET final_status = ?, completed_at = ?
					WHERE turn_id = ?
				`, string(status), now, turnID)
				if err != nil {
					return fmt.Errorf("update turn ledger on supersede: %w", err)
				}
				if rows, err := result.RowsAffected(); err != nil {
					return fmt.Errorf("check supersede update: %w", err)
				} else if rows != 1 {
					return fmt.Errorf("turn ledger lost on supersede")
				}
			}
			// Idempotent no-op: the turn already has a final terminal. Return
			// the existing terminal event id so downstream bookkeeping attaches
			// to the event that is actually recorded.
			terminalEventID = existingEventID
			return nil
		}

		var sequence int
		if err := conn.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(sequence), 0) + 1
			FROM turn_events
			WHERE turn_id = ? AND direction = 'outbound'
		`, turnID).Scan(&sequence); err != nil {
			return fmt.Errorf("get next sequence: %w", err)
		}

		eventState := "ok"
		if status == StatusError {
			eventState = "error"
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO turn_events (
				id, request_id, turn_id, conversation_id, session_id, team_id,
				direction, sequence, event_type, status, content_hash, metadata_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, eventID, requestID, turnID, conversationID, nullableString(sessionID), nullableString(teamID),
			"outbound", sequence, string(status), eventState, "", string(metadataJSON), now); err != nil {
			return fmt.Errorf("insert terminal event: %w", err)
		}

		result, err := conn.ExecContext(ctx, `
			UPDATE turn_ledger
			SET final_event_id = ?,
			    final_status = ?,
			    recording_state = 'complete',
			    completed_at = ?
			WHERE turn_id = ? AND (final_status IS NULL OR final_status = '')
		`, eventID, string(status), now, turnID)
		if err != nil {
			return fmt.Errorf("update turn ledger: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("check terminal update: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("turn already has a terminal status")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return terminalEventID, nil
}

// SetTurnResponse records the response L0 reference for replay and idempotency.
func SetTurnResponse(db *sql.DB, turnID, contentHash, contentPath string) error {
	result, err := db.Exec(`
		UPDATE turn_ledger
		SET l0_reply_path = ?, content_hash = COALESCE(content_hash, ?)
		WHERE turn_id = ?
	`, contentPath, contentHash, turnID)
	if err != nil {
		return fmt.Errorf("set turn response: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check response ledger update: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("turn ledger not found for response")
	}
	return nil
}

// SetEventContent attaches an L0 response reference to its terminal event.
func SetEventContent(db *sql.DB, eventID, contentHash, contentPath string) error {
	result, err := db.Exec(`
		UPDATE turn_events
		SET content_hash = ?, content_path = ?
		WHERE id = ?
	`, contentHash, contentPath, eventID)
	if err != nil {
		return fmt.Errorf("set event content: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check event content update: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("terminal event not found")
	}
	return nil
}

// EnqueueOutbox records a retry task after an upstream failure.
func EnqueueOutbox(db *sql.DB, requestID, eventID string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`
		INSERT OR IGNORE INTO outbox (id, event_id, request_id, payload_json, status, retry_count, max_retries, next_retry_at, created_at)
		VALUES (?, ?, ?, ?, 'pending', 0, 10, ?, ?)
	`, idgen.NewID(), eventID, requestID, string(body), now, now)
	if err != nil {
		return fmt.Errorf("enqueue outbox: %w", err)
	}
	return nil
}

// EnsureTerminalStatus ensures a turn has exactly one terminal status
// Used for cleanup/recovery scenarios
func EnsureTerminalStatus(db *sql.DB, turnID string) error {
	var finalStatus sql.NullString
	err := db.QueryRow(`
		SELECT final_status FROM turn_ledger WHERE turn_id = ?
	`, turnID).Scan(&finalStatus)
	if err != nil {
		return fmt.Errorf("check turn status: %w", err)
	}

	if !finalStatus.Valid || finalStatus.String == "" {
		return fmt.Errorf("turn %s has no terminal status", turnID)
	}

	return nil
}
