package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"gateway/internal/idgen"
	"gateway/internal/turn"
)

// MissingResponsePayload is the payload of a watchdog-enqueued missing_response
// job: a turn that opened but never received a terminal event.
type MissingResponsePayload struct {
	TurnID         string `json:"turn_id"`
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	SessionID      string `json:"session_id"`
}

// RegisterMissingResponse wires the missing_response handler into a processor.
// The handler closes ghost turns — open ledger rows that never received a
// terminal event — by recording a 'cancelled' terminal, so watchdog-detected
// dead turns converge to exactly one terminal and stop showing up as ghosts in
// per-turn terminal audits (ALL-53 V5.6 / V5.7).
func RegisterMissingResponse(processor *Processor, database *sql.DB) error {
	if processor == nil {
		return fmt.Errorf("missing_response processor is nil")
	}
	processor.Register("missing_response", func(ctx context.Context, claim *Claim) error {
		return HandleMissingResponse(ctx, database, claim)
	})
	return nil
}

// HandleMissingResponse closes one ghost turn. It is idempotent:
//   - turn not found        -> no-op (nothing left to close)
//   - turn already terminal -> no-op (the real terminal arrived meanwhile)
//   - turn still open       -> record a 'cancelled' terminal
//
// The terminal write is idempotent on the ledger side, so a real terminal that
// arrives later supersedes this placeholder instead of erroring.
func HandleMissingResponse(ctx context.Context, database *sql.DB, claim *Claim) error {
	if database == nil {
		return fmt.Errorf("missing_response database is nil")
	}
	if claim == nil {
		return fmt.Errorf("missing_response claim is nil")
	}
	var payload MissingResponsePayload
	if err := json.Unmarshal([]byte(claim.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("missing_response payload: %w", err)
	}
	if payload.TurnID == "" {
		return fmt.Errorf("missing_response payload has no turn_id")
	}

	var finalStatus sql.NullString
	err := database.QueryRowContext(ctx, `SELECT final_status FROM turn_ledger WHERE turn_id = ?`, payload.TurnID).Scan(&finalStatus)
	if errors.Is(err, sql.ErrNoRows) {
		// Turn is gone; nothing to close.
		return nil
	}
	if err != nil {
		return fmt.Errorf("missing_response check terminal: %w", err)
	}
	if finalStatus.Valid && finalStatus.String != "" {
		// The real terminal arrived before this job was claimed.
		return nil
	}

	_, err = turn.WriteTerminalStatusWithID(
		database,
		idgen.NewEventID(),
		payload.TurnID,
		payload.RequestID,
		payload.ConversationID,
		payload.SessionID,
		claim.TeamID,
		turn.StatusCancelled,
		"terminal never recorded; watchdog closed ghost turn",
	)
	if err != nil {
		return fmt.Errorf("missing_response close ghost turn: %w", err)
	}
	return nil
}
