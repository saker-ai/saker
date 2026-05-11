package toolbuiltin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
)

// NewSSRFSafeClient returns an HTTP client hardened against SSRF attacks
// when the target URL is not yet known. It rejects connections to private
// networks AND fails closed on DNS resolution errors.
//
// IMPORTANT: this client still performs an extra DNS lookup at dial time
// independent of any earlier hostname check, so it remains theoretically
// vulnerable to DNS rebinding. Prefer newSSRFPinnedClient(host, port) when
// the destination URL is known up-front (the typical webhook / stream
// monitor case) — that path pins the connection to the validated IP set.
func NewSSRFSafeClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, fmt.Errorf("ssrf: invalid address %q: %w", addr, err)
				}
				result, err := security.CheckSSRF(ctx, host)
				if err != nil {
					return nil, err
				}
				pinned := security.NewSSRFSafeDialer(result, port)
				if pinned == nil {
					return nil, fmt.Errorf("ssrf: no resolved IPs for %q", host)
				}
				return pinned(ctx, network, addr)
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("ssrf: too many redirects")
			}
			if isBlockedHost(req.URL.Hostname()) {
				return fmt.Errorf("ssrf: redirect to blocked host %q", req.URL.Hostname())
			}
			return nil
		},
	}
}

// newSSRFPinnedClient builds a one-shot HTTP client that only ever connects
// to the supplied pre-validated IP set. The validation and the dial use the
// SAME IPs, eliminating the DNS rebinding window present in NewSSRFSafeClient.
func newSSRFPinnedClient(result *security.SSRFResult, port string) *http.Client {
	dial := security.NewSSRFSafeDialer(result, port)
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if dial == nil {
					return nil, fmt.Errorf("ssrf: empty resolved IP set")
				}
				return dial(ctx, network, addr)
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("ssrf: too many redirects")
			}
			// Re-validate the redirect target since it may differ from the
			// pinned host. CheckSSRF is fail-closed.
			if _, err := security.CheckSSRF(req.Context(), req.URL.Hostname()); err != nil {
				return fmt.Errorf("ssrf: redirect blocked: %w", err)
			}
			return nil
		},
	}
}

// defaultPortForScheme returns the default port for the given URL scheme.
func defaultPortForScheme(scheme string) string {
	switch strings.ToLower(scheme) {
	case "https", "wss":
		return "443"
	default:
		return "80"
	}
}

// isBlockedHost resolves a hostname and returns true if any resolved IP falls
// within a private/reserved network range. **Fail-closed**: returns true on
// DNS lookup failure so callers cannot bypass SSRF protection by pointing at
// an attacker-controlled DNS server that returns NXDOMAIN.
//
// This helper is kept for backwards compatibility (stream monitor, webhook
// redirect check). Prefer security.CheckSSRF directly which also returns the
// validated IP set for connection pinning.
func isBlockedHost(hostname string) bool {
	host := hostname
	if h, _, err := net.SplitHostPort(hostname); err == nil {
		host = h
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := security.CheckSSRF(ctx, host); err != nil {
		return true
	}
	return false
}

const webhookDescription = `Sends an HTTP webhook request to a specified URL.

Use this tool to notify external services of events, trigger automations,
or send alerts. Supports custom HTTP methods, headers, and JSON body.`

var webhookSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"url": map[string]any{
			"type":        "string",
			"description": "The webhook URL to send the request to",
		},
		"method": map[string]any{
			"type":        "string",
			"description": "HTTP method (default: POST)",
			"enum":        []string{"GET", "POST", "PUT", "PATCH"},
		},
		"headers": map[string]any{
			"type":        "object",
			"description": "Optional HTTP headers as key-value pairs",
		},
		"body": map[string]any{
			"type":        "object",
			"description": "JSON body to send with the request",
		},
	},
	Required: []string{"url"},
}

// WebhookTool sends HTTP webhook requests to external services.
type WebhookTool struct {
	// Client is the HTTP client used for requests. If nil, a default client
	// with a 30-second timeout is used.
	Client *http.Client

	// AllowedHosts bypasses SSRF checks for specific hosts (e.g. test servers).
	AllowedHosts map[string]bool
}

// NewWebhookTool creates a new webhook tool.
func NewWebhookTool() *WebhookTool { return &WebhookTool{} }

func (w *WebhookTool) Name() string             { return "webhook" }
func (w *WebhookTool) Description() string      { return webhookDescription }
func (w *WebhookTool) Schema() *tool.JSONSchema { return webhookSchema }

func (w *WebhookTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	urlStr, _ := params["url"].(string)
	urlStr = strings.TrimSpace(urlStr)
	if urlStr == "" {
		return nil, fmt.Errorf("webhook: url is required")
	}
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		return nil, fmt.Errorf("webhook: url must start with http:// or https://")
	}

	// SSRF protection: block requests to private/reserved networks AND pin
	// the connection to the validated IPs to defeat DNS rebinding.
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("webhook: invalid url: %w", err)
	}

	var pinnedClient *http.Client
	if !w.AllowedHosts[parsed.Host] {
		ssrfResult, err := security.CheckSSRF(ctx, parsed.Host)
		if err != nil {
			return nil, fmt.Errorf("webhook: %w", err)
		}
		port := parsed.Port()
		if port == "" {
			port = defaultPortForScheme(parsed.Scheme)
		}
		pinnedClient = newSSRFPinnedClient(ssrfResult, port)
	}

	method := "POST"
	if m, ok := params["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	var bodyReader io.Reader
	if body, ok := params["body"]; ok && body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("webhook: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("webhook: create request: %w", err)
	}
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if headers, ok := params["headers"].(map[string]any); ok {
		for k, v := range headers {
			if vs, ok := v.(string); ok {
				req.Header.Set(k, vs)
			}
		}
	}

	client := w.Client
	if client == nil {
		if pinnedClient != nil {
			client = pinnedClient
		} else {
			client = NewSSRFSafeClient()
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webhook: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	output := fmt.Sprintf("Webhook %s %s → %d %s", method, urlStr, resp.StatusCode, resp.Status)
	if len(respBody) > 0 {
		output += "\n" + string(respBody)
	}

	return &tool.ToolResult{
		Success: resp.StatusCode >= 200 && resp.StatusCode < 300,
		Output:  output,
	}, nil
}
