package test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gateway/internal/idgen"
)

// TestChannelAliasResolvesProtocolMismatch verifies that a protocol-aware
// channel alias lets a client runtime that hardcodes a channel name (Codex
// Runtime uses "default") reach a responses-protocol upstream even though the
// stored "default" channel is chat_completions — without disturbing the
// "default" channel's own chat_completions users.
func TestChannelAliasResolvesProtocolMismatch(t *testing.T) {
	chatCalls := 0
	chatUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatCalls++
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected chat upstream path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat-1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer chatUpstream.Close()

	responsesCalls := 0
	responsesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		responsesCalls++
		if r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected responses upstream path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-1","object":"response","status":"completed","output":[]}`))
	}))
	defer responsesUpstream.Close()

	// Gateway with "default" -> chat_completions (the mismatch scenario for a
	// responses request).
	gateway := newPhase2Gateway(t, chatUpstream.URL, "chat_completions", "default")

	// The codex channel's upstream key is supplied via env override so the
	// gateway's RetrieveSecret succeeds without a pre-stored secret file.
	t.Setenv("SECRET_alias_upstream", "test-alias-upstream-key")

	codexID := "channel-codex"
	if _, err := gateway.db.Exec(`
		INSERT INTO upstream_channels (id, team_id, name, protocol, base_url, model, api_key_ref, enabled, priority)
		VALUES (?, ?, 'codex', 'responses', ?, 'e2e-responses-model', 'alias_upstream', 1, 10)
	`, codexID, gateway.teamID, responsesUpstream.URL); err != nil {
		t.Fatalf("insert codex channel: %v", err)
	}
	if _, err := gateway.db.Exec(`
		INSERT INTO channel_aliases (id, team_id, alias_name, protocol, target_channel_id)
		VALUES (?, ?, 'default', 'responses', ?)
	`, idgen.NewID(), gateway.teamID, codexID); err != nil {
		t.Fatalf("insert channel alias: %v", err)
	}

	// 1. The responses request on the hardcoded "default" channel name must be
	//    resolved via the alias to the codex (responses) channel -> 200.
	respBody := []byte(`{"model":"e2e-responses-model","input":"hello","max_output_tokens":16}`)
	resp := performGatewayRequest(t, gateway.router, "/codex/default/responses", respBody, gateway.apiKey, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("alias-resolved responses request expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if responsesCalls != 1 {
		t.Fatalf("expected 1 responses upstream call, got %d", responsesCalls)
	}
	if chatCalls != 0 {
		t.Fatalf("responses request must not reach the chat upstream, got %d calls", chatCalls)
	}

	// 2. The existing "default" chat_completions user is unaffected.
	chatBody := []byte(`{"model":"chat-test","messages":[{"role":"user","content":"hi"}]}`)
	chat := performGatewayRequest(t, gateway.router, "/codebuddy/default/v1/chat/completions", chatBody, gateway.apiKey, nil)
	if chat.Code != http.StatusOK {
		t.Fatalf("direct chat request expected 200, got %d: %s", chat.Code, chat.Body.String())
	}
	if chatCalls != 1 {
		t.Fatalf("expected 1 chat upstream call, got %d", chatCalls)
	}
}

// TestChannelAliasMissingKeepsProtocolMismatch verifies that a protocol
// mismatch with no alias still reports protocol_mismatch (existing behavior).
func TestChannelAliasMissingKeepsProtocolMismatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("protocol mismatch reached upstream")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	// "default" is chat_completions; a responses request with no alias must 400.
	gateway := newPhase2Gateway(t, upstream.URL, "chat_completions", "default")
	body := []byte(`{"model":"e2e-responses-model","input":"hello","max_output_tokens":16}`)
	resp := performGatewayRequest(t, gateway.router, "/codex/default/responses", body, gateway.apiKey, nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}
