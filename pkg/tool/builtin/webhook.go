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

	"github.com/cinience/saker/pkg/tool"
)

// NewSSRFSafeClient returns an HTTP client hardened against SSRF attacks:
//   - Custom dialer that validates resolved IPs against blocked ranges (prevents DNS rebinding)
//   - CheckRedirect that validates each redirect target (prevents redirect-based SSRF)
//   - 30-second timeout
func NewSSRFSafeClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, fmt.Errorf("ssrf: invalid address %q: %w", addr, err)
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, fmt.Errorf("ssrf: dns lookup failed for %q: %w", host, err)
				}
				for _, ip := range ips {
					if isBlockedIP(ip.IP) {
						return nil, fmt.Errorf("ssrf: resolved IP %s for %q is in a blocked range", ip.IP, host)
					}
				}
				// Connect to the first valid IP to prevent TOCTOU via DNS rebinding.
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
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

// ssrfBlockedNets lists private/reserved IP ranges that webhooks must not reach.
var ssrfBlockedNets = func() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",    // loopback
		"10.0.0.0/8",     // RFC 1918
		"172.16.0.0/12",  // RFC 1918
		"192.168.0.0/16", // RFC 1918
		"169.254.0.0/16", // link-local
		"::1/128",        // IPv6 loopback
		"fc00::/7",       // IPv6 unique local
		"fe80::/10",      // IPv6 link-local
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, _ := net.ParseCIDR(c)
		nets = append(nets, n)
	}
	return nets
}()

// isBlockedHost resolves a hostname and returns true if any resolved IP falls
// within a private/reserved network range (SSRF protection).
// For hostnames that fail DNS resolution, returns false — the SSRF-safe HTTP
// client's DialContext performs a second IP validation at connect time, so
// legitimate hosts with transient DNS issues are not permanently blocked.
func isBlockedHost(hostname string) bool {
	// Strip port if present
	host := hostname
	if h, _, err := net.SplitHostPort(hostname); err == nil {
		host = h
	}

	// Check if it's a direct IP
	if ip := net.ParseIP(host); ip != nil {
		return isBlockedIP(ip)
	}

	// Resolve hostname — if DNS fails, allow through and let the SSRF-safe
	// client's DialContext catch blocked IPs at connect time.
	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return true
		}
	}
	return false
}

func isBlockedIP(ip net.IP) bool {
	for _, n := range ssrfBlockedNets {
		if n.Contains(ip) {
			return true
		}
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

	// SSRF protection: block requests to private/reserved networks
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("webhook: invalid url: %w", err)
	}
	if !w.AllowedHosts[parsed.Host] && isBlockedHost(parsed.Host) {
		return nil, fmt.Errorf("webhook: requests to private/internal networks are blocked")
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
		client = NewSSRFSafeClient()
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
