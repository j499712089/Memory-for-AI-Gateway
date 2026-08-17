package httpx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"gateway/internal/adapter"
	"gateway/internal/idgen"
	"gateway/internal/l0"
	"gateway/internal/secrets"
	"gateway/internal/turn"

	"github.com/gin-gonic/gin"
)

// GatewayHandler handles LLM proxy requests for three protocols
type GatewayHandler struct {
	db             *sql.DB
	secretsManager *secrets.Manager
	memoryRoot     string
	upstreamClient *adapter.UpstreamClient
}

// NewGatewayHandler creates a new gateway handler
func NewGatewayHandler(db *sql.DB, secretsManager *secrets.Manager, memoryRoot string) *GatewayHandler {
	return &GatewayHandler{
		db:             db,
		secretsManager: secretsManager,
		memoryRoot:     memoryRoot,
		upstreamClient: adapter.NewUpstreamClient(120 * time.Second),
	}
}

// UpstreamChannelInfo holds upstream channel configuration
type UpstreamChannelInfo struct {
	ID       string
	Protocol string
	BaseURL  string
	Model    string
	APIKeyRef string
}

// HandleAnthropicMessages handles POST /claude-code/{channel}/v1/messages
func (h *GatewayHandler) HandleAnthropicMessages(c *gin.Context) {
	h.handleProtocolRequest(c, "anthropic_messages", "/v1/messages")
}

// HandleChatCompletions handles POST /codebuddy/{channel}/v1/chat/completions
func (h *GatewayHandler) HandleChatCompletions(c *gin.Context) {
	h.handleProtocolRequest(c, "chat_completions", "/v1/chat/completions")
}

// HandleResponses handles POST /codex/{channel}/v1/responses
func (h *GatewayHandler) HandleResponses(c *gin.Context) {
	h.handleProtocolRequest(c, "responses", "/v1/responses")
}

// handleProtocolRequest handles a protocol-specific request
func (h *GatewayHandler) handleProtocolRequest(c *gin.Context, protocol, endpoint string) {
	// Extract team_id from auth middleware
	teamID, exists := GetTeamID(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"type":    "unauthorized",
				"message": "Authentication required",
			},
		})
		return
	}

	// Get channel from path
	channel := c.Param("channel")
	if channel == "" {
		channel = "default"
	}

	// Read request body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request",
				"message": "Failed to read request body",
			},
		})
		return
	}

	// Generate IDs
	requestID := idgen.NewRequestID()
	turnID := idgen.NewTurnID()
	conversationID := c.GetHeader("X-Conversation-ID")
	if conversationID == "" {
		conversationID = requestID // Fallback
	}

	// Get upstream channel (Phase 2: simplified, no real binding resolution)
	upstreamChannel, err := h.getUpstreamChannel(teamID, channel, protocol)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "channel_not_found",
				"message": fmt.Sprintf("Upstream channel not found: %v", err),
			},
		})
		return
	}

	// Protocol mismatch check
	if upstreamChannel.Protocol != protocol {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "protocol_mismatch",
				"message": fmt.Sprintf("Client uses %s but channel is %s", protocol, upstreamChannel.Protocol),
			},
		})
		return
	}

	// Build injection text (Phase 2: placeholder)
	injectionPkg := adapter.BuildInjectionPackage(teamID, "", "")
	injectionText := adapter.RenderInjectionText(injectionPkg)

	// Inject memory package into request
	var injectedBody []byte
	switch protocol {
	case "anthropic_messages":
		injectedBody, err = adapter.BuildAnthropicUpstreamRequest(body, injectionText)
	case "chat_completions":
		injectedBody, err = adapter.BuildChatCompletionsUpstreamRequest(body, injectionText)
	case "responses":
		injectedBody, err = adapter.BuildResponsesUpstreamRequest(body, injectionText)
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "unsupported_protocol",
				"message": "Protocol not supported",
			},
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "injection_error",
				"message": fmt.Sprintf("Failed to inject memory: %v", err),
			},
		})
		return
	}

	// Write turn ledger (BEGIN IMMEDIATE transaction)
	sessionID := requestID // Phase 2: simplified
	if err := turn.WriteTurnLedger(h.db, turnID, requestID, conversationID, sessionID, teamID, 1); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "recording_error",
				"message": "Failed to create turn ledger",
			},
		})
		return
	}

	// Write L0 request file
	l0Path, contentHash, err := l0.WriteRequestFile(h.memoryRoot, turnID, requestID, json.RawMessage(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "recording_error",
				"message": "Failed to write L0 request",
			},
		})
		return
	}

	// Write inbound_persisted event
	if err := turn.WriteInboundEvent(h.db, turnID, requestID, conversationID, sessionID, teamID, contentHash, l0Path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "recording_error",
				"message": "Failed to record inbound event",
			},
		})
		return
	}

	// Register event file
	fileInfo, _ := l0.CheckEventFileExists(h.db, requestID)
	if !fileInfo {
		l0.RegisterEventFile(h.db, requestID, l0Path, int64(len(body)), contentHash)
	}

	// Get upstream API key
	upstreamKey, err := h.secretsManager.RetrieveSecret(upstreamChannel.APIKeyRef)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "configuration_error",
				"message": "Failed to retrieve upstream API key",
			},
		})
		return
	}

	// Check if streaming
	var isStream bool
	var streamField map[string]any
	json.Unmarshal(body, &streamField)
	if s, ok := streamField["stream"].(bool); ok {
		isStream = s
	}

	// Forward to upstream
	if isStream {
		h.forwardStream(c, upstreamChannel, injectedBody, protocol, upstreamKey, turnID, requestID, conversationID, sessionID, teamID)
	} else {
		h.forwardNonStream(c, upstreamChannel, injectedBody, protocol, upstreamKey, turnID, requestID, conversationID, sessionID, teamID)
	}
}

// forwardNonStream forwards non-streaming request to upstream
func (h *GatewayHandler) forwardNonStream(c *gin.Context, upstream *UpstreamChannelInfo, body []byte, protocol, apiKey, turnID, requestID, conversationID, sessionID, teamID string) {
	// Build upstream request
	upstreamReq := &adapter.UpstreamRequest{
		URL:      upstream.BaseURL + getProtocolEndpoint(protocol),
		Method:   "POST",
		Headers:  adapter.BuildUpstreamHeaders(protocol, apiKey),
		Body:     body,
		Stream:   false,
		Protocol: protocol,
	}

	// Send to upstream
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp, err := h.upstreamClient.Send(ctx, upstreamReq)
	if err != nil {
		// Write error terminal status
		turn.WriteTerminalStatus(h.db, turnID, requestID, conversationID, sessionID, teamID, turn.StatusError, err.Error())

		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{
				"type":       "upstream_error",
				"message":    "Upstream request failed",
				"request_id": requestID,
			},
		})
		return
	}

	// Write response to L0
	l0.WriteResponseFile(h.memoryRoot, turnID, requestID, json.RawMessage(resp.Body))

	// Write complete terminal status
	turn.WriteTerminalStatus(h.db, turnID, requestID, conversationID, sessionID, teamID, turn.StatusComplete, "")

	// Pass through response to client
	c.Data(resp.StatusCode, "application/json", resp.Body)
}

// forwardStream forwards streaming request to upstream with checkpoint
func (h *GatewayHandler) forwardStream(c *gin.Context, upstream *UpstreamChannelInfo, body []byte, protocol, apiKey, turnID, requestID, conversationID, sessionID, teamID string) {
	// Build upstream request
	upstreamReq := &adapter.UpstreamRequest{
		URL:      upstream.BaseURL + getProtocolEndpoint(protocol),
		Method:   "POST",
		Headers:  adapter.BuildUpstreamHeaders(protocol, apiKey),
		Body:     body,
		Stream:   true,
		Protocol: protocol,
	}

	// Send to upstream
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second) // Longer timeout for streaming
	defer cancel()

	resp, err := h.upstreamClient.Send(ctx, upstreamReq)
	if err != nil {
		turn.WriteTerminalStatus(h.db, turnID, requestID, conversationID, sessionID, teamID, turn.StatusError, err.Error())

		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{
				"type":       "upstream_error",
				"message":    "Upstream streaming request failed",
				"request_id": requestID,
			},
		})
		return
	}

	// Write response_started event
	turn.WriteResponseStartedEvent(h.db, turnID, requestID, conversationID, sessionID, teamID)

	// Set up SSE
	sseWriter := NewSSEWriter(c)

	// Create stream tee for interception
	streamTee := adapter.NewStreamTee(
		resp.BodyStream,
		sseWriter,
		h.db,
		h.memoryRoot,
		turnID,
		requestID,
		conversationID,
		sessionID,
		teamID,
	)

	// Stream and checkpoint
	terminalStatus, streamErr := streamTee.Stream()

	// Write terminal status
	errorMsg := ""
	if streamErr != nil {
		errorMsg = streamErr.Error()
	}
	turn.WriteTerminalStatus(h.db, turnID, requestID, conversationID, sessionID, teamID, terminalStatus, errorMsg)
}

// getUpstreamChannel retrieves upstream channel configuration
// Phase 2: Simplified implementation, returns first matching channel
func (h *GatewayHandler) getUpstreamChannel(teamID, channelName, protocol string) (*UpstreamChannelInfo, error) {
	var channel UpstreamChannelInfo

	err := h.db.QueryRow(`
		SELECT id, protocol, base_url, model, api_key_ref
		FROM upstream_channels
		WHERE enabled = 1 AND protocol = ?
		ORDER BY priority DESC
		LIMIT 1
	`, protocol).Scan(&channel.ID, &channel.Protocol, &channel.BaseURL, &channel.Model, &channel.APIKeyRef)

	if err != nil {
		return nil, fmt.Errorf("query upstream channel: %w", err)
	}

	return &channel, nil
}

// getProtocolEndpoint returns the API endpoint for a protocol
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
