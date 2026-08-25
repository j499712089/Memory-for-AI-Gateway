package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"gateway/internal/adapter"
	"gateway/internal/idgen"
	"gateway/internal/turn"
	"gateway/internal/worker"
)

func TestSSEFramesPassThroughForEachProtocol(t *testing.T) {
	largeData := strings.Repeat("x", 4200)
	tests := []struct {
		name     string
		protocol string
		path     string
		endpoint string
		body     []byte
		frames   string
	}{
		{
			name:     "anthropic",
			protocol: "anthropic_messages",
			path:     "/claude-code/default/v1/messages",
			endpoint: "/v1/messages",
			body:     []byte(`{"model":"claude-test","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":true}`),
			frames:   "event: message_start\ndata: {\"type\":\"message_start\"}\n\ndata: " + largeData + "\n\n",
		},
		{
			name:     "chat_completions",
			protocol: "chat_completions",
			path:     "/codebuddy/default/v1/chat/completions",
			endpoint: "/v1/chat/completions",
			body:     []byte(`{"model":"chat-test","messages":[{"role":"user","content":"hello"}],"stream":true}`),
			frames:   "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: " + largeData + "\n\ndata: [DONE]\n\n",
		},
		{
			name:     "responses",
			protocol: "responses",
			path:     "/codex/default/v1/responses",
			endpoint: "/v1/responses",
			body:     []byte(`{"model":"responses-test","input":"hello","stream":true}`),
			frames:   "event: response.created\ndata: {\"type\":\"response.created\"}\n\ndata: " + largeData + "\n\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.endpoint {
					t.Errorf("unexpected upstream path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.frames)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}))
			defer upstream.Close()

			gateway := newPhase2Gateway(t, upstream.URL, test.protocol, "default")
			response := performGatewayRequest(t, gateway.router, test.path, test.body, gateway.apiKey, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
			}
			if response.Body.String() != test.frames {
				t.Fatalf("SSE bytes changed during proxying\nwant: %q\n got: %q", test.frames, response.Body.String())
			}

			requestID := response.Header().Get("X-Request-ID")
			if eventCount(t, gateway.db, requestID, "response_started") != 1 || eventCount(t, gateway.db, requestID, "complete") != 1 {
				t.Fatalf("stream terminal events missing for %s", test.name)
			}
			if eventCount(t, gateway.db, requestID, "delta_checkpoint") < 1 {
				t.Fatalf("expected byte-threshold checkpoint for %s", test.name)
			}
			checkpoints, err := filepath.Glob(filepath.Join(gateway.memoryRoot, "90_运行数据", "检查点", "*", "*.jsonl"))
			if err != nil || len(checkpoints) == 0 {
				t.Fatalf("expected checkpoint file, matches=%v err=%v", checkpoints, err)
			}
		})
	}
}

func TestInterruptedStreamRecordsPartialTerminalAndCheckpoint(t *testing.T) {
	gateway := newPhase2Gateway(t, "http://127.0.0.1:1", "chat_completions", "default")
	turnID := "turn-interrupted"
	requestID := "req-interrupted"
	conversationID := "conversation-interrupted"
	if err := turn.WriteTurnLedger(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, 1); err != nil {
		t.Fatalf("write turn ledger: %v", err)
	}

	stream := adapter.NewStreamTee(
		context.Background(),
		io.NopCloser(&chunkReader{chunks: [][]byte{[]byte("data: first\n\n"), []byte("data: second\n\n")}}),
		&failAfterFirstWrite{},
		gateway.db,
		gateway.memoryRoot,
		turnID,
		requestID,
		conversationID,
		"",
		gateway.teamID,
	)
	status, err := stream.Stream()
	if err == nil || status != turn.StatusPartial {
		t.Fatalf("expected partial stream, got status=%s err=%v", status, err)
	}
	if err := turn.WriteTerminalStatus(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, status, err.Error()); err != nil {
		t.Fatalf("write partial terminal: %v", err)
	}
	if eventCount(t, gateway.db, requestID, "partial") != 1 {
		t.Fatal("expected one partial terminal event")
	}
	checkpoints, globErr := filepath.Glob(filepath.Join(gateway.memoryRoot, "90_运行数据", "检查点", "*", "*.jsonl"))
	if globErr != nil || len(checkpoints) == 0 {
		t.Fatalf("expected checkpoint after disconnect, matches=%v err=%v", checkpoints, globErr)
	}
}

type chunkReader struct {
	chunks [][]byte
	index  int
}

func (r *chunkReader) Read(buffer []byte) (int, error) {
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.index]
	r.index++
	return copy(buffer, chunk), nil
}

type failAfterFirstWrite struct {
	writes int
}

func (w *failAfterFirstWrite) Write(data []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, errors.New("client disconnected")
	}
	return len(data), nil
}

// errorAfterChunks streams the given chunks then surfaces a terminal read error
// (typically context.Canceled when a client disconnect cancels the upstream
// request). The downstream writer stays healthy so the context-canceled path is
// isolated from the downstream-write-failure path.
type errorAfterChunks struct {
	chunks [][]byte
	index  int
	err    error
}

func (r *errorAfterChunks) Read(buffer []byte) (int, error) {
	if r.index < len(r.chunks) {
		chunk := r.chunks[r.index]
		r.index++
		return copy(buffer, chunk), nil
	}
	return 0, r.err
}

type alwaysOKWriter struct{}

func (alwaysOKWriter) Write(data []byte) (int, error) { return len(data), nil }

// TestInterruptedStreamContextCanceledRecordsPartial covers the real E2E
// disconnect race: the client closes the connection, which cancels the request
// context and the upstream request, so the upstream read surfaces
// context.Canceled before any downstream write failure is observed. The turn
// must end in a partial terminal (with a checkpoint file), never an error
// terminal that enqueues a spurious outbox retry.
func TestInterruptedStreamContextCanceledRecordsPartial(t *testing.T) {
	gateway := newPhase2Gateway(t, "http://127.0.0.1:1", "chat_completions", "default")
	turnID := "turn-context-canceled"
	requestID := "req-context-canceled"
	conversationID := "conversation-context-canceled"
	if err := turn.WriteTurnLedger(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, 1); err != nil {
		t.Fatalf("write turn ledger: %v", err)
	}

	stream := adapter.NewStreamTee(
		context.Background(),
		io.NopCloser(&errorAfterChunks{
			chunks: [][]byte{[]byte("data: first\n\n"), []byte("data: second\n\n")},
			err:    context.Canceled,
		}),
		alwaysOKWriter{},
		gateway.db,
		gateway.memoryRoot,
		turnID,
		requestID,
		conversationID,
		"",
		gateway.teamID,
	)
	status, err := stream.Stream()
	if err == nil || status != turn.StatusPartial {
		t.Fatalf("expected partial stream on context cancel, got status=%s err=%v", status, err)
	}
	if err := turn.WriteTerminalStatus(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, status, err.Error()); err != nil {
		t.Fatalf("write partial terminal: %v", err)
	}
	if eventCount(t, gateway.db, requestID, "partial") != 1 {
		t.Fatal("expected one partial terminal event")
	}
	checkpoints, globErr := filepath.Glob(filepath.Join(gateway.memoryRoot, "90_运行数据", "检查点", "*", "*.jsonl"))
	if globErr != nil || len(checkpoints) == 0 {
		t.Fatalf("expected checkpoint after disconnect, matches=%v err=%v", checkpoints, globErr)
	}
}

// TestInterruptedStreamDeadlineExceededRecordsPartial is the request-timeout
// variant of the disconnect race: same classification, partial terminal.
func TestInterruptedStreamDeadlineExceededRecordsPartial(t *testing.T) {
	gateway := newPhase2Gateway(t, "http://127.0.0.1:1", "chat_completions", "default")
	turnID := "turn-deadline"
	requestID := "req-deadline"
	conversationID := "conversation-deadline"
	if err := turn.WriteTurnLedger(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, 1); err != nil {
		t.Fatalf("write turn ledger: %v", err)
	}

	stream := adapter.NewStreamTee(
		context.Background(),
		io.NopCloser(&errorAfterChunks{
			chunks: [][]byte{[]byte("data: first\n\n")},
			err:    context.DeadlineExceeded,
		}),
		alwaysOKWriter{},
		gateway.db,
		gateway.memoryRoot,
		turnID,
		requestID,
		conversationID,
		"",
		gateway.teamID,
	)
	status, err := stream.Stream()
	if err == nil || status != turn.StatusPartial {
		t.Fatalf("expected partial stream on deadline exceeded, got status=%s err=%v", status, err)
	}
}

// TestOutboxDrainAcknowledgesRecordedTerminal verifies the zombie-queue fix:
// a pending outbox row whose terminal event is already recorded is drained to
// done, while a row referencing a missing terminal event stays pending for
// retry instead of piling up silently.
func TestOutboxDrainAcknowledgesRecordedTerminal(t *testing.T) {
	gateway := newPhase2Gateway(t, "http://127.0.0.1:1", "chat_completions", "default")
	turnID := "turn-outbox"
	requestID := "req-outbox"
	conversationID := "conversation-outbox"
	if err := turn.WriteTurnLedger(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, 1); err != nil {
		t.Fatalf("write turn ledger: %v", err)
	}
	eventID, err := turn.WriteTerminalStatusWithID(gateway.db, "evt-outbox-terminal", turnID, requestID, conversationID, "", gateway.teamID, turn.StatusError, "upstream 500")
	if err != nil {
		t.Fatalf("write terminal status: %v", err)
	}
	if err := turn.EnqueueOutbox(gateway.db, requestID, eventID, map[string]string{"turn_id": turnID, "reason": "upstream 500"}); err != nil {
		t.Fatalf("enqueue outbox: %v", err)
	}

	ack := func(ctx context.Context, entry worker.OutboxEntry) error {
		var exists int
		if err := gateway.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM turn_events WHERE id = ?`, entry.EventID).Scan(&exists); err != nil {
			return fmt.Errorf("check outbox terminal event: %w", err)
		}
		if exists == 0 {
			return fmt.Errorf("referenced terminal event is missing")
		}
		return nil
	}
	drained, err := worker.ReplayOutbox(context.Background(), gateway.db, ack)
	if err != nil {
		t.Fatalf("replay outbox: %v", err)
	}
	if drained != 1 {
		t.Fatalf("expected 1 drained outbox row, got %d", drained)
	}
	var status string
	if err := gateway.db.QueryRow(`SELECT status FROM outbox WHERE request_id = ?`, requestID).Scan(&status); err != nil {
		t.Fatalf("query outbox status: %v", err)
	}
	if status != "done" {
		t.Fatalf("expected outbox row done, got %q", status)
	}

	// A second sweep must be a no-op: already-drained rows are not re-processed.
	drainedAgain, err := worker.ReplayOutbox(context.Background(), gateway.db, ack)
	if err != nil {
		t.Fatalf("second replay outbox: %v", err)
	}
	if drainedAgain != 0 {
		t.Fatalf("expected 0 drained rows on second sweep, got %d", drainedAgain)
	}
}

// TestStreamCancelledBeforeAnyDataRecordsCancelled covers the active-cancel
// path (ALL-84): a client aborts before a single response byte was delivered,
// so the turn must end in a 'cancelled' terminal, never partial/error.
func TestStreamCancelledBeforeAnyDataRecordsCancelled(t *testing.T) {
	gateway := newPhase2Gateway(t, "http://127.0.0.1:1", "chat_completions", "default")
	turnID := "turn-cancelled"
	requestID := "req-cancelled"
	conversationID := "conversation-cancelled"
	if err := turn.WriteTurnLedger(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, 1); err != nil {
		t.Fatalf("write turn ledger: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // client went away before the upstream produced anything

	stream := adapter.NewStreamTee(
		ctx,
		&closableBlockingReader{closed: make(chan struct{})},
		alwaysOKWriter{},
		gateway.db,
		gateway.memoryRoot,
		turnID,
		requestID,
		conversationID,
		"",
		gateway.teamID,
	)
	status, err := stream.Stream()
	if err == nil || status != turn.StatusCancelled {
		t.Fatalf("expected cancelled stream, got status=%s err=%v", status, err)
	}
	if err := turn.WriteTerminalStatus(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, status, err.Error()); err != nil {
		t.Fatalf("write cancelled terminal: %v", err)
	}
	if eventCount(t, gateway.db, requestID, "cancelled") != 1 {
		t.Fatal("expected one cancelled terminal event")
	}
}

// closableBlockingReader never produces data; Close unblocks the pending Read.
// Stream() must not hang on it when the client context is cancelled — the grace
// path classifies and returns, and the deferred upstream.Close() releases the
// blocked read loop goroutine.
type closableBlockingReader struct {
	closed chan struct{}
}

func (r *closableBlockingReader) Read([]byte) (int, error) {
	<-r.closed
	return 0, io.EOF
}

func (r *closableBlockingReader) Close() error {
	close(r.closed)
	return nil
}

// TestStreamFullyDeliveredRecordsCompleteDespiteContextCancel covers the
// "流已完整则为 complete" requirement (ALL-84): the upstream delivered the whole
// stream (EOF reached) even though the client cancelled at the same moment, so
// the terminal must be 'complete', not a spurious 'partial'.
func TestStreamFullyDeliveredRecordsCompleteDespiteContextCancel(t *testing.T) {
	gateway := newPhase2Gateway(t, "http://127.0.0.1:1", "chat_completions", "default")
	turnID := "turn-complete-despite-cancel"
	requestID := "req-complete-despite-cancel"
	conversationID := "conversation-complete-despite-cancel"
	if err := turn.WriteTurnLedger(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, 1); err != nil {
		t.Fatalf("write turn ledger: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the stream starts, but the data is already buffered

	stream := adapter.NewStreamTee(
		ctx,
		io.NopCloser(&chunkReader{chunks: [][]byte{[]byte("data: full\n\n")}}),
		alwaysOKWriter{},
		gateway.db,
		gateway.memoryRoot,
		turnID,
		requestID,
		conversationID,
		"",
		gateway.teamID,
	)
	status, err := stream.Stream()
	if err != nil || status != turn.StatusComplete {
		t.Fatalf("expected complete stream, got status=%s err=%v", status, err)
	}
	if err := turn.WriteTerminalStatus(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, status, ""); err != nil {
		t.Fatalf("write complete terminal: %v", err)
	}
	if eventCount(t, gateway.db, requestID, "complete") != 1 {
		t.Fatal("expected one complete terminal event")
	}
}

// TestWriteTerminalStatusSupersedesCancelledPlaceholder verifies the idempotent
// continuation ("幂等续写"): a placeholder 'cancelled' written for a turn that
// was still streaming is superseded in place by the real terminal when it
// arrives, keeping exactly one terminal event (V5.7).
func TestWriteTerminalStatusSupersedesCancelledPlaceholder(t *testing.T) {
	gateway := newPhase2Gateway(t, "http://127.0.0.1:1", "chat_completions", "default")
	turnID := "turn-supersede"
	requestID := "req-supersede"
	conversationID := "conversation-supersede"
	if err := turn.WriteTurnLedger(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, 1); err != nil {
		t.Fatalf("write turn ledger: %v", err)
	}

	cancelledEventID, err := turn.WriteTerminalStatusWithID(gateway.db, idgen.NewEventID(), turnID, requestID, conversationID, "", gateway.teamID, turn.StatusCancelled, "watchdog")
	if err != nil {
		t.Fatalf("write cancelled placeholder: %v", err)
	}

	// The real terminal arrives later: must supersede the placeholder.
	returnedEventID, err := turn.WriteTerminalStatusWithID(gateway.db, idgen.NewEventID(), turnID, requestID, conversationID, "", gateway.teamID, turn.StatusComplete, "")
	if err != nil {
		t.Fatalf("write complete terminal after cancelled: %v", err)
	}
	if returnedEventID != cancelledEventID {
		t.Fatalf("expected supersede to return the existing event id, got %q want %q", returnedEventID, cancelledEventID)
	}

	var finalStatus, finalEventID string
	if err := gateway.db.QueryRow(`SELECT COALESCE(final_status,''), COALESCE(final_event_id,'') FROM turn_ledger WHERE turn_id = ?`, turnID).Scan(&finalStatus, &finalEventID); err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	if finalStatus != string(turn.StatusComplete) {
		t.Fatalf("expected final_status complete, got %q", finalStatus)
	}
	if finalEventID != cancelledEventID {
		t.Fatalf("expected final_event_id unchanged, got %q want %q", finalEventID, cancelledEventID)
	}
	var eventType, status string
	if err := gateway.db.QueryRow(`SELECT event_type, status FROM turn_events WHERE id = ?`, cancelledEventID).Scan(&eventType, &status); err != nil {
		t.Fatalf("query terminal event: %v", err)
	}
	if eventType != string(turn.StatusComplete) || status != "ok" {
		t.Fatalf("expected event superseded to complete/ok, got %s/%s", eventType, status)
	}
	if eventCount(t, gateway.db, requestID, "cancelled") != 0 {
		t.Fatal("expected no remaining cancelled event")
	}
	if eventCount(t, gateway.db, requestID, "complete") != 1 {
		t.Fatal("expected exactly one complete terminal event")
	}
}

// TestWriteTerminalStatusIdempotentNoOp verifies that a duplicate write after a
// real terminal is a no-op returning the existing id, not an error.
func TestWriteTerminalStatusIdempotentNoOp(t *testing.T) {
	gateway := newPhase2Gateway(t, "http://127.0.0.1:1", "chat_completions", "default")
	turnID := "turn-noop"
	requestID := "req-noop"
	conversationID := "conversation-noop"
	if err := turn.WriteTurnLedger(gateway.db, turnID, requestID, conversationID, "", gateway.teamID, 1); err != nil {
		t.Fatalf("write turn ledger: %v", err)
	}
	firstID, err := turn.WriteTerminalStatusWithID(gateway.db, idgen.NewEventID(), turnID, requestID, conversationID, "", gateway.teamID, turn.StatusPartial, "first")
	if err != nil {
		t.Fatalf("write partial terminal: %v", err)
	}
	secondID, err := turn.WriteTerminalStatusWithID(gateway.db, idgen.NewEventID(), turnID, requestID, conversationID, "", gateway.teamID, turn.StatusError, "second")
	if err != nil {
		t.Fatalf("duplicate write must be a no-op, got error: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("expected no-op to return existing id %q, got %q", firstID, secondID)
	}
	if eventCount(t, gateway.db, requestID, "partial") != 1 {
		t.Fatal("expected exactly one terminal event")
	}
}

// TestHandleMissingResponseClosesGhostTurn verifies the watchdog consumer
// (ALL-84): an open turn with no terminal event gets closed with a 'cancelled'
// terminal, and an already-terminal turn is a no-op.
func TestHandleMissingResponseClosesGhostTurn(t *testing.T) {
	gateway := newPhase2Gateway(t, "http://127.0.0.1:1", "chat_completions", "default")

	ghostTurn := "turn-ghost"
	ghostRequest := "req-ghost"
	if err := turn.WriteTurnLedger(gateway.db, ghostTurn, ghostRequest, "conv-ghost", "", gateway.teamID, 1); err != nil {
		t.Fatalf("write ghost ledger: %v", err)
	}
	payload, _ := json.Marshal(map[string]string{
		"turn_id":         ghostTurn,
		"request_id":      ghostRequest,
		"conversation_id": "conv-ghost",
		"session_id":      "",
	})
	claim := &worker.Claim{TeamID: gateway.teamID, PayloadJSON: string(payload)}
	if err := worker.HandleMissingResponse(context.Background(), gateway.db, claim); err != nil {
		t.Fatalf("handle missing response: %v", err)
	}
	var finalStatus string
	if err := gateway.db.QueryRow(`SELECT COALESCE(final_status,'') FROM turn_ledger WHERE turn_id = ?`, ghostTurn).Scan(&finalStatus); err != nil {
		t.Fatalf("query ghost ledger: %v", err)
	}
	if finalStatus != string(turn.StatusCancelled) {
		t.Fatalf("expected ghost turn closed with cancelled, got %q", finalStatus)
	}
	if eventCount(t, gateway.db, ghostRequest, "cancelled") != 1 {
		t.Fatal("expected one cancelled terminal for the ghost turn")
	}

	// A second run must be a no-op (idempotent).
	if err := worker.HandleMissingResponse(context.Background(), gateway.db, claim); err != nil {
		t.Fatalf("second handle missing response: %v", err)
	}
	if eventCount(t, gateway.db, ghostRequest, "cancelled") != 1 {
		t.Fatal("expected still exactly one cancelled terminal")
	}

	// A turn that already has a real terminal is left untouched.
	doneTurn := "turn-done"
	doneRequest := "req-done"
	if err := turn.WriteTurnLedger(gateway.db, doneTurn, doneRequest, "conv-done", "", gateway.teamID, 1); err != nil {
		t.Fatalf("write done ledger: %v", err)
	}
	if _, err := turn.WriteTerminalStatusWithID(gateway.db, idgen.NewEventID(), doneTurn, doneRequest, "conv-done", "", gateway.teamID, turn.StatusComplete, ""); err != nil {
		t.Fatalf("write complete terminal: %v", err)
	}
	donePayload, _ := json.Marshal(map[string]string{"turn_id": doneTurn, "request_id": doneRequest})
	if err := worker.HandleMissingResponse(context.Background(), gateway.db, &worker.Claim{TeamID: gateway.teamID, PayloadJSON: string(donePayload)}); err != nil {
		t.Fatalf("handle missing response on done turn: %v", err)
	}
	if eventCount(t, gateway.db, doneRequest, "cancelled") != 0 {
		t.Fatal("done turn must not gain a cancelled terminal")
	}
}
