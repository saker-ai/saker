package model

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimitBucket represents a single rate limit bucket (e.g., requests/min).
type RateLimitBucket struct {
	Limit     int     `json:"limit"`
	Remaining int     `json:"remaining"`
	ResetSecs float64 `json:"reset_secs"`
}

// RateLimitInfo holds rate limit information from an API response.
type RateLimitInfo struct {
	RequestsPerMin RateLimitBucket `json:"requests_per_min"`
	TokensPerMin   RateLimitBucket `json:"tokens_per_min"`
	TokensPerDay   RateLimitBucket `json:"tokens_per_day"`
	CapturedAt     time.Time       `json:"captured_at"`
	Provider       string          `json:"provider"`
}

// rateLimitStore holds the most recent rate limit info per provider,
// safe for concurrent access. Keyed by provider name so multi-provider
// setups don't overwrite each other's data.
var rateLimitStore struct {
	mu   sync.RWMutex
	info map[string]*RateLimitInfo
}

func init() {
	rateLimitStore.info = make(map[string]*RateLimitInfo)
}

// GetRateLimitInfo returns the most recently captured rate limit info for
// the given provider, or nil. If provider is empty, returns info for the
// last provider that wrote data (backward compatible).
func GetRateLimitInfo(provider string) *RateLimitInfo {
	rateLimitStore.mu.RLock()
	defer rateLimitStore.mu.RUnlock()
	if provider != "" {
		if v, ok := rateLimitStore.info[provider]; ok {
			cp := *v
			return &cp
		}
		return nil
	}
	// Backward compatible: return any available info.
	for _, v := range rateLimitStore.info {
		cp := *v
		return &cp
	}
	return nil
}

// updateRateLimitInfo stores new rate limit info keyed by provider.
func updateRateLimitInfo(info *RateLimitInfo) {
	if info == nil {
		return
	}
	rateLimitStore.mu.Lock()
	rateLimitStore.info[info.Provider] = info
	rateLimitStore.mu.Unlock()
}

// ParseAnthropicRateLimits extracts rate limit info from Anthropic API response headers.
// Header format: x-ratelimit-{bucket}-{field}
// Buckets: requests, input-tokens, output-tokens, tokens
// Fields: limit, remaining, reset
func ParseAnthropicRateLimits(headers http.Header) *RateLimitInfo {
	if headers == nil {
		return nil
	}

	info := &RateLimitInfo{
		CapturedAt: time.Now(),
		Provider:   "anthropic",
	}

	hasData := false

	// Request limits.
	if v := headerInt(headers, "x-ratelimit-requests-limit"); v > 0 {
		info.RequestsPerMin.Limit = v
		info.RequestsPerMin.Remaining = headerInt(headers, "x-ratelimit-requests-remaining")
		info.RequestsPerMin.ResetSecs = parseResetToSecs(headers.Get("x-ratelimit-requests-reset"))
		hasData = true
	}

	// Token limits (input tokens as primary).
	if v := headerInt(headers, "x-ratelimit-input-tokens-limit"); v > 0 {
		info.TokensPerMin.Limit = v
		info.TokensPerMin.Remaining = headerInt(headers, "x-ratelimit-input-tokens-remaining")
		info.TokensPerMin.ResetSecs = parseResetToSecs(headers.Get("x-ratelimit-input-tokens-reset"))
		hasData = true
	} else if v := headerInt(headers, "x-ratelimit-tokens-limit"); v > 0 {
		// Fallback to generic tokens bucket.
		info.TokensPerMin.Limit = v
		info.TokensPerMin.Remaining = headerInt(headers, "x-ratelimit-tokens-remaining")
		info.TokensPerMin.ResetSecs = parseResetToSecs(headers.Get("x-ratelimit-tokens-reset"))
		hasData = true
	}

	// Output token limits → map to TokensPerDay as a secondary bucket.
	if v := headerInt(headers, "x-ratelimit-output-tokens-limit"); v > 0 {
		info.TokensPerDay.Limit = v
		info.TokensPerDay.Remaining = headerInt(headers, "x-ratelimit-output-tokens-remaining")
		info.TokensPerDay.ResetSecs = parseResetToSecs(headers.Get("x-ratelimit-output-tokens-reset"))
		hasData = true
	}

	if !hasData {
		return nil
	}
	return info
}

// headerInt extracts an integer from a header value.
func headerInt(h http.Header, key string) int {
	v := h.Get(key)
	if v == "" {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

// parseResetToSecs parses a reset timestamp (RFC3339) or duration and returns
// seconds until reset. Returns 0 on parse failure.
func parseResetToSecs(val string) float64 {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0
	}

	// Try RFC3339 timestamp first.
	if t, err := time.Parse(time.RFC3339, val); err == nil {
		secs := time.Until(t).Seconds()
		if secs < 0 {
			return 0
		}
		return secs
	}

	// Try plain seconds.
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		return f
	}

	return 0
}

// RateLimitCapturingTransport wraps an http.RoundTripper to capture rate limit
// headers from responses.
type RateLimitCapturingTransport struct {
	Base http.RoundTripper
}

// RoundTrip executes the HTTP request and captures rate limit headers.
func (t *RateLimitCapturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// Capture rate limit headers in the background.
	if info := ParseAnthropicRateLimits(resp.Header); info != nil {
		updateRateLimitInfo(info)
	}

	return resp, nil
}
