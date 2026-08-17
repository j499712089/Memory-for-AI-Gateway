package test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"gateway/internal/adapter"
	"gateway/internal/turn"
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
