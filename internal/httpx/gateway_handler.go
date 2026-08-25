package httpx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gateway/internal/adapter"
	"gateway/internal/binding"
	"gateway/internal/db"
	"gateway/internal/embedding"
	"gateway/internal/hashutil"
	"gateway/internal/idgen"
	"gateway/internal/inject"
	"gateway/internal/l0"
	"gateway/internal/paths"
	"gateway/internal/secrets"
	"gateway/internal/turn"
	"gateway/internal/worker"

	"github.com/gin-gonic/gin"
)

const idempotencyTTL = 24 * time.Hour

// defaultInjectionTokenBudget caps how many tokens of retrieved memory context
// are injected into one upstream request (Specify §6.4: hard cap token budget,
// exceed → memory_truncated=true). Overridable via MEMORY_TOKEN_BUDGET.
const defaultInjectionTokenBudget = 3000

// defaultInjectionLimit caps how many retrieved assets are injected.
const defaultInjectionLimit = 20

var errChannelDisabled = errors.New("upstream channel is disabled")

// errRecordingUnavailable is returned by the degradation gate when neither the
// SQLite ledger nor the local durable buffer accepted an event
// (Constitution §1: DB and local buffer both unwritable → do not forward).
var errRecordingUnavailable = errors.New("recording unavailable")

// RecordingDegradedHeader is set on the response whenever a request was
// accepted by the local durable buffer instead of SQLite (dual-write
// degradation, Specify §1.5). Downstream clients can use it to surface that
// memory was recorded in degraded mode rather than written to the L0 ledger
// synchronously.
const RecordingDegradedHeader = "X-Recording-Degraded"

// GatewayHandler handles LLM proxy requests for three protocols.
type GatewayHandler struct {
	db             *sql.DB
	secretsManager *secrets.Manager
	memoryRoot     string
	upstreamClient *adapter.UpstreamClient
	teamsDir       string
	tokenBudget    int
	refineQueue    *worker.Queue
	// embeddingService is the semantic encoder shared with the MCP retrieval
	// path (ALL-233). It is nil when the model failed to load at startup; the
	// injection chain then degrades to FTS-only retrieval.
	embeddingService *embedding.Service

	// sqliteWriteProbe and bufferWriteProbe are test seams for the recording
	// degradation gate. nil means the sink is considered writable; returning a
	// non-nil error simulates a temporarily unwritable sink.
	sqliteWriteProbe func() error
	bufferWriteProbe func() error
}

// NewGatewayHandler creates a new gateway handler. An optional embedding
// service wires semantic retrieval into the LLM injection chain (ALL-233);
// omitting it keeps the FTS-only degradation.
func NewGatewayHandler(db *sql.DB, secretsManager *secrets.Manager, memoryRoot string, services ...*embedding.Service) *GatewayHandler {
	tokenBudget := defaultInjectionTokenBudget
	if raw := os.Getenv("MEMORY_TOKEN_BUDGET"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			tokenBudget = n
		}
	}
	var embeddingService *embedding.Service
	if len(services) > 0 {
		embeddingService = services[0]
	}
	return &GatewayHandler{
		db:               db,
		secretsManager:   secretsManager,
		memoryRoot:       memoryRoot,
		upstreamClient:   adapter.NewUpstreamClient(120 * time.Second),
		teamsDir:         paths.TeamsDir(memoryRoot),
		tokenBudget:      tokenBudget,
		refineQueue:      worker.NewQueue(db, 30*time.Second),
		embeddingService: embeddingService,
	}
}

// SetRecordingProbes overrides writability of the SQLite and local-buffer
// sinks used by the dual-write degradation gate. A probe returning a non-nil
// error makes the sink appear unwritable. Exported for tests only.
func (h *GatewayHandler) SetRecordingProbes(sqliteWritable, bufferWritable func() error) {
	h.sqliteWriteProbe = sqliteWritable
	h.bufferWriteProbe = bufferWritable
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

	upstreamChannel, err := h.getUpstreamChannel(teamID, channelName, protocol)
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

	turnID := idgen.NewTurnID()
	conversationID := c.GetHeader("X-Conversation-ID")
	if conversationID == "" {
		conversationID = requestID
	}

	// Resolve the conversation's session binding (team/agent/identity card) so
	// retrieval is scoped to the correct identity (V4.5 binding isolation). A
	// binding failure degrades to an empty subject and never blocks the LLM
	// path (传话者协议: 绑定缺失进入隔离区并告警，但仍写 L0).
	sessionID, agentID, identityCardID := h.resolveSession(c, conversationID, teamID, upstreamChannel.ID)

	// Phase 3 injection: approved path manifest + active identity card +
	// ACL-filtered retrieval with token budget and L4→L3→L2→L1→L0 layer
	// ordering (V2.1-V2.4, V4.1-V4.5). On retrieval failure degrade to a
	// manifest-only package so the LLM path still flows.
	injectionPkg, injectionText, err := h.buildLLMInjection(c.Request.Context(), teamID, agentID, identityCardID, inbound)
	if err != nil {
		log.Printf("injection build degraded (request %s): %v", requestID, err)
		// Retrieval may be temporarily unavailable, but the upstream request
		// must still carry the approved manifest and binding identity rather
		// than the retired Phase 2 path list or an empty package.
		injectionPkg = adapter.BuildInjectionPackage(teamID, agentID, identityCardID)
		injectionText = inject.Render(injectionPkg)
	}
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
			// The idempotency reservation is part of the inbound persistence
			// gate below, so it degrades to the local buffer together with the
			// event when SQLite is temporarily unwritable.
		}
	}

	inboundEventID := idgen.NewEventID()
	inboundRecord := inboundRecord{
		TurnID:         turnID,
		RequestID:      requestID,
		ConversationID: conversationID,
		SessionID:      sessionID,
		TeamID:         teamID,
		EventID:        inboundEventID,
		RequestBody:    body,
		IdempotencyKey: idempotencyKey,
		InjectionPkg:   injectionPkg,
		InjectionText:  injectionText,
		TokenBudget:    h.tokenBudget,
	}
	degraded, err := h.recordInbound(c.Request.Context(), inboundRecord)
	if err != nil {
		if errors.Is(err, errRecordingUnavailable) {
			writeGatewayError(c, http.StatusInternalServerError, "recording_unavailable", "Memory recording unavailable: SQLite and local buffer are both unwritable; refusing to forward upstream", requestID)
			return
		}
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
		writeGatewayError(c, http.StatusInternalServerError, "recording_error", "Failed to record inbound request", requestID)
		return
	}
	if degraded {
		// The inbound event was accepted by the local durable buffer instead
		// of SQLite (Specify §1.5 degradation). Surface it to the client; for
		// streaming requests this header must be set before the SSE bytes.
		c.Header(RecordingDegradedHeader, "buffer")
	}

	upstreamKey, err := h.secretsManager.RetrieveSecret(upstreamChannel.APIKeyRef)
	if err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
		writeGatewayError(c, http.StatusInternalServerError, "configuration_error", "Failed to retrieve upstream API key", requestID)
		return
	}

	if inbound.Stream {
		h.forwardStream(c, upstreamChannel, injectedBody, protocol, upstreamKey, teamID, turnID, requestID, conversationID, sessionID, agentID, identityCardID, inboundEventID, extractInjectionQuery(inbound))
		return
	}
	h.forwardNonStream(c, upstreamChannel, injectedBody, protocol, upstreamKey, teamID, turnID, requestID, conversationID, sessionID, idempotencyKey, agentID, identityCardID, inboundEventID, extractInjectionQuery(inbound))
}

// inboundRecord carries everything needed to durably record one inbound turn.
type inboundRecord struct {
	TurnID         string
	RequestID      string
	ConversationID string
	SessionID      string
	TeamID         string
	EventID        string
	RequestBody    []byte
	IdempotencyKey string
	InjectionPkg   adapter.InjectionPackage
	InjectionText  string
	TokenBudget    int
}

// terminalRecord carries everything needed to durably record one terminal event.
type terminalRecord struct {
	EventID        string
	TurnID         string
	RequestID      string
	ConversationID string
	SessionID      string
	TeamID         string
	AgentID        string
	IdentityCardID string
	InboundEventID string
	FactText       string
	Status         turn.TerminalStatus
	ErrorMsg       string
	ContentHash    string
	ContentPath    string
}

// recordInbound applies the dual-write degradation gate (Constitution §1.5,
// Specify §1.5) to the inbound event: SQLite first, then the local durable
// buffer. It returns nil as soon as either sink accepted the event, with
// degraded=true when the event landed in the local buffer instead of SQLite.
// It returns errRecordingUnavailable only when both sinks are unwritable — the
// caller must then NOT forward upstream.
func (h *GatewayHandler) recordInbound(ctx context.Context, in inboundRecord) (bool, error) {
	if h.sqliteWriteProbe == nil || h.sqliteWriteProbe() == nil {
		if err := h.persistInboundToSQLite(ctx, in); err == nil {
			return false, nil
		}
	}
	// SQLite is (or appears) unwritable: drop the idempotency reservation so a
	// retry can be re-accepted, then fall back to the local durable buffer.
	h.releaseIdempotency(in.IdempotencyKey, in.RequestID)
	if h.bufferWriteProbe == nil || h.bufferWriteProbe() == nil {
		if err := h.persistInboundToBuffer(ctx, in); err == nil {
			return true, nil
		}
	}
	return false, errRecordingUnavailable
}

func (h *GatewayHandler) persistInboundToSQLite(ctx context.Context, in inboundRecord) error {
	if in.IdempotencyKey != "" {
		if err := h.reserveIdempotency(in.IdempotencyKey, in.RequestID); err != nil {
			return err
		}
	}
	if err := turn.WriteTurnLedger(h.db, in.TurnID, in.RequestID, in.ConversationID, in.SessionID, in.TeamID, 1); err != nil {
		return err
	}
	l0Path, contentHash, err := l0.WriteRequestFile(h.memoryRoot, in.TurnID, in.EventID, json.RawMessage(in.RequestBody))
	if err != nil {
		return err
	}
	if _, err := turn.WriteInboundEventWithID(h.db, in.EventID, in.TurnID, in.RequestID, in.ConversationID, in.SessionID, in.TeamID, contentHash, l0Path); err != nil {
		return err
	}
	if err := h.registerEventFile(in.EventID, l0Path, contentHash); err != nil {
		return err
	}
	if err := h.recordInjectionSnapshot(in.RequestID, in.TurnID, in.InjectionPkg, in.InjectionText, in.TokenBudget); err != nil {
		return err
	}
	return nil
}

func (h *GatewayHandler) persistInboundToBuffer(ctx context.Context, in inboundRecord) error {
	anchor := in.IdempotencyKey
	if anchor == "" {
		anchor = in.RequestID
	}
	payload := db.BufferEventPayload{
		BufferKey:                anchor,
		EventID:                  in.EventID,
		TurnID:                   in.TurnID,
		RequestID:                in.RequestID,
		ConversationID:           in.ConversationID,
		SessionID:                in.SessionID,
		TeamID:                   in.TeamID,
		Direction:                "inbound",
		Sequence:                 1,
		EventType:                "inbound_persisted",
		Status:                   "ok",
		RequestBody:              in.RequestBody,
		InjectionManifestVersion: in.InjectionPkg.ManifestVersion,
		InjectionText:            in.InjectionText,
		InjectionSources:         in.InjectionPkg.SourceEventIDs,
		AgentID:                  "",
		IdentityCardID:           "",
	}
	record, err := db.WriteLocalBuffer(ctx, h.db, h.memoryRoot, payload)
	if err != nil {
		return err
	}
	// Best-effort compensation job; a missing job is covered by the startup
	// recovery pass that replays pending local_buffer rows (§1.8.3).
	_ = worker.EnqueueBufferReplay(ctx, h.db, record.ID, in.RequestID)
	return nil
}

// recordTerminalResult durably records a terminal event with the same
// degradation gate as the inbound event. It never fails the client response:
// the caller decides how to surface the error, and an upstream result that
// already succeeded must still be delivered. degraded=true signals that the
// terminal event landed in the local durable buffer instead of SQLite.
func (h *GatewayHandler) recordTerminalResult(ctx context.Context, tr terminalRecord) (string, bool, error) {
	if h.sqliteWriteProbe == nil || h.sqliteWriteProbe() == nil {
		if recordedEventID, err := h.persistTerminalToSQLite(ctx, tr); err == nil {
			tr.EventID = recordedEventID
			if err := h.enqueueL1Refine(ctx, tr); err != nil {
				// A queue insert can fail after the terminal committed. Persist a
				// replayable handoff before returning success so startup and worker
				// recovery retry this exact completed turn rather than waiting for a
				// later turn to happen to re-trigger refinement.
				if handoffErr := h.persistL1RefineHandoffToBuffer(ctx, tr); handoffErr != nil {
					log.Printf("persist l1 refine handoff for turn %s: %v", tr.TurnID, handoffErr)
				}
				log.Printf("enqueue l1 refine for turn %s: %v", tr.TurnID, err)
			}
			return recordedEventID, false, nil
		}
	}
	if h.bufferWriteProbe == nil || h.bufferWriteProbe() == nil {
		if err := h.persistTerminalToBuffer(ctx, tr); err == nil {
			return tr.EventID, true, nil
		}
	}
	return "", false, errRecordingUnavailable
}

func (h *GatewayHandler) enqueueL1Refine(ctx context.Context, tr terminalRecord) error {
	if tr.Status != turn.StatusComplete || h.refineQueue == nil {
		return nil
	}
	facts := worker.ExtractL1Facts(tr.FactText, tr.TurnID)
	if len(facts) == 0 {
		return nil
	}
	sourceIDs := make([]string, 0, 2)
	if tr.InboundEventID != "" {
		sourceIDs = append(sourceIDs, tr.InboundEventID)
	}
	if tr.EventID != "" {
		sourceIDs = append(sourceIDs, tr.EventID)
	}
	_, err := worker.EnqueueL1Refine(context.WithoutCancel(ctx), h.refineQueue, worker.L1RefinePayload{
		TeamID:         tr.TeamID,
		AgentID:        tr.AgentID,
		IdentityCardID: tr.IdentityCardID,
		TurnID:         tr.TurnID,
		SourceEventIDs: sourceIDs,
		Facts:          facts,
		CompletedAt:    time.Now().UTC(),
	})
	return err
}

func (h *GatewayHandler) persistL1RefineHandoffToBuffer(ctx context.Context, tr terminalRecord) error {
	if tr.Status != turn.StatusComplete || h.refineQueue == nil {
		return nil
	}
	if len(worker.ExtractL1Facts(tr.FactText, tr.TurnID)) == 0 {
		return nil
	}
	record, err := db.WriteLocalBuffer(context.WithoutCancel(ctx), h.db, h.memoryRoot, db.BufferEventPayload{
		BufferKey:      tr.RequestID,
		EventID:        tr.EventID,
		TurnID:         tr.TurnID,
		RequestID:      tr.RequestID,
		ConversationID: tr.ConversationID,
		SessionID:      tr.SessionID,
		TeamID:         tr.TeamID,
		AgentID:        tr.AgentID,
		IdentityCardID: tr.IdentityCardID,
		Direction:      "outbound",
		Sequence:       1,
		EventType:      "l1_refine_pending",
		Status:         "pending",
		FactText:       worker.RedactSensitiveText(tr.FactText),
	})
	if err != nil {
		return err
	}
	// The file is already durable. This job only makes replay prompt; startup
	// reconciliation also finds the file if the queue write fails again.
	_ = worker.EnqueueBufferReplay(context.WithoutCancel(ctx), h.db, record.ID, tr.RequestID)
	return nil
}

func (h *GatewayHandler) persistTerminalToSQLite(ctx context.Context, tr terminalRecord) (string, error) {
	if tr.ContentHash != "" && tr.ContentPath != "" {
		if err := turn.SetTurnResponse(h.db, tr.TurnID, tr.ContentHash, tr.ContentPath); err != nil {
			return "", err
		}
	}
	recordedEventID, err := turn.WriteTerminalStatusWithID(h.db, tr.EventID, tr.TurnID, tr.RequestID, tr.ConversationID, tr.SessionID, tr.TeamID, tr.Status, tr.ErrorMsg)
	if err != nil {
		return "", err
	}
	if tr.ContentHash != "" && tr.ContentPath != "" {
		// WriteTerminalStatusWithID is idempotent: on a no-op or a superseded
		// cancelled placeholder it returns the event id that is actually
		// recorded, so the response content is attached to the real event.
		_ = turn.SetEventContent(h.db, recordedEventID, tr.ContentHash, tr.ContentPath)
		_ = h.registerEventFile(recordedEventID, tr.ContentPath, tr.ContentHash)
	}
	return recordedEventID, nil
}

func (h *GatewayHandler) persistTerminalToBuffer(ctx context.Context, tr terminalRecord) error {
	payload := db.BufferEventPayload{
		BufferKey:      tr.RequestID,
		EventID:        tr.EventID,
		TurnID:         tr.TurnID,
		RequestID:      tr.RequestID,
		ConversationID: tr.ConversationID,
		SessionID:      tr.SessionID,
		TeamID:         tr.TeamID,
		AgentID:        tr.AgentID,
		IdentityCardID: tr.IdentityCardID,
		FactText:       worker.RedactSensitiveText(tr.FactText),
		Direction:      "outbound",
		Sequence:       1,
		EventType:      string(tr.Status),
		Status:         "ok",
		ContentHash:    tr.ContentHash,
		ContentPath:    tr.ContentPath,
		ErrorMsg:       tr.ErrorMsg,
	}
	if tr.Status == turn.StatusError {
		payload.Status = "error"
	}
	record, err := db.WriteLocalBuffer(ctx, h.db, h.memoryRoot, payload)
	if err != nil {
		return err
	}
	_ = worker.EnqueueBufferReplay(ctx, h.db, record.ID, tr.RequestID)
	return nil
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

func (h *GatewayHandler) forwardNonStream(c *gin.Context, upstream *UpstreamChannelInfo, body []byte, protocol, apiKey, teamID, turnID, requestID, conversationID, sessionID, idempotencyKey, agentID, identityCardID, inboundEventID, factText string) {
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
	terminalCtx, terminalCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer terminalCancel()
	if _, terminalDegraded, err := h.recordTerminalResult(terminalCtx, terminalRecord{
		EventID:        terminalEventID,
		TurnID:         turnID,
		RequestID:      requestID,
		ConversationID: conversationID,
		SessionID:      sessionID,
		TeamID:         teamID,
		AgentID:        agentID,
		IdentityCardID: identityCardID,
		InboundEventID: inboundEventID,
		FactText:       factText,
		Status:         turn.StatusComplete,
		ContentHash:    responseHash,
		ContentPath:    responsePath,
	}); err != nil {
		// The upstream result already succeeded; a recording failure must not
		// take the client's response hostage. Log it and deliver normally.
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
	} else if terminalDegraded {
		// Terminal event landed in the local buffer; keep the degradation
		// marker on the response even if the inbound event was recorded fresh.
		c.Header(RecordingDegradedHeader, "buffer")
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

func (h *GatewayHandler) forwardStream(c *gin.Context, upstream *UpstreamChannelInfo, body []byte, protocol, apiKey, teamID, turnID, requestID, conversationID, sessionID, agentID, identityCardID, inboundEventID, factText string) {
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
	// response_started is an intermediate event. When SQLite is unwritable the
	// stream continues anyway and the terminal status is buffered by
	// recordTerminalResult below; killing the stream would lose the upstream
	// result without improving durability.
	if err := turn.WriteResponseStartedEvent(h.db, turnID, requestID, conversationID, sessionID, teamID); err != nil {
		h.recordFailure(turnID, requestID, conversationID, sessionID, teamID, err.Error(), false)
	}

	streamTee := adapter.NewStreamTee(c.Request.Context(), resp.BodyStream, NewSSEWriter(c), h.db, h.memoryRoot, turnID, requestID, conversationID, sessionID, teamID)
	terminalStatus, streamErr := streamTee.Stream()
	errorMessage := ""
	if streamErr != nil {
		errorMessage = streamErr.Error()
	}
	// The terminal event MUST be recorded even when the client disconnected:
	// the request context is already cancelled at this point, so record with a
	// fresh background context. Otherwise the terminal lands in the local
	// buffer (or nowhere) and the turn stays a ghost with no final_status.
	terminalCtx, terminalCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer terminalCancel()
	terminalEventID, _, terminalErr := h.streamTerminalResult(terminalCtx, terminalRecord{
		EventID:        idgen.NewEventID(),
		TurnID:         turnID,
		RequestID:      requestID,
		ConversationID: conversationID,
		SessionID:      sessionID,
		TeamID:         teamID,
		AgentID:        agentID,
		IdentityCardID: identityCardID,
		InboundEventID: inboundEventID,
		FactText:       factText,
		Status:         terminalStatus,
		ErrorMsg:       errorMessage,
	})
	if terminalErr != nil {
		// The stream result is already settled; a recording failure must not
		// retry the upstream. The watchdog missing_response pass closes the
		// turn with a cancelled terminal if SQLite and the buffer both reject it.
		log.Printf("record stream terminal: status=%s err=%v", terminalStatus, terminalErr)
		return
	}
	if terminalStatus == turn.StatusError {
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

// getUpstreamChannel resolves a client-facing channel name plus the requested
// protocol to a usable upstream channel. Resolution order:
//
//  1. Direct name match whose stored protocol equals the requested protocol.
//  2. Protocol-aware alias (channel_aliases) — used when a client runtime uses
//     a fixed channel name whose stored protocol differs from the route's
//     protocol (e.g. Codex Runtime always requests channel "default").
//
// Original error semantics are preserved: disabled → errChannelDisabled (503),
// not found → wrapped error (404), and a protocol mismatch with no alias is
// returned to the caller so it can report protocol_mismatch (400).
func (h *GatewayHandler) getUpstreamChannel(teamID, channelName, protocol string) (*UpstreamChannelInfo, error) {
	channel, enabled, err := h.lookupChannelByName(teamID, channelName)
	switch {
	case err == nil && enabled == 1 && channel.Protocol == protocol:
		return channel, nil
	case err == nil && enabled != 1:
		return nil, errChannelDisabled
	case err == nil:
		// Channel exists and is enabled but its stored protocol differs from the
		// requested protocol. Try a protocol-aware alias before reporting it.
		if target := h.lookupChannelAlias(teamID, channelName, protocol); target != nil {
			return target, nil
		}
		return channel, nil
	case errors.Is(err, sql.ErrNoRows):
		// No direct channel; a protocol-aware alias may still resolve it.
		if target := h.lookupChannelAlias(teamID, channelName, protocol); target != nil {
			return target, nil
		}
		return nil, fmt.Errorf("query upstream channel: %w", err)
	default:
		return nil, fmt.Errorf("query upstream channel: %w", err)
	}
}

// lookupChannelByName returns the highest-priority enabled-or-disabled channel
// matching (teamID, name), plus its enabled flag.
func (h *GatewayHandler) lookupChannelByName(teamID, channelName string) (*UpstreamChannelInfo, int, error) {
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
		return nil, 0, err
	}
	return &channel, enabled, nil
}

// lookupChannelAlias resolves a protocol-aware channel alias to a usable target
// channel. It returns nil when no alias exists for (team, aliasName, protocol)
// or when the target is unusable (missing, disabled, or wrong protocol).
func (h *GatewayHandler) lookupChannelAlias(teamID, aliasName, protocol string) *UpstreamChannelInfo {
	var targetID string
	err := h.db.QueryRow(`
		SELECT target_channel_id
		FROM channel_aliases
		WHERE team_id = ? AND alias_name = ? AND protocol = ?
	`, teamID, aliasName, protocol).Scan(&targetID)
	if err != nil {
		return nil
	}
	channel, enabled, err := h.lookupChannelByID(targetID)
	if err != nil || enabled != 1 || channel.Protocol != protocol {
		return nil
	}
	return channel
}

// lookupChannelByID returns a channel by primary key, plus its enabled flag.
func (h *GatewayHandler) lookupChannelByID(channelID string) (*UpstreamChannelInfo, int, error) {
	var channel UpstreamChannelInfo
	var enabled int
	err := h.db.QueryRow(`
		SELECT id, name, protocol, base_url, model, api_key_ref, COALESCE(failover_channel_id, ''), enabled
		FROM upstream_channels
		WHERE id = ?
	`, channelID).Scan(
		&channel.ID, &channel.Name, &channel.Protocol, &channel.BaseURL, &channel.Model,
		&channel.APIKeyRef, &channel.FailoverChannelID, &enabled,
	)
	if err != nil {
		return nil, 0, err
	}
	return &channel, enabled, nil
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

func (h *GatewayHandler) recordInjectionSnapshot(requestID, turnID string, pkg adapter.InjectionPackage, injectionText string, tokenBudget int) error {
	sourceIDs, err := json.Marshal(pkg.SourceEventIDs)
	if err != nil {
		return fmt.Errorf("marshal injection sources: %w", err)
	}
	usedTokens := (len([]rune(injectionText)) + 3) / 4
	_, err = h.db.Exec(`
		INSERT INTO injection_snapshots (
			id, request_id, turn_id, manifest_version, source_ids_json, token_budget, used_tokens, truncated
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, idgen.NewID(), requestID, turnID, pkg.ManifestVersion, string(sourceIDs), tokenBudget, usedTokens, boolToInt(pkg.Truncated))
	if err != nil {
		return fmt.Errorf("insert injection snapshot: %w", err)
	}
	return nil
}

// resolveSession derives the conversation's durable binding so the injection
// subject (team/agent/identity card) matches the conversation that actually
// made the request. A failed or missing binding degrades to an empty subject;
// it must never block the LLM path (传话者协议 §录入不变量).
func (h *GatewayHandler) resolveSession(c *gin.Context, conversationID, teamID, channelID string) (sessionID, agentID, identityCardID string) {
	apiKeyID, ok := GetAPIKeyID(c)
	if !ok {
		return "", "", ""
	}
	resolved, err := binding.NewResolver(h.db).Resolve(c.Request.Context(), binding.ResolveRequest{
		ConversationID:    conversationID,
		APIKeyID:          apiKeyID,
		TeamID:            teamID,
		UpstreamChannelID: channelID,
		BindingSource:     "service",
	})
	if err != nil {
		log.Printf("session binding resolve degraded (conversation %s): %v", conversationID, err)
		return "", "", ""
	}
	return resolved.SessionID, resolved.AgentID, resolved.IdentityCardID
}

// buildLLMInjection assembles the phase3 injection package for one LLM turn:
// approved path manifest + active identity card + ACL-filtered retrieval with
// token budget and L4→L3→L2→L1→L0 layer ordering. It returns the package and
// its rendered text, or an error so the caller can degrade to a manifest-only
// package instead of blocking the request.
func (h *GatewayHandler) buildLLMInjection(ctx context.Context, teamID, agentID, identityCardID string, inbound *adapter.InboundTurn) (adapter.InjectionPackage, string, error) {
	tokenBudget := h.tokenBudget
	if tokenBudget <= 0 {
		tokenBudget = defaultInjectionTokenBudget
	}
	teamDB, err := db.OpenTeamDB(h.teamsDir, teamID)
	if err != nil {
		return adapter.InjectionPackage{}, "", fmt.Errorf("open team db for retrieval: %w", err)
	}
	defer teamDB.Close()
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		return adapter.InjectionPackage{}, "", fmt.Errorf("ensure team asset schema: %w", err)
	}
	pkg, text, err := inject.Build(ctx, inject.Request{
		GlobalDB:         h.db,
		TeamDB:           teamDB,
		MemoryRoot:       h.memoryRoot,
		TeamID:           teamID,
		AgentID:          agentID,
		IdentityCardID:   identityCardID,
		Query:            extractInjectionQuery(inbound),
		TokenBudget:      tokenBudget,
		Limit:            defaultInjectionLimit,
		EmbeddingService: h.embeddingService,
	})
	if err != nil {
		return adapter.InjectionPackage{}, "", err
	}
	return pkg, text, nil
}

// extractInjectionQuery returns the most recent user text from a normalized
// inbound turn across all three protocols. It is used as the retrieval query
// (V2.1: 每次请求前从记忆库检索并注入 source_ids).
func extractInjectionQuery(inbound *adapter.InboundTurn) string {
	if inbound == nil {
		return ""
	}
	for i := len(inbound.Messages) - 1; i >= 0; i-- {
		msg := inbound.Messages[i]
		role := msg.Role
		if role == "" {
			var obj struct {
				Role string `json:"role"`
			}
			if json.Unmarshal(msg.Content, &obj) == nil {
				role = obj.Role
			}
		}
		if role != "" && role != "user" {
			continue
		}
		if text := userTextFromPayload(msg.Content); text != "" {
			return text
		}
	}
	return ""
}

// userTextFromPayload recursively extracts the last user text from a protocol
// payload that may be a plain string, an object with a content field, or an
// array of content blocks / message items (Anthropic blocks, chat content
// arrays, Responses input items).
func userTextFromPayload(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Role    string          `json:"role"`
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		if obj.Text != "" {
			if obj.Role == "" || obj.Role == "user" {
				return obj.Text
			}
			return ""
		}
		if len(obj.Content) > 0 && string(obj.Content) != "null" {
			return userTextFromPayload(obj.Content)
		}
		return ""
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		for i := len(arr) - 1; i >= 0; i-- {
			if text := userTextFromPayload(arr[i]); text != "" {
				return text
			}
		}
	}
	return ""
}

func (h *GatewayHandler) registerEventFile(eventID, relativePath, contentHash string) error {
	abs, pathErr := paths.SafeJoin(h.memoryRoot, relativePath)
	if pathErr != nil {
		return fmt.Errorf("unsafe L0 file path: %w", pathErr)
	}
	fileInfo, err := os.Stat(abs)
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
		cachedPath, pathErr := paths.SafeJoin(h.memoryRoot, responsePath.String)
		if pathErr != nil {
			return idempotencyNew, "", nil, fmt.Errorf("unsafe idempotent response path: %w", pathErr)
		}
		cachedResponse, err := os.ReadFile(cachedPath)
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
