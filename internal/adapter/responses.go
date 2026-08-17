package adapter

import (
	"encoding/json"
	"fmt"
)

// ResponsesRequest represents an OpenAI Responses API request
type ResponsesRequest struct {
	Model           string          `json:"model"`
	Instructions    string          `json:"instructions,omitempty"`
	Input           json.RawMessage `json:"input,omitempty"` // array or string
	Tools           []json.RawMessage `json:"tools,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	MaxOutputTokens *int            `json:"max_output_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
}

// ParseResponsesRequest parses a Responses request
func ParseResponsesRequest(body []byte) (*InboundTurn, error) {
	var req ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("unmarshal responses request: %w", err)
	}

	turn := &InboundTurn{
		Protocol:   "responses",
		Model:      req.Model,
		Stream:     req.Stream,
		SystemHint: req.Instructions,
		Tools:      req.Tools,
		Meta:       make(map[string]any),
		RawRequest: body,
	}

	// Store input as messages
	if len(req.Input) > 0 {
		turn.Messages = []TurnMessage{{Content: req.Input}}
	}

	// Store protocol-specific fields
	if req.Temperature != nil {
		turn.Meta["temperature"] = *req.Temperature
	}
	if req.MaxOutputTokens != nil {
		turn.Meta["max_output_tokens"] = *req.MaxOutputTokens
	}

	return turn, nil
}

// InjectResponsesInstructions injects memory package at the head of instructions
// Preserves client's original instructions
func InjectResponsesInstructions(req *ResponsesRequest, injectionText string) error {
	if req.Instructions == "" {
		// No existing instructions, use injection as instructions
		req.Instructions = injectionText
	} else {
		// Prepend injection to existing instructions
		req.Instructions = injectionText + "\n\n" + req.Instructions
	}

	// If no instructions but has input array, inject as first system message in input
	if req.Instructions == "" && len(req.Input) > 0 {
		// Try to parse input as array
		var inputArray []json.RawMessage
		if err := json.Unmarshal(req.Input, &inputArray); err == nil {
			// Input is an array, prepend system message
			systemMsg := map[string]any{
				"role": "system",
				"content": []map[string]string{
					{"type": "input_text", "text": injectionText},
				},
			}
			systemJSON, err := json.Marshal(systemMsg)
			if err != nil {
				return fmt.Errorf("marshal system message: %w", err)
			}

			newInput := append([]json.RawMessage{systemJSON}, inputArray...)
			req.Input, err = json.Marshal(newInput)
			return err
		}
	}

	return nil
}

// BuildResponsesUpstreamRequest builds the upstream request with injection
func BuildResponsesUpstreamRequest(originalBody []byte, injectionText string) ([]byte, error) {
	var req ResponsesRequest
	if err := json.Unmarshal(originalBody, &req); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	// Inject instructions
	if err := InjectResponsesInstructions(&req, injectionText); err != nil {
		return nil, fmt.Errorf("inject instructions: %w", err)
	}

	// Marshal back to JSON
	return json.Marshal(req)
}
