package adapter

import (
	"encoding/json"
	"fmt"
)

// ChatCompletionsRequest represents an OpenAI Chat Completions API request
type ChatCompletionsRequest struct {
	Model       string            `json:"model"`
	Messages    []ChatMessage     `json:"messages"`
	Tools       []json.RawMessage `json:"tools,omitempty"`
	ToolChoice  any               `json:"tool_choice,omitempty"`
	Stream      bool              `json:"stream,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
	MaxTokens   *int              `json:"max_tokens,omitempty"`
	TopP        *float64          `json:"top_p,omitempty"`
}

// ChatMessage represents a chat message
type ChatMessage struct {
	Role       string          `json:"role"` // system|user|assistant|tool
	Content    any             `json:"content,omitempty"` // string or array
	ToolCalls  []json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

// ParseChatCompletionsRequest parses a Chat Completions request
func ParseChatCompletionsRequest(body []byte) (*InboundTurn, error) {
	var req ChatCompletionsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("unmarshal chat request: %w", err)
	}

	turn := &InboundTurn{
		Protocol:   "chat_completions",
		Model:      req.Model,
		Stream:     req.Stream,
		Tools:      req.Tools,
		Meta:       make(map[string]any),
		RawRequest: body,
	}

	// Extract system hint from first message if it's system role
	if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
		if contentStr, ok := req.Messages[0].Content.(string); ok {
			turn.SystemHint = contentStr
		}
	}

	// Store messages
	turn.Messages = make([]TurnMessage, len(req.Messages))
	for i, msg := range req.Messages {
		contentJSON, _ := json.Marshal(msg.Content)
		turn.Messages[i] = TurnMessage{
			Role:    msg.Role,
			Content: contentJSON,
		}
	}

	// Store protocol-specific fields
	if req.Temperature != nil {
		turn.Meta["temperature"] = *req.Temperature
	}
	if req.ToolChoice != nil {
		turn.Meta["tool_choice"] = req.ToolChoice
	}

	return turn, nil
}

// InjectChatCompletionsSystem injects memory package at the head of messages array
// Preserves client's original system messages
func InjectChatCompletionsSystem(req *ChatCompletionsRequest, injectionText string) error {
	injectionMsg := ChatMessage{
		Role:    "system",
		Content: injectionText,
	}

	// Prepend injection message
	newMessages := append([]ChatMessage{injectionMsg}, req.Messages...)
	req.Messages = newMessages

	return nil
}

// BuildChatCompletionsUpstreamRequest builds the upstream request with injection
func BuildChatCompletionsUpstreamRequest(originalBody []byte, injectionText string) ([]byte, error) {
	var req ChatCompletionsRequest
	if err := json.Unmarshal(originalBody, &req); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	// Inject system message at head
	if err := InjectChatCompletionsSystem(&req, injectionText); err != nil {
		return nil, fmt.Errorf("inject system: %w", err)
	}

	// Marshal back to JSON
	return json.Marshal(req)
}
