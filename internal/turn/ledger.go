package turn

import (
	"database/sql"
	"fmt"
	"time"

	"gateway/internal/idgen"
)

// WriteTurnLedger creates a turn ledger entry with sequential turn_seq
// Uses BEGIN IMMEDIATE for write locking
func WriteTurnLedger(db *sql.DB, turnID, requestID, conversationID, sessionID, teamID string, bindingVersion int) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Execute BEGIN IMMEDIATE
	if _, err := tx.Exec("BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate: %w", err)
	}

	// Get next turn_seq for this conversation
	var turnSeq int
	err = tx.QueryRow(`
		SELECT COALESCE(MAX(turn_seq), 0) + 1
		FROM turn_ledger
		WHERE conversation_id = ?
	`, conversationID).Scan(&turnSeq)
	if err != nil {
		return fmt.Errorf("get next turn_seq: %w", err)
	}

	// Insert turn ledger entry
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.Exec(`
		INSERT INTO turn_ledger (
			turn_id, request_id, conversation_id, session_id, team_id,
			turn_seq, binding_version, recording_state, gap_detected, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, turnID, requestID, conversationID, sessionID, teamID,
		turnSeq, bindingVersion, "open", 0, now)
	if err != nil {
		return fmt.Errorf("insert turn_ledger: %w", err)
	}

	return tx.Commit()
}

// WriteInboundEvent writes an inbound_persisted event
func WriteInboundEvent(db *sql.DB, turnID, requestID, conversationID, sessionID, teamID, contentHash, contentPath string) error {
	eventID := idgen.NewEventID()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err := db.Exec(`
		INSERT INTO turn_events (
			id, request_id, turn_id, conversation_id, session_id, team_id,
			direction, sequence, event_type, status, content_hash, content_path, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, eventID, requestID, turnID, conversationID, sessionID, teamID,
		"inbound", 1, "inbound_persisted", "ok", contentHash, contentPath, now)

	if err != nil {
		return fmt.Errorf("insert inbound_persisted event: %w", err)
	}

	// Update turn_ledger with inbound_event_id
	_, err = db.Exec(`
		UPDATE turn_ledger
		SET inbound_event_id = ?
		WHERE turn_id = ?
	`, eventID, turnID)

	return err
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
	`, eventID, requestID, turnID, conversationID, sessionID, teamID,
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
	`, eventID, requestID, turnID, conversationID, sessionID, teamID,
		"outbound", sequence, "delta_checkpoint", "ok", contentHash, now)

	return err
}
