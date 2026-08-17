package adapter

import "encoding/json"

// InboundTurn represents a normalized incoming request across all three protocols
type InboundTurn struct {
	RequestID      string
	ConversationID string
	SessionID      string
	TurnID         string
	TeamID         string
	AgentID        string
	Protocol       string // anthropic_messages|chat_completions|responses
	Model          string
	Stream         bool
	SystemHint     string            // Original system/instructions content (injection point)
	Messages       []TurnMessage     // Normalized message list
	Tools          []json.RawMessage // Tool definitions (protocol-specific)
	Meta           map[string]any    // Protocol-specific fields preserved
	RawRequest     json.RawMessage   // Original request body for passthrough
}

// TurnMessage represents a normalized message across protocols
type TurnMessage struct {
	Role      string          // system|user|assistant|tool|developer
	Content   json.RawMessage // Content blocks (text/image/tool_call/tool_result)
	ToolCalls json.RawMessage // Tool calls (protocol-specific)
}

// OutboundTurn represents the upstream request to be sent
type OutboundTurn struct {
	RequestID   string
	TurnID      string
	Protocol    string
	Stream      bool
	UpstreamURL string
	UpstreamKey string // Retrieved from secrets service, never persisted
	// Non-streaming: full response
	Response json.RawMessage
	// Streaming: event-driven callback
	OnEvent func(TurnStreamEvent)
}

// TurnStreamEvent represents a streaming event
type TurnStreamEvent struct {
	Kind       string // delta | tool_call | done | error | cancelled
	Delta      string // Text increment
	ToolUse    any
	Done       bool
	StopReason string
	Err        error
}

// InjectionPackage represents memory injection content
type InjectionPackage struct {
	PathManifest    string   // Fixed path list placeholder (Phase 2)
	IdentityCard    string   // Identity card summary
	RetrievalSnips  []string // Retrieved snippets (Phase 3)
	SourceEventIDs  []string // Traceability
	ManifestVersion string
	Truncated       bool // Set if token budget exceeded
}
