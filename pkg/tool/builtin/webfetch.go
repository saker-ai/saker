// WebFetch tool entry-point: schema, constructor and Tool interface
// implementation. Fetching, caching, host validation and HTML→Markdown
// conversion live in sibling files webfetch_fetch.go, webfetch_safety.go
// and webfetch_extract.go.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
)

const (
	webFetchDescription = `
	- Fetches content from a specified URL and processes it using an AI model
	- Takes a URL and a prompt as input
	- Fetches the URL content, converts HTML to markdown
	- Processes the content with the prompt using a small, fast model
	- Returns the model's response about the content
	- Use this tool when you need to retrieve and analyze web content

		Usage notes:
			- IMPORTANT: If an MCP-provided web fetch tool is available, prefer using that tool instead of this one, as it may have fewer restrictions. MCP-provided tools are named as \"{serverName}__{toolName}\" format.
			- The URL must be a fully-formed valid URL
			- HTTP URLs will be automatically upgraded to HTTPS
			- The prompt should describe what information you want to extract from the page
			- This tool is read-only and does not modify any files
			- Results may be summarized if the content is very large
		- Includes a self-cleaning 15-minute cache for faster responses when repeatedly accessing the same URL
    - When a URL redirects to a different host, the tool will inform you and provide the redirect URL in a special format. You should then make a new WebFetch request with the redirect URL to fetch the content.

	`

	defaultFetchTimeout     = 15 * time.Second
	maxFetchTimeout         = 60 * time.Second
	defaultFetchCacheTTL    = 15 * time.Minute
	defaultFetchMaxBytes    = 2 << 20 // 2 MiB
	maxFetchRedirects       = 5
	defaultFetchUserAgent   = "saker-webfetch/1.0"
	redirectNoticePrefix    = "redirect://"
	markdownSnippetMaxLines = 12
)

var webFetchSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"url": map[string]interface{}{
			"type":        "string",
			"format":      "uri",
			"description": "The URL to fetch content from",
		},
		"prompt": map[string]interface{}{
			"type":        "string",
			"description": "The prompt to run on the fetched content",
		},
	},
	Required: []string{"url", "prompt"},
}

// WebFetchOptions configures WebFetchTool behaviour.
type WebFetchOptions struct {
	HTTPClient        *http.Client
	Timeout           time.Duration
	CacheTTL          time.Duration
	MaxContentSize    int64
	AllowedHosts      []string
	BlockedHosts      []string
	AllowPrivateHosts bool
}

// WebFetchTool fetches remote web pages and returns Markdown content.
type WebFetchTool struct {
	client    *http.Client
	timeout   time.Duration
	maxBytes  int64
	cache     *fetchCache
	validator hostValidator
	now       func() time.Time
}

// NewWebFetchTool builds a WebFetchTool with sane defaults.
func NewWebFetchTool(opts *WebFetchOptions) *WebFetchTool {
	cfg := WebFetchOptions{}
	if opts != nil {
		cfg = *opts
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	if timeout > maxFetchTimeout {
		timeout = maxFetchTimeout
	}
	maxBytes := cfg.MaxContentSize
	if maxBytes <= 0 {
		maxBytes = defaultFetchMaxBytes
	}
	cacheTTL := cfg.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = defaultFetchCacheTTL
	}

	client := cloneHTTPClient(cfg.HTTPClient)
	client.Timeout = timeout

	tool := &WebFetchTool{
		client:    client,
		timeout:   timeout,
		maxBytes:  maxBytes,
		cache:     newFetchCache(cacheTTL),
		validator: newHostValidator(cfg.AllowedHosts, cfg.BlockedHosts, cfg.AllowPrivateHosts),
		now:       time.Now,
	}
	tool.client.CheckRedirect = tool.redirectPolicy()
	return tool
}

func (w *WebFetchTool) Name() string { return "WebFetch" }

func (w *WebFetchTool) Description() string { return webFetchDescription }

func (w *WebFetchTool) Schema() *tool.JSONSchema { return webFetchSchema }

// Execute fetches the requested URL, converts it to Markdown and returns metadata.
func (w *WebFetchTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if w == nil || w.client == nil {
		return nil, errors.New("web fetch tool is not initialised")
	}
	if params == nil {
		return nil, errors.New("params is nil")
	}

	rawURL, err := w.extractURL(params)
	if err != nil {
		return nil, err
	}
	prompt, err := extractNonEmptyString(params, "prompt")
	if err != nil {
		return nil, err
	}

	normalized, err := w.normaliseURL(rawURL)
	if err != nil {
		return nil, err
	}

	// SSRF pre-flight: resolve DNS and block private/reserved IPs.
	// The result contains validated IPs for connection pinning (TOCTOU mitigation).
	if parsed, pErr := url.Parse(normalized); pErr == nil {
		ssrfResult, ssrfErr := w.checkSSRF(ctx, parsed.Hostname())
		if ssrfErr != nil {
			return nil, ssrfErr
		}
		// Pin the HTTP transport to validated IPs to prevent DNS rebinding.
		if ssrfResult != nil && len(ssrfResult.ResolvedIPs) > 0 {
			port := parsed.Port()
			if port == "" {
				port = "443"
			}
			pinned := security.NewSSRFSafeDialer(ssrfResult, port)
			if pinned != nil {
				transport := w.client.Transport
				if transport == nil {
					transport = http.DefaultTransport
				}
				if base, ok := transport.(*http.Transport); ok {
					clone := base.Clone()
					clone.DialContext = pinned
					w.client.Transport = clone
				}
			}
		}
	}

	reqCtx := ctx
	var cancel context.CancelFunc
	if w.timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, w.timeout)
		defer cancel()
	}

	fetched, notice, err := w.fetch(reqCtx, normalized)
	if err != nil {
		return nil, err
	}
	if notice != nil {
		return &tool.ToolResult{
			Success: false,
			Output:  redirectNoticePrefix + notice.URL,
			Data: map[string]interface{}{
				"redirect_url": notice.URL,
				"reason":       "cross-host redirect",
			},
		}, nil
	}

	markdown := htmlToMarkdown(string(fetched.Body))
	snippet := summariseMarkdown(markdown)

	result := &tool.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Fetched %s (%d bytes)\n%s", fetched.URL, len(fetched.Body), snippet),
		Data: map[string]interface{}{
			"url":              fetched.URL,
			"requested_url":    normalized,
			"status":           fetched.Status,
			"content_markdown": markdown,
			"prompt":           prompt,
			"from_cache":       fetched.FromCache,
			"fetched_at":       w.now().UTC().Format(time.RFC3339),
			"content_bytes":    len(fetched.Body),
		},
	}
	return result, nil
}

func (w *WebFetchTool) extractURL(params map[string]interface{}) (string, error) {
	raw, ok := params["url"]
	if !ok {
		return "", errors.New("url is required")
	}
	value, err := stringValue(raw)
	if err != nil {
		return "", fmt.Errorf("url must be string: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("url cannot be empty")
	}
	return value, nil
}

func (w *WebFetchTool) normaliseURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		return "", errors.New("url scheme is required")
	}
	switch scheme {
	case "http":
		parsed.Scheme = "https"
	case "https":
	default:
		return "", fmt.Errorf("unsupported url scheme %s", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", errors.New("url host is required")
	}
	parsed.User = nil
	parsed.Fragment = ""
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	host := parsed.Hostname()
	if err := w.validator.Validate(host); err != nil {
		return "", err
	}
	return parsed.String(), nil
}

// checkSSRF performs pre-flight DNS resolution to block requests to private IPs.
// Returns validated IPs for connection pinning (TOCTOU mitigation).
func (w *WebFetchTool) checkSSRF(ctx context.Context, host string) (*security.SSRFResult, error) {
	if w.validator.allowPrivate {
		return nil, nil // user explicitly allowed private hosts
	}
	return security.CheckSSRF(ctx, host)
}

func stringValue(value interface{}) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case fmt.Stringer:
		return v.String(), nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("expected string got %T", value)
	}
}
