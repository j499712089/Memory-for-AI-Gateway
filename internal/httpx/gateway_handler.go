package httpx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gateway/internal/adapter"
	"gateway/internal/hashutil"
	"gateway/internal/idgen"
	"gateway/internal/l0"
	"gateway/internal/secrets"
	"gateway/internal/turn"

	"github.com/gin-gonic/gin"
)

const idempotencyTTL = 24 * time.Hour

var errChannelDisabled = errors.New("upstream channel is disabled")

// GatewayHandler handles LLM proxy requests for three protocols.
type GatewayHandler struct {
	db             *sql.DB
	secretsManager *secrets.Manager
	memoryRoot     string
	upstreamClient *adapter.UpstreamClient
}

// NewGatewayHandler creates a new gateway handler.
func NewGatewayHandler(db *sql.DB, secretsManager *secrets.Manager, memoryRoot string) *GatewayHandler {
	return &GatewayHandler{
		db:             db,
		secretsManager: secretsManager,
		memoryRoot:     memoryRoot,
		upstreamClient: adapter.NewUpstreamClient(120 * time.Second),
	}
}

// UpstreamChannelInfo holds the channel data required for a same-protocol request.
type UpstreamChannelInfo struct {
	ID                string
	Name              string
	Protocol          string
	BaseURL           string
	Model             string
	APIKeyRef         string
	FailoverChannelID string
}

type idempotencyState int

const (
	idempotencyNew idempotencyState = iota
	idempotencyCached
	idempotencyConflict
	idempotencyInProgress
)

// HandleAnthropicMessages handles POST /claude-code/{channel}/v1/messages.
func (h *GatewayHandler) HandleAnthropicMessages(c *gin.Context) {
	h.handleProtocolRequest(c, "anthropic_messages")
}

// HandleChatCompletions handles POST /codebuddy/{channel}/v1/chat/completions.
func (h *GatewayHandler) HandleChatCompletions(c *gin.Context) {
	h.handleProtocolRequest(c, "chat_completions")
}

// HandleResponses handles POST /codex/{channel}/v1/responses.
func (h *GatewayHandler) HandleResponses(c *gin.Context) {
	h.handleProtocolRequest(c, "responses")
}

func (h *GatewayHandler) handleProtocolRequest(c *gin.Context, protocol string) {
	teamID, exists := GetTeamID(c)
	if !exists {
		writeGatewayError(c, http.StatusUnauthorized, "unauthorized", "Authentication required", "")
		return
	}

	channelName := c.Param("channel")
	if channelName == "" {
		channelName = "default"
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeGatewayError(c, http.StatusBadRequest, "invalid_request", "Failed to read request body", "")
		return
	}

	inbound, err := parseInboundRequest(protocol, body)
	if err != nil {
		writeGatewayError(c, http.StatusBadRequest, "invalid_request", err.Error(), "")
		return
	}

	requestID := idgen.NewRequestID()
	requestHash := "sha256:" + hashutil.SHA256Bytes(body)
	idempotencyKey := idempotencyKeyForRequest(c, body, protocol)
	c.Header("X-Request-ID", requestID)

	upstreamChannel, err := h.getUpstreamChannel(teamID, channelName)
	if err != nil {
		if errors.Is(err, errChannelDisabled) {
			writeGatewayError(c, http.StatusServiceUnavailable, "channel_disabled", "Upstream channel is disabled", requestID)
			return
		}
		writeGatewayError(c, http.StatusNotFound, "not_found", "Upstream channel not found", requestID)
		return
	}
	if upstreamChannel.Protocol != protocol {
		writeGatewayError(c, http.StatusBadRequest, "protocol_mismatch", fmt.Sprintf("Client uses %s but channel is %s", protocol, upstreamChannel.Protocol), requestID)
		return
	}

	injectionPkg := adapter.BuildInjectionPackage(teamID, "", "")
	injectionText := adapter.RenderInjectionText(injectionPkg)
	injectedBody, err := buildInjectedRequest(protocol, body, injectionText)
	if err != nil {
		writeGatewayError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Failed to inject memory: %v", err), requestID)
		return
	}

	if idempotencyKey != "" {
		state, existingRequestID, cachedResponse, err := h.checkIdempotency(idempotencyKey, requestHash)
		if err != nil {
			writeGatewayError(c, http.StatusInternalServerError, "recording_error", "Failed to resolve idempotency key", requestID)
			return
		}

		switch state {
		case idempotencyCached:
			c.Header("X-Request-ID", existingRequestID)
			c.Data(http.StatusOK, "application/json", cachedResponse)
			return
		case idempotencyConflict:
			writeGatewayError(c, http.StatusConflict, "idempotency_conflict", "Idempotency key was used with a different request", existingRequestID)
			return
		case idempotencyInProgress:
			writeGatewayError(c, http.StatusConflict, "idempotency_conflict", "A request with this idempotency key is already recorded", existingRequestID)
			return
		case idempotencyNew:
			if err := h.reserveIdempotency(idempotencyKey, requestID); err != nil {
				writeGatewayError(c, http.StatusConflict, "idempotency_conflict", "Could not reserve idempotency key", requestID)
				return
			}
		}
	}

	turnID := idgen.NewTurnID()
	// Phase 2 has no binding resolver yet. Keep the nullable foreign key empty
	// rather than manufacturing a session row that does not exist.
	sessionID := ""
	conversationID := c.GetHeader("X-Conversation-ID")
	if conversationID == "" {
		conversationID = requestID
	}

	if err := turn.WriteTurnLedger(h.db, turnID, requestID, conversationID, sessionID, teamID, 1); err != nil {
		h.releaseIdempotency(idempotencyKey, requestID)
		writeGatewayError(c, http.StatusInternalServerError, "recording_error", "Failed to create turn ledger", requestID)
		return
	}

	inboundEventID := idgen.NewEventID()
	l0Path, contentHash, err := l0.WriteRequestFile(h.memoryRoot, turnID, inboundEventID, json.RawMessage(body))
	if err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
		writeGatewayError(c, http.StatusInternalServerError, "recording_error", "Failed to write L0 request", requestID)
		return
	}

	if _, err := turn.WriteInboundEventWithID(h.db, inboundEventID, turnID, requestID, conversationID, sessionID, teamID, contentHash, l0Path); err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
		writeGatewayError(c, http.StatusInternalServerError, "recording_error", "Failed to record inbound event", requestID)
		return
	}
	if err := h.registerEventFile(inboundEventID, l0Path, contentHash); err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
		writeGatewayError(c, http.StatusInternalServerError, "recording_error", "Failed to register L0 request", requestID)
		return
	}

	if err := h.recordInjectionSnapshot(requestID, turnID, injectionPkg, injectionText); err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
		writeGatewayError(c, http.StatusInternalServerError, "recording_error", "Failed to record injection snapshot", requestID)
		return
	}

	upstreamKey, err := h.secretsManager.RetrieveSecret(upstreamChannel.APIKeyRef)
	if err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
		writeGatewayError(c, http.StatusInternalServerError, "configuration_error", "Failed to retrieve upstream API key", requestID)
		return
	}

	if inbound.Stream {
		h.forwardStream(c, upstreamChannel, injectedBody, protocol, upstreamKey, teamID, turnID, requestID, conversationID, sessionID)
		return
	}
	h.forwardNonStream(c, upstreamChannel, injectedBody, protocol, upstreamKey, teamID, turnID, requestID, conversationID, sessionID, idempotencyKey)
}

func parseInboundRequest(protocol string, body []byte) (*adapter.InboundTurn, error) {
	switch protocol {
	case "anthropic_messages":
		return adapter.ParseAnthropicRequest(body)
	case "chat_completions":
		return adapter.ParseChatCompletionsRequest(body)
	case "responses":
		return adapter.ParseResponsesRequest(body)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
}

func buildInjectedRequest(protocol string, body []byte, injectionText string) ([]byte, error) {
	switch protocol {
	case "anthropic_messages":
		return adapter.BuildAnthropicUpstreamRequest(body, injectionText)
	case "chat_completions":
		return adapter.BuildChatCompletionsUpstreamRequest(body, injectionText)
	case "responses":
		return adapter.BuildResponsesUpstreamRequest(body, injectionText)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
}

func (h *GatewayHandler) forwardNonStream(c *gin.Context, upstream *UpstreamChannelInfo, body []byte, protocol, apiKey, teamID, turnID, requestID, conversationID, sessionID, idempotencyKey string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()

	resp, err := h.sendWithFailover(ctx, teamID, upstream, body, protocol, apiKey, false)
	if err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), true)
		writeGatewayError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", requestID)
		return
	}

	terminalEventID := idgen.NewEventID()
	responsePath, responseHash, err := l0.WriteResponseFile(h.memoryRoot, turnID, terminalEventID, json.RawMessage(resp.Body))
	if err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
		writeGatewayError(c, http.StatusInternalServerError, "recording_error", "Failed to write L0 response", requestID)
		return
	}
	if err := turn.SetTurnResponse(h.db, turnID, responseHash, responsePath); err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
		writeGatewayError(c, http.StatusInternalServerError, "recording_error", "Failed to record L0 response", requestID)
		return
	}
	if _, err := turn.WriteTerminalStatusWithID(h.db, terminalEventID, turnID, requestID, conversationID, sessionID, teamID, turn.StatusComplete, ""); err != nil {
		writeGatewayError(c, http.StatusInternalServerError, "recording_error", "Failed to record completion", requestID)
		return
	}
	if err := turn.SetEventContent(h.db, terminalEventID, responseHash, responsePath); err == nil {
		_ = h.registerEventFile(terminalEventID, responsePath, responseHash)
	}
	if idempotencyKey != "" {
		_ = h.completeIdempotency(idempotencyKey, requestID, responsePath)
	}

	contentType := resp.Headers.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, resp.Body)
}

func (h *GatewayHandler) forwardStream(c *gin.Context, upstream *UpstreamChannelInfo, body []byte, protocol, apiKey, teamID, turnID, requestID, conversationID, sessionID string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	resp, err := h.sendWithFailover(ctx, teamID, upstream, body, protocol, apiKey, true)
	if err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), true)
		writeGatewayError(c, http.StatusBadGateway, "upstream_error", "Upstream streaming request failed", requestID)
		return
	}
	if resp.BodyStream == nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, "upstream returned no stream", true)
		writeGatewayError(c, http.StatusBadGateway, "upstream_error", "Upstream returned no stream", requestID)
		return
	}
	if err := turn.WriteResponseStartedEvent(h.db, turnID, requestID, conversationID, sessionID, teamID); err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
		writeGatewayError(c, http.StatusInternalServerError, "recording_error", "Failed to start stream recording", requestID)
		return
	}

	streamTee := adapter.NewStreamTee(resp.BodyStream, NewSSEWriter(c), h.db, h.memoryRoot, turnID, requestID, conversationID, sessionID, teamID)
	terminalStatus, streamErr := streamTee.Stream()
	errorMessage := ""
	if streamErr != nil {
		errorMessage = streamErr.Error()
	}
	terminalEventID, terminalErr := turn.WriteTerminalStatusWithID(h.db, idgen.NewEventID(), turnID, requestID, conversationID, sessionID, teamID, terminalStatus, errorMessage)
	if terminalErr == nil && terminalStatus == turn.StatusError {
		_ = turn.EnqueueOutbox(h.db, requestID, terminalEventID, map[string]string{
			"turn_id": turnID,
			"reason":  errorMessage,
		})
	}
}

func (h *GatewayHandler) sendWithFailover(ctx context.Context, teamID string, primary *UpstreamChannelInfo, body []byte, protocol, apiKey string, stream bool) (*adapter.UpstreamResponse, error) {
	response, err := h.upstreamClient.Send(ctx, h.upstreamRequest(primary, body, protocol, apiKey, stream))
	if err == nil || primary.FailoverChannelID == "" || (response != nil && response.StatusCode >= http.StatusBadRequest && response.StatusCode < http.StatusInternalServerError) {
		return response, err
	}

	fallback, fallbackLookupErr := h.getFailoverChannel(teamID, primary.FailoverChannelID)
	if fallbackLookupErr != nil || fallback.Protocol != protocol {
		return response, err
	}
	fallbackKey, fallbackKeyErr := h.secretsManager.RetrieveSecret(fallback.APIKeyRef)
	if fallbackKeyErr != nil {
		return response, err
	}
	fallbackResponse, fallbackErr := h.upstreamClient.Send(ctx, h.upstreamRequest(fallback, body, protocol, fallbackKey, stream))
	if fallbackErr == nil {
		return fallbackResponse, nil
	}
	if fallbackResponse != nil {
		return fallbackResponse, fmt.Errorf("primary upstream failed: %v; failover failed: %w", err, fallbackErr)
	}
	return response, fmt.Errorf("primary upstream failed: %v; failover failed: %w", err, fallbackErr)
}

func (h *GatewayHandler) upstreamRequest(channel *UpstreamChannelInfo, body []byte, protocol, apiKey string, stream bool) *adapter.UpstreamRequest {
	return &adapter.UpstreamRequest{
		URL:      joinUpstreamURL(channel.BaseURL, getProtocolEndpoint(protocol)),
		Method:   http.MethodPost,
		Headers:  adapter.BuildUpstreamHeaders(protocol, apiKey),
		Body:     body,
		Stream:   stream,
		Protocol: protocol,
	}
}

func (h *GatewayHandler) getUpstreamChannel(teamID, channelName string) (*UpstreamChannelInfo, error) {
	var channel UpstreamChannelInfo
	var enabled int
	err := h.db.QueryRow(`
		SELECT id, name, protocol, base_url, model, api_key_ref, COALESCE(failover_channel_id, ''), enabled
		FROM upstream_channels
		WHERE team_id = ? AND name = ?
		ORDER BY priority ASC
		LIMIT 1
	`, teamID, channelName).Scan(
		&channel.ID, &channel.Name, &channel.Protocol, &channel.BaseURL, &channel.Model,
		&channel.APIKeyRef, &channel.FailoverChannelID, &enabled,
	)
	if err != nil {
		return nil, fmt.Errorf("query upstream channel: %w", err)
	}
	if enabled != 1 {
		return nil, errChannelDisabled
	}
	return &channel, nil
}

func (h *GatewayHandler) getFailoverChannel(teamID, channelID string) (*UpstreamChannelInfo, error) {
	var channel UpstreamChannelInfo
	err := h.db.QueryRow(`
		SELECT id, name, protocol, base_url, model, api_key_ref, COALESCE(failover_channel_id, '')
		FROM upstream_channels
		WHERE id = ? AND team_id = ? AND enabled = 1
	`, channelID, teamID).Scan(
		&channel.ID, &channel.Name, &channel.Protocol, &channel.BaseURL, &channel.Model,
		&channel.APIKeyRef, &channel.FailoverChannelID,
	)
	if err != nil {
		return nil, fmt.Errorf("query failover channel: %w", err)
	}
	return &channel, nil
}

func (h *GatewayHandler) recordInjectionSnapshot(requestID, turnID string, pkg adapter.InjectionPackage, injectionText string) error {
	sourceIDs, err := json.Marshal(pkg.SourceEventIDs)
	if err != nil {
		return fmt.Errorf("marshal injection sources: %w", err)
	}
	usedTokens := (len([]rune(injectionText)) + 3) / 4
	_, err = h.db.Exec(`
		INSERT INTO injection_snapshots (
			id, request_id, turn_id, manifest_version, source_ids_json, token_budget, used_tokens, truncated
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, idgen.NewID(), requestID, turnID, pkg.ManifestVersion, string(sourceIDs), 0, usedTokens, boolToInt(pkg.Truncated))
	if err != nil {
		return fmt.Errorf("insert injection snapshot: %w", err)
	}
	return nil
}

func (h *GatewayHandler) registerEventFile(eventID, relativePath, contentHash string) error {
	fileInfo, err := os.Stat(filepath.Join(h.memoryRoot, relativePath))
	if err != nil {
		return fmt.Errorf("stat L0 file: %w", err)
	}
	if err := l0.RegisterEventFile(h.db, eventID, relativePath, fileInfo.Size(), contentHash); err != nil {
		return fmt.Errorf("register L0 file: %w", err)
	}
	return nil
}

func (h *GatewayHandler) recordFailure(turnID, requestID, conversationID, sessionID, teamID, reason string, enqueueOutbox bool) {
	eventID, err := turn.WriteTerminalStatusWithID(h.db, idgen.NewEventID(), turnID, requestID, conversationID, sessionID, teamID, turn.StatusError, reason)
	if err != nil || !enqueueOutbox {
		return
	}
	_ = turn.EnqueueOutbox(h.db, requestID, eventID, map[string]string{
		"turn_id": turnID,
		"reason":  reason,
	})
}

func (h *GatewayHandler) checkIdempotency(key, requestHash string) (idempotencyState, string, []byte, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var requestID string
	err := h.db.QueryRow(`
		SELECT request_id FROM idempotency_keys WHERE key = ? AND expires_at > ?
	`, key, now).Scan(&requestID)
	if err == sql.ErrNoRows {
		return idempotencyNew, "", nil, nil
	}
	if err != nil {
		return idempotencyNew, "", nil, fmt.Errorf("query idempotency key: %w", err)
	}

	var contentHash, responsePath sql.NullString
	var finalStatus sql.NullString
	err = h.db.QueryRow(`
		SELECT content_hash, l0_reply_path, final_status
		FROM turn_ledger
		WHERE request_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, requestID).Scan(&contentHash, &responsePath, &finalStatus)
	if err == sql.ErrNoRows {
		return idempotencyInProgress, requestID, nil, nil
	}
	if err != nil {
		return idempotencyNew, "", nil, fmt.Errorf("query idempotent turn: %w", err)
	}
	if contentHash.Valid && contentHash.String != "" && contentHash.String != requestHash {
		return idempotencyConflict, requestID, nil, nil
	}
	if finalStatus.Valid && finalStatus.String == string(turn.StatusComplete) && responsePath.Valid && responsePath.String != "" {
		cachedResponse, err := os.ReadFile(filepath.Join(h.memoryRoot, responsePath.String))
		if err != nil {
			return idempotencyNew, "", nil, fmt.Errorf("read idempotent response: %w", err)
		}
		return idempotencyCached, requestID, cachedResponse, nil
	}
	return idempotencyInProgress, requestID, nil, nil
}

func (h *GatewayHandler) reserveIdempotency(key, requestID string) error {
	now := time.Now().UTC()
	if _, err := h.db.Exec(`DELETE FROM idempotency_keys WHERE expires_at <= ?`, now.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("expire idempotency keys: %w", err)
	}
	_, err := h.db.Exec(`
		INSERT INTO idempotency_keys (id, key, request_id, expires_at)
		VALUES (?, ?, ?, ?)
	`, idgen.NewID(), key, requestID, now.Add(idempotencyTTL).Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("reserve idempotency key: %w", err)
	}
	return nil
}

func (h *GatewayHandler) completeIdempotency(key, requestID, responsePath string) error {
	_, err := h.db.Exec(`
		UPDATE idempotency_keys
		SET response_ref = ?
		WHERE key = ? AND request_id = ?
	`, responsePath, key, requestID)
	if err != nil {
		return fmt.Errorf("complete idempotency key: %w", err)
	}
	return nil
}

func (h *GatewayHandler) releaseIdempotency(key, requestID string) {
	if key == "" {
		return
	}
	_, _ = h.db.Exec(`DELETE FROM idempotency_keys WHERE key = ? AND request_id = ?`, key, requestID)
}

func idempotencyKeyForRequest(c *gin.Context, body []byte, protocol string) string {
	if key, ok := GetIdempotencyKey(c); ok && strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key)
	}
	if protocol != "responses" {
		return ""
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return ""
	}
	var key string
	if err := json.Unmarshal(request["idempotency_key"], &key); err != nil {
		return ""
	}
	return strings.TrimSpace(key)
}

func joinUpstreamURL(baseURL, endpoint string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, endpoint) {
		return baseURL
	}
	if strings.HasPrefix(endpoint, "/v1/") && strings.HasSuffix(baseURL, "/v1") {
		return baseURL + strings.TrimPrefix(endpoint, "/v1")
	}
	return baseURL + endpoint
}

func getProtocolEndpoint(protocol string) string {
	switch protocol {
	case "anthropic_messages":
		return "/v1/messages"
	case "chat_completions":
		return "/v1/chat/completions"
	case "responses":
		return "/v1/responses"
	default:
		return ""
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func writeGatewayError(c *gin.Context, status int, errorType, message, requestID string) {
	body := gin.H{
		"type":    errorType,
		"message": message,
	}
	if requestID != "" {
		body["request_id"] = requestID
	}
	c.JSON(status, gin.H{"error": body})
}
