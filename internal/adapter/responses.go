package adapter

import (
	"encoding/json"
	"fmt"
)

// ResponsesRequest represents an OpenAI Responses API request
type ResponsesRequest struct {
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions,omitempty"`
	Input           json.RawMessage   `json:"input,omitempty"` // array or string
	Tools           []json.RawMessage `json:"tools,omitempty"`
	Stream          bool              `json:"stream,omitempty"`
	MaxOutputTokens *int              `json:"max_output_tokens,omitempty"`
	Temperature     *float64          `json:"temperature,omitempty"`
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
	if req.Instructions != "" {
		// Prepend injection to existing instructions
		req.Instructions = injectionText + "\n\n" + req.Instructions
		return nil
	}

	// With no instructions, preserve the client's input and insert a system
	// message at the start of an input array.
	if len(req.Input) > 0 {
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

	// String input has no system-message slot, so use the protocol's
	// instructions field without changing the client's input representation.
	req.Instructions = injectionText

	return nil
}

// BuildResponsesUpstreamRequest builds the upstream request with injection
func BuildResponsesUpstreamRequest(originalBody []byte, injectionText string) ([]byte, error) {
	request, err := decodeRequestObject(originalBody)
	if err != nil {
		return nil, err
	}

	if instructionsJSON, ok := request["instructions"]; ok && string(instructionsJSON) != "null" {
		var instructions string
		if err := json.Unmarshal(instructionsJSON, &instructions); err != nil {
			return nil, fmt.Errorf("unmarshal instructions: %w", err)
		}
		if instructions != "" {
			request["instructions"], err = json.Marshal(injectionText + "\n\n" + instructions)
			if err != nil {
				return nil, fmt.Errorf("marshal instructions: %w", err)
			}
			return json.Marshal(request)
		}
	}

	if inputJSON, ok := request["input"]; ok {
		var input []json.RawMessage
		if err := json.Unmarshal(inputJSON, &input); err == nil {
			systemJSON, marshalErr := json.Marshal(map[string]any{
				"role":    "system",
				"content": []map[string]string{{"type": "input_text", "text": injectionText}},
			})
			if marshalErr != nil {
				return nil, fmt.Errorf("marshal system input: %w", marshalErr)
			}
			request["input"], marshalErr = json.Marshal(append([]json.RawMessage{systemJSON}, input...))
			if marshalErr != nil {
				return nil, fmt.Errorf("marshal input: %w", marshalErr)
			}
			return json.Marshal(request)
		}
	}

	request["instructions"], err = json.Marshal(injectionText)
	if err != nil {
		return nil, fmt.Errorf("inject instructions: %w", err)
	}

	return json.Marshal(request)
}
