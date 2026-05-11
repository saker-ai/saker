// HTTP fetching, redirect policy, response cache and host extraction
// helpers for WebFetchTool. Constants and the tool struct live in
// webfetch.go; host validation lives in webfetch_safety.go.
package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

func (w *WebFetchTool) fetch(ctx context.Context, normalized string) (*fetchResult, *redirectNotice, error) {
	if cached, ok := w.cache.Get(normalized); ok {
		clone := *cached
		clone.Body = append([]byte(nil), cached.Body...)
		clone.FromCache = true
		return &clone, nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", defaultFetchUserAgent)
	resp, err := w.client.Do(req)
	if err != nil {
		if notice := detectRedirectNotice(err); notice != nil {
			return nil, notice, nil
		}
		return nil, nil, fmt.Errorf("fetch url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, nil, fmt.Errorf("fetch failed with status %d", resp.StatusCode)
	}

	data, err := readBounded(resp.Body, w.maxBytes)
	if err != nil {
		return nil, nil, err
	}

	result := &fetchResult{
		URL:    resp.Request.URL.String(),
		Status: resp.StatusCode,
		Body:   data,
	}
	w.cache.Set(normalized, result)
	return result, nil, nil
}

type fetchResult struct {
	URL       string
	Status    int
	Body      []byte
	FromCache bool
}

type redirectNotice struct {
	URL string
}

func (w *WebFetchTool) redirectPolicy() func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxFetchRedirects {
			return fmt.Errorf("too many redirects")
		}
		if len(via) == 0 {
			return nil
		}
		original := hostWithoutPort(via[0].URL.Host)
		next := hostWithoutPort(req.URL.Host)
		if !strings.EqualFold(original, next) {
			return &hostRedirectError{target: req.URL.String()}
		}
		return nil
	}
}

type hostRedirectError struct {
	target string
}

func (e *hostRedirectError) Error() string {
	return fmt.Sprintf("redirected to disallowed host: %s", e.target)
}

func detectRedirectNotice(err error) *redirectNotice {
	var redirectErr *hostRedirectError
	if errors.As(err, &redirectErr) {
		return &redirectNotice{URL: redirectErr.target}
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if errors.As(urlErr.Err, &redirectErr) {
			return &redirectNotice{URL: redirectErr.target}
		}
	}
	return nil
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = defaultFetchMaxBytes
	}
	reader := io.LimitReader(r, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes limit", limit)
	}
	return data, nil
}

func cloneHTTPClient(c *http.Client) *http.Client {
	if c == nil {
		return &http.Client{}
	}
	clone := *c
	if clone.Transport == nil {
		clone.Transport = http.DefaultTransport
	}
	return &clone
}

type fetchCache struct {
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	expires time.Time
	result  fetchResult
}

func newFetchCache(ttl time.Duration) *fetchCache {
	return &fetchCache{
		ttl:     ttl,
		entries: make(map[string]cacheEntry),
	}
}

func (c *fetchCache) Get(key string) (*fetchResult, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeLocked()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expires) {
		if ok {
			delete(c.entries, key)
		}
		return nil, false
	}
	res := entry.result
	return &res, true
}

func (c *fetchCache) Set(key string, result *fetchResult) {
	if c == nil || result == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeLocked()
	clone := fetchResult{
		URL:    result.URL,
		Status: result.Status,
		Body:   append([]byte(nil), result.Body...),
	}
	c.entries[key] = cacheEntry{
		expires: time.Now().Add(c.ttl),
		result:  clone,
	}
}

func (c *fetchCache) purgeLocked() {
	if c == nil || len(c.entries) == 0 {
		return
	}
	now := time.Now()
	for k, v := range c.entries {
		if now.After(v.expires) {
			delete(c.entries, k)
		}
	}
}

func hostWithoutPort(hostport string) string {
	host := hostport
	if strings.Contains(hostport, ":") {
		if parsedHost, _, err := net.SplitHostPort(hostport); err == nil {
			host = parsedHost
		}
	}
	return host
}
