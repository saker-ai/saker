package skillhub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func defaultTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 100
	t.MaxIdleConnsPerHost = 10
	t.IdleConnTimeout = 90 * time.Second
	return t
}

// Client is a thin HTTP client for the skillhub registry.
// Zero-value is not usable; use New.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	userAgent  string
}

// ClientOption tweaks a Client at construction time.
type ClientOption func(*Client)

// WithToken sets the bearer token.
func WithToken(t string) ClientOption { return func(c *Client) { c.token = t } }

// WithHTTPClient installs a custom underlying http.Client.
func WithHTTPClient(h *http.Client) ClientOption { return func(c *Client) { c.httpClient = h } }

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(s string) ClientOption { return func(c *Client) { c.userAgent = s } }

// New creates a Client. registry is the base URL (e.g. https://skillhub.saker.run).
func New(registry string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL:   strings.TrimRight(registry, "/"),
		userAgent: "saker-skillhub/0.1",
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: defaultTransport(),
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// BaseURL returns the configured registry root.
func (c *Client) BaseURL() string { return c.baseURL }

// Token returns the configured bearer token (may be empty).
func (c *Client) Token() string { return c.token }

// SetToken replaces the bearer token (used after device login).
func (c *Client) SetToken(t string) { c.token = t }

// APIError is returned for non-2xx responses carrying JSON {"error": "..."}.
type APIError struct {
	Status int
	Body   string
	Msg    string
}

func (e *APIError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("skillhub: %d %s", e.Status, e.Msg)
	}
	return fmt.Sprintf("skillhub: HTTP %d: %s", e.Status, truncate(e.Body, 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	u := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, method, path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("skillhub request: %w", err)
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func decodeResponse(resp *http.Response, out any) error {
	buf, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(buf, &errBody)
		return &APIError{Status: resp.StatusCode, Body: string(buf), Msg: errBody.Error}
	}
	if out != nil && len(buf) > 0 {
		if err := json.Unmarshal(buf, out); err != nil {
			return fmt.Errorf("decode response: %w (body=%s)", err, truncate(string(buf), 120))
		}
	}
	return nil
}

// WhoAmI returns the identity tied to the current token.
func (c *Client) WhoAmI(ctx context.Context) (*User, error) {
	var u User
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/whoami", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// Search runs a keyword search against /api/v1/search.
func (c *Client) Search(ctx context.Context, query string, limit int) (*SearchResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	u := fmt.Sprintf("/api/v1/search?q=%s&limit=%d", url.QueryEscape(query), limit)
	var out SearchResult
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// List returns a paginated skill list. category/cursor may be empty.
func (c *Client) List(ctx context.Context, category, sort, cursor string, limit int) (*ListResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	if sort != "" {
		q.Set("sort", sort)
	}
	if category != "" {
		q.Set("category", category)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var out ListResult
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/skills?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get returns metadata for a single skill.
func (c *Client) Get(ctx context.Context, slug string) (*Skill, error) {
	var s Skill
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/skills/"+url.PathEscape(slug), nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Versions returns all versions for a skill.
func (c *Client) Versions(ctx context.Context, slug string) ([]Version, error) {
	var out struct {
		Versions []Version `json:"versions"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/skills/"+url.PathEscape(slug)+"/versions", nil, &out); err != nil {
		return nil, err
	}
	return out.Versions, nil
}

// Download streams the zip archive for a skill version. Caller must close reader.
// etag, when non-empty, is sent as If-None-Match; if the server returns 304
// this function returns (nil, "", ErrNotModified).
func (c *Client) Download(ctx context.Context, slug, version, etag string) (io.ReadCloser, string, error) {
	q := url.Values{}
	q.Set("slug", slug)
	if version != "" {
		q.Set("version", version)
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/api/v1/download?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("skillhub download: %w", err)
	}
	if resp.StatusCode == http.StatusNotModified {
		resp.Body.Close()
		return nil, "", ErrNotModified
	}
	if resp.StatusCode >= 400 {
		buf, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		if err != nil {
			return nil, "", fmt.Errorf("skillhub search: read error body: %w", err)
		}
		resp.Body.Close()
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(buf, &errBody)
		return nil, "", &APIError{Status: resp.StatusCode, Body: string(buf), Msg: errBody.Error}
	}
	return resp.Body, resp.Header.Get("ETag"), nil
}

// ErrNotModified is returned by Download when an If-None-Match header matches.
var ErrNotModified = fmt.Errorf("not modified")

// Publish uploads a skill version via multipart form.
func (c *Client) Publish(ctx context.Context, req PublishRequest) (*PublishResponse, error) {
	if req.Slug == "" || req.Version == "" {
		return nil, fmt.Errorf("slug and version are required")
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	addField := func(name, value string) error {
		if value == "" {
			return nil
		}
		return mw.WriteField(name, value)
	}
	if err := addField("slug", req.Slug); err != nil {
		return nil, err
	}
	if err := addField("version", req.Version); err != nil {
		return nil, err
	}
	if err := addField("category", req.Category); err != nil {
		return nil, err
	}
	if err := addField("kind", req.Kind); err != nil {
		return nil, err
	}
	if err := addField("displayName", req.DisplayName); err != nil {
		return nil, err
	}
	if err := addField("summary", req.Summary); err != nil {
		return nil, err
	}
	if err := addField("changelog", req.Changelog); err != nil {
		return nil, err
	}
	if len(req.Tags) > 0 {
		if err := addField("tags", strings.Join(req.Tags, ",")); err != nil {
			return nil, err
		}
	}
	for path, content := range req.Files {
		part, err := mw.CreateFormFile("files", path)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(content); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/skills", &body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("skillhub publish: %w", err)
	}
	defer resp.Body.Close()
	var out PublishResponse
	if err := decodeResponse(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
