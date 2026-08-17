package turn

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"gateway/internal/idgen"
)

func withImmediateTx(db *sql.DB, fn func(context.Context, *sql.Conn) error) (err error) {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire database connection: %w", err)
	}
	defer conn.Close()

	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			if _, rollbackErr := conn.ExecContext(ctx, "ROLLBACK"); rollbackErr != nil && err == nil {
				err = fmt.Errorf("rollback transaction: %w", rollbackErr)
			}
		}
	}()

	if err = fn(ctx, conn); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// WriteTurnLedger creates a turn ledger entry with sequential turn_seq
// Uses BEGIN IMMEDIATE for write locking
func WriteTurnLedger(db *sql.DB, turnID, requestID, conversationID, sessionID, teamID string, bindingVersion int) error {
	return withImmediateTx(db, func(ctx context.Context, conn *sql.Conn) error {
		var turnSeq int
		if err := conn.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(turn_seq), 0) + 1
			FROM turn_ledger
			WHERE conversation_id = ?
		`, conversationID).Scan(&turnSeq); err != nil {
			return fmt.Errorf("get next turn_seq: %w", err)
		}

		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO turn_ledger (
				turn_id, request_id, conversation_id, session_id, team_id,
				turn_seq, binding_version, recording_state, gap_detected, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, turnID, requestID, conversationID, nullableString(sessionID), nullableString(teamID),
			turnSeq, bindingVersion, "open", 0, now); err != nil {
			return fmt.Errorf("insert turn_ledger: %w", err)
		}

		return nil
	})
}

// WriteInboundEvent writes an inbound_persisted event
func WriteInboundEvent(db *sql.DB, turnID, requestID, conversationID, sessionID, teamID, contentHash, contentPath string) error {
	_, err := WriteInboundEventWithID(db, idgen.NewEventID(), turnID, requestID, conversationID, sessionID, teamID, contentHash, contentPath)
	return err
}

// WriteInboundEventWithID records an inbound event using an event ID selected
// before its L0 file is written, so event_files can reference the same ID.
func WriteInboundEventWithID(db *sql.DB, eventID, turnID, requestID, conversationID, sessionID, teamID, contentHash, contentPath string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := withImmediateTx(db, func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO turn_events (
				id, request_id, turn_id, conversation_id, session_id, team_id,
				direction, sequence, event_type, status, content_hash, content_path, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, eventID, requestID, turnID, conversationID, nullableString(sessionID), nullableString(teamID),
			"inbound", 1, "inbound_persisted", "ok", contentHash, contentPath, now); err != nil {
			return fmt.Errorf("insert inbound_persisted event: %w", err)
		}

		result, err := conn.ExecContext(ctx, `
			UPDATE turn_ledger
			SET inbound_event_id = ?, content_hash = ?, l0_request_path = ?
			WHERE turn_id = ?
		`, eventID, contentHash, contentPath, turnID)
		if err != nil {
			return fmt.Errorf("update turn ledger: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("check turn ledger update: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("turn ledger not found for inbound event")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return eventID, nil
}

// WriteResponseStartedEvent writes a response_started event
func WriteResponseStartedEvent(db *sql.DB, turnID, requestID, conversationID, sessionID, teamID string) error {
	eventID := idgen.NewEventID()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err := db.Exec(`
		INSERT INTO turn_events (
			id, request_id, turn_id, conversation_id, session_id, team_id,
			direction, sequence, event_type, status, content_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, eventID, requestID, turnID, conversationID, nullableString(sessionID), nullableString(teamID),
		"outbound", 1, "response_started", "ok", "", now)

	return err
}

// WriteDeltaCheckpoint writes a delta_checkpoint event
func WriteDeltaCheckpoint(db *sql.DB, turnID, requestID, conversationID, sessionID, teamID, contentHash string, sequence int) error {
	eventID := idgen.NewEventID()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err := db.Exec(`
		INSERT INTO turn_events (
			id, request_id, turn_id, conversation_id, session_id, team_id,
			direction, sequence, event_type, status, content_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, eventID, requestID, turnID, conversationID, nullableString(sessionID), nullableString(teamID),
		"outbound", sequence, "delta_checkpoint", "ok", contentHash, now)

	return err
}
