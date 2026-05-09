package model

import (
	"net/http"
	"testing"
	"time"
)

func TestParseAnthropicRateLimits(t *testing.T) {
	t.Parallel()

	t.Run("full headers", func(t *testing.T) {
		t.Parallel()
		h := http.Header{}
		h.Set("x-ratelimit-requests-limit", "60")
		h.Set("x-ratelimit-requests-remaining", "55")
		h.Set("x-ratelimit-requests-reset", "30.5")
		h.Set("x-ratelimit-input-tokens-limit", "100000")
		h.Set("x-ratelimit-input-tokens-remaining", "95000")
		h.Set("x-ratelimit-input-tokens-reset", "60")
		h.Set("x-ratelimit-output-tokens-limit", "50000")
		h.Set("x-ratelimit-output-tokens-remaining", "48000")

		info := ParseAnthropicRateLimits(h)
		if info == nil {
			t.Fatal("expected non-nil info")
		}
		if info.RequestsPerMin.Limit != 60 {
			t.Errorf("requests limit = %d, want 60", info.RequestsPerMin.Limit)
		}
		if info.RequestsPerMin.Remaining != 55 {
			t.Errorf("requests remaining = %d, want 55", info.RequestsPerMin.Remaining)
		}
		if info.TokensPerMin.Limit != 100000 {
			t.Errorf("tokens limit = %d, want 100000", info.TokensPerMin.Limit)
		}
		if info.TokensPerDay.Limit != 50000 {
			t.Errorf("output tokens limit = %d, want 50000", info.TokensPerDay.Limit)
		}
		if info.Provider != "anthropic" {
			t.Errorf("provider = %q, want %q", info.Provider, "anthropic")
		}
	})

	t.Run("nil headers", func(t *testing.T) {
		t.Parallel()
		info := ParseAnthropicRateLimits(nil)
		if info != nil {
			t.Error("expected nil for nil headers")
		}
	})

	t.Run("empty headers", func(t *testing.T) {
		t.Parallel()
		info := ParseAnthropicRateLimits(http.Header{})
		if info != nil {
			t.Error("expected nil for empty headers")
		}
	})

	t.Run("generic tokens fallback", func(t *testing.T) {
		t.Parallel()
		h := http.Header{}
		h.Set("x-ratelimit-tokens-limit", "80000")
		h.Set("x-ratelimit-tokens-remaining", "70000")

		info := ParseAnthropicRateLimits(h)
		if info == nil {
			t.Fatal("expected non-nil info")
		}
		if info.TokensPerMin.Limit != 80000 {
			t.Errorf("tokens limit = %d, want 80000", info.TokensPerMin.Limit)
		}
	})
}

func TestParseResetToSecs(t *testing.T) {
	t.Parallel()

	t.Run("plain seconds", func(t *testing.T) {
		t.Parallel()
		got := parseResetToSecs("30.5")
		if got != 30.5 {
			t.Errorf("parseResetToSecs(\"30.5\") = %v, want 30.5", got)
		}
	})

	t.Run("RFC3339 future", func(t *testing.T) {
		t.Parallel()
		future := time.Now().Add(60 * time.Second).Format(time.RFC3339)
		got := parseResetToSecs(future)
		if got < 50 || got > 70 {
			t.Errorf("parseResetToSecs(future) = %v, want ~60", got)
		}
	})

	t.Run("empty string", func(t *testing.T) {
		t.Parallel()
		got := parseResetToSecs("")
		if got != 0 {
			t.Errorf("parseResetToSecs(\"\") = %v, want 0", got)
		}
	})

	t.Run("past timestamp", func(t *testing.T) {
		t.Parallel()
		past := time.Now().Add(-60 * time.Second).Format(time.RFC3339)
		got := parseResetToSecs(past)
		if got != 0 {
			t.Errorf("parseResetToSecs(past) = %v, want 0", got)
		}
	})
}

func TestRateLimitStore(t *testing.T) {
	t.Parallel()

	// Initially nil.
	info := GetRateLimitInfo()
	if info != nil {
		t.Error("expected nil initially")
	}

	// Store some info.
	testInfo := &RateLimitInfo{
		Provider:   "test",
		CapturedAt: time.Now(),
		RequestsPerMin: RateLimitBucket{
			Limit:     100,
			Remaining: 90,
		},
	}
	updateRateLimitInfo(testInfo)

	got := GetRateLimitInfo()
	if got == nil {
		t.Fatal("expected non-nil after update")
	}
	if got.RequestsPerMin.Limit != 100 {
		t.Errorf("limit = %d, want 100", got.RequestsPerMin.Limit)
	}

	// Returned value should be a copy.
	got.RequestsPerMin.Limit = 999
	got2 := GetRateLimitInfo()
	if got2.RequestsPerMin.Limit != 100 {
		t.Error("store should return copies, not references")
	}
}
