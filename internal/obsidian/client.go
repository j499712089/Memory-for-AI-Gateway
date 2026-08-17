package obsidian

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func NewClient(baseURL, token string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "http://127.0.0.1:27124"
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTPClient: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) Health(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/", nil, "")
	return err
}

func (c *Client) GetMarkdown(ctx context.Context, vaultPath string) ([]byte, error) {
	encoded, err := encodeVaultPath(vaultPath)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodGet, "/vault/"+encoded, nil, "")
}

func (c *Client) PutMarkdown(ctx context.Context, vaultPath string, content []byte) error {
	encoded, err := encodeVaultPath(vaultPath)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPut, "/vault/"+encoded, content, "text/markdown; charset=utf-8")
	return err
}

func encodeVaultPath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.HasPrefix(value, "/") || path.IsAbs(value) {
		return "", fmt.Errorf("vault path must be relative")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("vault path escapes vault")
	}
	parts := strings.Split(clean, "/")
	encoded := make([]string, len(parts))
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid vault path segment")
		}
		encoded[i] = url.PathEscape(part)
	}
	return strings.Join(encoded, "/"), nil
}

func (c *Client) do(ctx context.Context, method, endpoint string, body []byte, contentType string) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("obsidian client is nil")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return nil, fmt.Errorf("obsidian base URL is empty")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("obsidian request: %w", err)
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return nil, readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("obsidian returned %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}
