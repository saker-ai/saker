// Package e2e provides end-to-end testing utilities for saker.
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RunRequest matches the HTTP server's runRequest struct.
type RunRequest struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
}

// RunResponse matches the HTTP server's runResponse struct.
type RunResponse struct {
	SessionID  string         `json:"session_id"`
	Output     string         `json:"output"`
	StopReason string         `json:"stop_reason"`
	Usage      map[string]any `json:"usage"`
	ToolCalls  []ToolCallInfo `json:"tool_calls"`
}

// ToolCallInfo captures tool call details from the response.
type ToolCallInfo struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// SSEEvent represents a single Server-Sent Event.
type SSEEvent struct {
	Event string
	Data  string
}

// StreamResult collects all events from an SSE stream.
type StreamResult struct {
	Events   []SSEEvent
	Final    *RunResponse
	RawText  string // concatenated text deltas
	Duration time.Duration
}

// Client wraps saker HTTP API calls.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient creates a new e2e client pointing at the given server URL.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
}

// Health checks the /health endpoint.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: status %d", resp.StatusCode)
	}
	return nil
}

// Run calls POST /v1/run synchronously and returns the response.
func (c *Client) Run(ctx context.Context, prompt, sessionID string) (*RunResponse, error) {
	body, err := json.Marshal(RunRequest{
		Prompt:    prompt,
		SessionID: sessionID,
		TimeoutMs: 300000, // 5 minutes
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/run", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("run: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("run: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("run: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result RunResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("run: unmarshal: %w", err)
	}
	return &result, nil
}

// RunStream calls POST /v1/run/stream and collects SSE events.
func (c *Client) RunStream(ctx context.Context, prompt, sessionID string) (*StreamResult, error) {
	start := time.Now()

	body, err := json.Marshal(RunRequest{
		Prompt:    prompt,
		SessionID: sessionID,
		TimeoutMs: 300000,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/run/stream", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("stream: status %d: %s", resp.StatusCode, string(respBody))
	}

	result := &StreamResult{}
	var textBuilder strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20) // 1MB buffer

	var currentEvent SSEEvent
	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = event boundary
			if currentEvent.Event != "" || currentEvent.Data != "" {
				result.Events = append(result.Events, currentEvent)

				// Extract text deltas
				if currentEvent.Event == "content_block_delta" {
					var delta struct {
						Delta struct {
							Text string `json:"text"`
						} `json:"delta"`
					}
					if json.Unmarshal([]byte(currentEvent.Data), &delta) == nil {
						textBuilder.WriteString(delta.Delta.Text)
					}
				}

				// Check for message_stop to extract final response
				if currentEvent.Event == "message_stop" {
					var final RunResponse
					if json.Unmarshal([]byte(currentEvent.Data), &final) == nil {
						result.Final = &final
					}
				}
			}
			currentEvent = SSEEvent{}
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			currentEvent.Event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			currentEvent.Data = strings.TrimPrefix(line, "data: ")
		}
	}

	result.RawText = textBuilder.String()
	result.Duration = time.Since(start)
	return result, scanner.Err()
}

// WaitForHealthy polls the health endpoint until the server is ready.
func (c *Client) WaitForHealthy(ctx context.Context, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("server not healthy after %v", timeout)
		case <-ticker.C:
			if err := c.Health(ctx); err == nil {
				return nil
			}
		}
	}
}
