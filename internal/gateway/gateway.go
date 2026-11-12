// Package gateway talks to the OpenAI-compatible Qwen subscription gateway.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// BaseURL is the OpenAI-compatible endpoint of the Qwen subscription.
const BaseURL = "https://api.qwen.ai/compatible-mode/v1"

// Model describes one entry of the gateway model catalog.
type Model struct {
	ID                  string   `json:"id"`
	InputModalities     []string `json:"input_modalities,omitempty"`
	ContextWindowTokens int      `json:"context_window_tokens,omitempty"`
	Created             int64    `json:"created,omitempty"`
}

// Catalog is the gateway model list.
type Catalog struct {
	Data []Model `json:"data"`
}

// Client performs gateway requests.
type Client struct {
	HTTP    *http.Client
	UserAge string
	// BaseURL overrides the gateway origin. Empty means the production
	// endpoint; tests point it at a local server.
	BaseURL string
}

// NewClient returns a Client whose transport has no overall request timeout:
// chat completions may stream for minutes. Idle and header timeouts still
// apply so dead connections are detected.
func NewClient(userAgent string) *Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second,
	}
	return &Client{
		HTTP:    &http.Client{Transport: transport},
		UserAge: userAgent,
	}
}

// base returns the effective gateway origin.
func (c *Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return BaseURL
}

// Do sends a request to the gateway and returns the raw response so the
// caller can stream the body without buffering it.
func (c *Client) Do(ctx context.Context, method, path, accessToken string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAge)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	return c.HTTP.Do(req)
}

// Models fetches the gateway model catalog.
func (c *Client) Models(ctx context.Context, accessToken string) (*Catalog, error) {
	resp, err := c.Do(ctx, http.MethodGet, "/models", accessToken, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qwen gateway: models: status %d: %s", resp.StatusCode, truncate(data))
	}
	var catalog Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("qwen gateway: decode models: %w", err)
	}
	return &catalog, nil
}

func truncate(data []byte) string {
	const max = 400
	if len(data) <= max {
		return string(data)
	}
	return string(data[:max]) + "..."
}
