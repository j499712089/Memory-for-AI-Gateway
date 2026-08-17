package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gateway/internal/adapter"
)

func TestResponsesInjectionUsesInputSystemSlot(t *testing.T) {
	original := []byte(`{"model":"responses-test","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"metadata":{"trace":"t-1"}}`)
	forwarded, err := adapter.BuildResponsesUpstreamRequest(original, "memory context")
	if err != nil {
		t.Fatalf("build upstream request: %v", err)
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(forwarded, &request); err != nil {
		t.Fatalf("decode forwarded request: %v", err)
	}
	if _, exists := request["instructions"]; exists {
		t.Fatal("instructions should remain absent when input array is used")
	}
	var input []map[string]any
	if err := json.Unmarshal(request["input"], &input); err != nil {
		t.Fatalf("decode input: %v", err)
	}
	if len(input) != 2 || input[0]["role"] != "system" {
		t.Fatalf("unexpected injected input: %#v", input)
	}
	if string(request["metadata"]) != `{"trace":"t-1"}` {
		t.Fatalf("unknown field was lost: %s", request["metadata"])
	}

	withInstructions, err := adapter.BuildResponsesUpstreamRequest([]byte(`{"model":"responses-test","instructions":"client instructions","input":"hello"}`), "memory context")
	if err != nil {
		t.Fatalf("build request with instructions: %v", err)
	}
	var instructed map[string]json.RawMessage
	if err := json.Unmarshal(withInstructions, &instructed); err != nil {
		t.Fatalf("decode instructed request: %v", err)
	}
	var instructions string
	if err := json.Unmarshal(instructed["instructions"], &instructions); err != nil {
		t.Fatalf("decode injected instructions: %v", err)
	}
	if instructions != "memory context\n\nclient instructions" {
		t.Fatalf("instructions were not injected at the head: %q", instructions)
	}
}

func TestResponsesGatewaySupportsBodyIdempotencyKey(t *testing.T) {
	upstreamResponse := []byte(`{"id":"resp-1","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
	upstreamCalls := 0
	var forwardedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
		forwardedBody = mustReadBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(upstreamResponse)
	}))
	defer upstream.Close()

	gateway := newPhase2Gateway(t, upstream.URL, "responses", "default")
	body := []byte(`{"model":"responses-test","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"idempotency_key":"responses-key-1"}`)
	first := performGatewayRequest(t, gateway.router, "/codex/default/v1/responses", body, gateway.apiKey, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first request expected 200, got %d: %s", first.Code, first.Body.String())
	}
	second := performGatewayRequest(t, gateway.router, "/codex/default/v1/responses", body, gateway.apiKey, nil)
	if second.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatalf("body idempotency replay failed: %d %s", second.Code, second.Body.String())
	}
	if upstreamCalls != 1 {
		t.Fatalf("expected one upstream call, got %d", upstreamCalls)
	}
	var input []map[string]any
	var forwarded map[string]json.RawMessage
	if err := json.Unmarshal(forwardedBody, &forwarded); err != nil {
		t.Fatalf("decode forwarded response request: %v", err)
	}
	if err := json.Unmarshal(forwarded["input"], &input); err != nil {
		t.Fatalf("decode forwarded input: %v", err)
	}
	if len(input) != 2 || input[0]["role"] != "system" {
		t.Fatalf("injection was not placed at input head: %#v", input)
	}
}

func TestUpstreamFailureRecordsErrorAndOutbox(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"upstream down"}`))
	}))
	defer upstream.Close()

	gateway := newPhase2Gateway(t, upstream.URL, "responses", "default")
	body := []byte(`{"model":"responses-test","input":"hello"}`)
	response := performGatewayRequest(t, gateway.router, "/codex/default/v1/responses", body, gateway.apiKey, nil)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", response.Code, response.Body.String())
	}
	requestID := response.Header().Get("X-Request-ID")
	if eventCount(t, gateway.db, requestID, "error") != 1 {
		t.Fatalf("expected one error terminal event")
	}
	var outboxCount int
	if err := gateway.db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE request_id = ?`, requestID).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox tasks: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("expected one outbox retry task, got %d", outboxCount)
	}
}

func TestSameProtocolFailoverUsesConfiguredChannel(t *testing.T) {
	primaryCalls := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	fallbackCalls := 0
	fallbackResponse := []byte(`{"id":"resp-failover","output":[]}`)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fallbackResponse)
	}))
	defer fallback.Close()

	gateway := newPhase2Gateway(t, primary.URL, "responses", "default")
	var keyRef string
	if err := gateway.db.QueryRow(`SELECT api_key_ref FROM upstream_channels WHERE id = ?`, "channel-phase2").Scan(&keyRef); err != nil {
		t.Fatalf("read upstream key reference: %v", err)
	}
	if _, err := gateway.db.Exec(`
		INSERT INTO upstream_channels (id, team_id, name, protocol, base_url, model, api_key_ref, enabled, priority)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, 200)
	`, "channel-failover", gateway.teamID, "fallback", "responses", fallback.URL, "test-model", keyRef); err != nil {
		t.Fatalf("insert failover channel: %v", err)
	}
	if _, err := gateway.db.Exec(`UPDATE upstream_channels SET failover_channel_id = ? WHERE id = ?`, "channel-failover", "channel-phase2"); err != nil {
		t.Fatalf("configure failover channel: %v", err)
	}

	response := performGatewayRequest(t, gateway.router, "/codex/default/v1/responses", []byte(`{"model":"responses-test","input":"hello"}`), gateway.apiKey, nil)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), fallbackResponse) {
		t.Fatalf("failover response mismatch: %d %s", response.Code, response.Body.String())
	}
	if primaryCalls != 3 || fallbackCalls != 1 {
		t.Fatalf("expected three primary retries and one fallback call, got primary=%d fallback=%d", primaryCalls, fallbackCalls)
	}
}
