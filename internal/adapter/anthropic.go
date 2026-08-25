package adapter

import (
	"encoding/json"
	"fmt"
)

// AnthropicRequest represents an Anthropic Messages API request
type AnthropicRequest struct {
	Model         string            `json:"model"`
	MaxTokens     int               `json:"max_tokens"`
	System        json.RawMessage   `json:"system,omitempty"` // string or array
	Messages      []json.RawMessage `json:"messages"`
	Tools         []json.RawMessage `json:"tools,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	TopP          *float64          `json:"top_p,omitempty"`
	TopK          *int              `json:"top_k,omitempty"`
}

// AnthropicSystemBlock represents a system content block
type AnthropicSystemBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ParseAnthropicRequest parses an Anthropic Messages request
func ParseAnthropicRequest(body []byte) (*InboundTurn, error) {
	var req AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("unmarshal anthropic request: %w", err)
	}

	turn := &InboundTurn{
		Protocol:   "anthropic_messages",
		Model:      req.Model,
		Stream:     req.Stream,
		Tools:      req.Tools,
		Meta:       make(map[string]any),
		RawRequest: body,
	}

	// Extract system hint
	if len(req.System) > 0 {
		turn.SystemHint = string(req.System)
	}

	// Store original messages
	turn.Messages = make([]TurnMessage, len(req.Messages))
	for i, msg := range req.Messages {
		turn.Messages[i] = TurnMessage{
			Content: msg,
		}
	}

	// Store protocol-specific fields
	if req.Temperature != nil {
		turn.Meta["temperature"] = *req.Temperature
	}
	if req.StopSequences != nil {
		turn.Meta["stop_sequences"] = req.StopSequences
	}

	return turn, nil
}

// InjectAnthropicSystem injects memory package at the head of system array
// Preserves client's original system content
func InjectAnthropicSystem(req *AnthropicRequest, injectionText string) error {
	injectionBlock := AnthropicSystemBlock{
		Type: "text",
		Text: injectionText,
	}

	injectionJSON, err := json.Marshal(injectionBlock)
	if err != nil {
		return fmt.Errorf("marshal injection block: %w", err)
	}

	// Handle different system formats
	if len(req.System) == 0 || string(req.System) == "null" {
		// No existing system, create array with injection
		systemArray := []json.RawMessage{injectionJSON}
		req.System, err = json.Marshal(systemArray)
		return err
	}

	// Check if system is string or array
	var systemStr string
	if err := json.Unmarshal(req.System, &systemStr); err == nil {
		// System is a string, convert to array
		originalBlock := AnthropicSystemBlock{
			Type: "text",
			Text: systemStr,
		}
		originalJSON, err := json.Marshal(originalBlock)
		if err != nil {
			return fmt.Errorf("marshal original system: %w", err)
		}
		systemArray := []json.RawMessage{injectionJSON, originalJSON}
		req.System, err = json.Marshal(systemArray)
		return err
	}

	// System is already an array, prepend injection
	var systemArray []json.RawMessage
	if err := json.Unmarshal(req.System, &systemArray); err != nil {
		return fmt.Errorf("unmarshal system array: %w", err)
	}

	// Prepend injection block
	newSystemArray := append([]json.RawMessage{injectionJSON}, systemArray...)
	req.System, err = json.Marshal(newSystemArray)
	return err
}

// BuildAnthropicUpstreamRequest builds the upstream request with injection
func BuildAnthropicUpstreamRequest(originalBody []byte, injectionText string) ([]byte, error) {
	request, err := decodeRequestObject(originalBody)
	if err != nil {
		return nil, err
	}

	req := AnthropicRequest{System: request["system"]}
	if err := InjectAnthropicSystem(&req, injectionText); err != nil {
		return nil, fmt.Errorf("inject system: %w", err)
	}
	request["system"] = req.System

	return json.Marshal(request)
}
