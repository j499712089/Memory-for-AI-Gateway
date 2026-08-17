package adapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// UpstreamClient handles requests to upstream model services
type UpstreamClient struct {
	httpClient *http.Client
	timeout    time.Duration
	maxRetries int
}

// NewUpstreamClient creates a new upstream client
func NewUpstreamClient(timeout time.Duration) *UpstreamClient {
	return &UpstreamClient{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		timeout:    timeout,
		maxRetries: 3,
	}
}

// UpstreamRequest represents an upstream API request
type UpstreamRequest struct {
	URL         string
	Method      string
	Headers     map[string]string
	Body        []byte
	Stream      bool
	Protocol    string
}

// UpstreamResponse represents an upstream API response
type UpstreamResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	BodyStream io.ReadCloser
	Stream     bool
}

// Send sends a request to upstream with retry logic
func (c *UpstreamClient) Send(ctx context.Context, req *UpstreamRequest) (*UpstreamResponse, error) {
	var lastErr error

	for attempt := 0; attempt < c.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		resp, err := c.doRequest(ctx, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Don't retry on client errors (4xx)
		if resp != nil && resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return resp, err
		}
	}

	return nil, fmt.Errorf("upstream request failed after %d attempts: %w", c.maxRetries, lastErr)
}

// doRequest performs a single upstream request
func (c *UpstreamClient) doRequest(ctx context.Context, req *UpstreamRequest) (*UpstreamResponse, error) {
	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Set headers
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	// Default headers
	if httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// Send request
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	resp := &UpstreamResponse{
		StatusCode: httpResp.StatusCode,
		Headers:    httpResp.Header,
		Stream:     req.Stream,
	}

	// Handle streaming response
	if req.Stream {
		resp.BodyStream = httpResp.Body
		return resp, nil
	}

	// Handle non-streaming response
	defer httpResp.Body.Close()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return resp, fmt.Errorf("read response body: %w", err)
	}

	resp.Body = body

	// Check for HTTP errors
	if httpResp.StatusCode >= 400 {
		return resp, fmt.Errorf("upstream error: status %d, body: %s", httpResp.StatusCode, string(body))
	}

	return resp, nil
}

// BuildUpstreamHeaders builds headers for upstream request based on protocol
func BuildUpstreamHeaders(protocol, apiKey string) map[string]string {
	headers := make(map[string]string)

	switch protocol {
	case "anthropic_messages":
		headers["x-api-key"] = apiKey
		headers["anthropic-version"] = "2023-06-01"
	case "chat_completions", "responses":
		headers["Authorization"] = "Bearer " + apiKey
	}

	headers["Content-Type"] = "application/json"
	return headers
}
