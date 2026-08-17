package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gateway/internal/adapter"
)

func TestChatCompletionsInjectionPreservesUnknownFields(t *testing.T) {
	original := []byte(`{"model":"chat-test","messages":[{"role":"system","content":"client system"},{"role":"user","content":"hello"}],"response_format":{"type":"json_object"},"stream":false}`)
	forwarded, err := adapter.BuildChatCompletionsUpstreamRequest(original, "memory context")
	if err != nil {
		t.Fatalf("build upstream request: %v", err)
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(forwarded, &request); err != nil {
		t.Fatalf("decode forwarded request: %v", err)
	}
	var messages []map[string]any
	if err := json.Unmarshal(request["messages"], &messages); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(messages) != 3 || messages[0]["role"] != "system" || messages[0]["content"] != "memory context" || messages[1]["content"] != "client system" {
		t.Fatalf("unexpected injected message: %#v", messages)
	}
	if string(request["response_format"]) != `{"type":"json_object"}` {
		t.Fatalf("unknown field was lost: %s", request["response_format"])
	}
}

func TestChatCompletionsIdempotencyReplaysResponse(t *testing.T) {
	upstreamResponse := []byte(`{"id":"chat-1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`)
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(upstreamResponse)
	}))
	defer upstream.Close()

	gateway := newPhase2Gateway(t, upstream.URL, "chat_completions", "default")
	body := []byte(`{"model":"chat-test","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	first := performGatewayRequest(t, gateway.router, "/codebuddy/default/v1/chat/completions", body, gateway.apiKey, map[string]string{"Idempotency-Key": "chat-key-1"})
	if first.Code != http.StatusOK {
		t.Fatalf("first request expected 200, got %d: %s", first.Code, first.Body.String())
	}
	second := performGatewayRequest(t, gateway.router, "/codebuddy/default/v1/chat/completions", body, gateway.apiKey, map[string]string{"Idempotency-Key": "chat-key-1"})
	if second.Code != http.StatusOK {
		t.Fatalf("replay expected 200, got %d: %s", second.Code, second.Body.String())
	}
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) || first.Header().Get("X-Request-ID") != second.Header().Get("X-Request-ID") {
		t.Fatalf("idempotent replay changed response or request ID")
	}
	if upstreamCalls != 1 {
		t.Fatalf("expected one upstream call, got %d", upstreamCalls)
	}
	conflictingBody := []byte(`{"model":"chat-test","messages":[{"role":"user","content":"changed"}],"stream":false}`)
	conflict := performGatewayRequest(t, gateway.router, "/codebuddy/default/v1/chat/completions", conflictingBody, gateway.apiKey, map[string]string{"Idempotency-Key": "chat-key-1"})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("different request with same key expected 409, got %d: %s", conflict.Code, conflict.Body.String())
	}
	var turns int
	if err := gateway.db.QueryRow(`SELECT COUNT(*) FROM turn_ledger`).Scan(&turns); err != nil {
		t.Fatalf("count turns: %v", err)
	}
	if turns != 1 {
		t.Fatalf("expected one turn for replay, got %d", turns)
	}
}

func TestProtocolMismatchIsRejectedBeforeUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("protocol mismatch reached upstream")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	gateway := newPhase2Gateway(t, upstream.URL, "chat_completions", "anthropic-channel")
	body := []byte(`{"model":"claude-test","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`)
	response := performGatewayRequest(t, gateway.router, "/claude-code/anthropic-channel/v1/messages", body, gateway.apiKey, map[string]string{"Idempotency-Key": "mismatch-key"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
	var payload map[string]map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload["error"]["type"] != "protocol_mismatch" {
		t.Fatalf("unexpected error type: %#v", payload)
	}
	var reservations int
	if err := gateway.db.QueryRow(`SELECT COUNT(*) FROM idempotency_keys WHERE key = ?`, "mismatch-key").Scan(&reservations); err != nil {
		t.Fatalf("count idempotency reservations: %v", err)
	}
	if reservations != 0 {
		t.Fatalf("protocol mismatch unexpectedly reserved an idempotency key")
	}
}
