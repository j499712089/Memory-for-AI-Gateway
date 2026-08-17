package turn

import (
	"database/sql"
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
	// Check if terminal status already exists
	var existingStatus string
	err := db.QueryRow(`
		SELECT final_status FROM turn_ledger WHERE turn_id = ?
	`, turnID).Scan(&existingStatus)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("check existing terminal status: %w", err)
	}
	if existingStatus != "" {
		return fmt.Errorf("turn already has terminal status: %s", existingStatus)
	}

	// Get next sequence number for outbound events
	var sequence int
	err = db.QueryRow(`
		SELECT COALESCE(MAX(sequence), 0) + 1
		FROM turn_events
		WHERE turn_id = ? AND direction = 'outbound'
	`, turnID).Scan(&sequence)
	if err != nil {
		return fmt.Errorf("get next sequence: %w", err)
	}

	// Write terminal event
	eventID := idgen.NewEventID()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	metadataJSON := "{}"
	if errorMsg != "" {
		metadataJSON = fmt.Sprintf(`{"error":"%s"}`, errorMsg)
	}

	_, err = db.Exec(`
		INSERT INTO turn_events (
			id, request_id, turn_id, conversation_id, session_id, team_id,
			direction, sequence, event_type, status, content_hash, metadata_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, eventID, requestID, turnID, conversationID, sessionID, teamID,
		"outbound", sequence, string(status), "ok", "", metadataJSON, now)
	if err != nil {
		return fmt.Errorf("insert terminal event: %w", err)
	}

	// Update turn_ledger
	_, err = db.Exec(`
		UPDATE turn_ledger
		SET final_event_id = ?,
		    final_status = ?,
		    recording_state = 'complete',
		    completed_at = ?
		WHERE turn_id = ?
	`, eventID, string(status), now, turnID)
	if err != nil {
		return fmt.Errorf("update turn_ledger: %w", err)
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
