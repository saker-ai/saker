package model

import (
	"errors"
	"fmt"
	"net"
	"testing"
)

// mockHTTPError simulates an SDK error with HTTP status code.
type mockHTTPError struct {
	code int
	msg  string
}

func (e *mockHTTPError) Error() string       { return e.msg }
func (e *mockHTTPError) HTTPStatusCode() int { return e.code }

// mockNetError simulates a network timeout error.
type mockNetError struct{ msg string }

func (e *mockNetError) Error() string   { return e.msg }
func (e *mockNetError) Timeout() bool   { return true }
func (e *mockNetError) Temporary() bool { return true }

// Ensure mockNetError satisfies net.Error.
var _ net.Error = (*mockNetError)(nil)

func TestClassifyError_Nil(t *testing.T) {
	c := ClassifyError(nil)
	if c.Reason != FailoverUnknown {
		t.Errorf("nil error: got %s, want unknown", c.Reason)
	}
}

func TestClassifyError_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		code       int
		msg        string
		wantReason FailoverReason
		retryable  bool
		fallback   bool
	}{
		{"401 unauthorized", 401, "unauthorized", FailoverAuth, false, true},
		{"403 forbidden", 403, "access forbidden", FailoverAuth, false, true},
		{"403 key limit", 403, "key limit exceeded", FailoverBilling, false, true},
		{"402 billing", 402, "insufficient credits", FailoverBilling, false, true},
		{"402 transient", 402, "usage limit, try again in 5m", FailoverRateLimit, true, true},
		{"404 not found", 404, "model not found", FailoverModelNotFound, false, true},
		{"413 too large", 413, "request entity too large", FailoverContextOverflow, true, false},
		{"429 rate limit", 429, "too many requests", FailoverRateLimit, true, true},
		{"400 context overflow", 400, "context length exceeded", FailoverContextOverflow, true, false},
		{"400 model not found", 400, "invalid model specified", FailoverModelNotFound, false, true},
		{"400 rate limit in body", 400, "rate limit exceeded", FailoverRateLimit, true, true},
		{"400 generic", 400, "bad request", FailoverFormatError, false, true},
		{"500 server error", 500, "internal server error", FailoverServerError, true, false},
		{"502 bad gateway", 502, "bad gateway", FailoverServerError, true, false},
		{"503 overloaded", 503, "service unavailable", FailoverOverloaded, true, true},
		{"529 overloaded", 529, "overloaded", FailoverOverloaded, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &mockHTTPError{code: tt.code, msg: tt.msg}
			c := ClassifyError(err)
			if c.Reason != tt.wantReason {
				t.Errorf("reason: got %s, want %s", c.Reason, tt.wantReason)
			}
			if c.Retryable != tt.retryable {
				t.Errorf("retryable: got %v, want %v", c.Retryable, tt.retryable)
			}
			if c.ShouldFallback != tt.fallback {
				t.Errorf("fallback: got %v, want %v", c.ShouldFallback, tt.fallback)
			}
			if c.StatusCode != tt.code {
				t.Errorf("statusCode: got %d, want %d", c.StatusCode, tt.code)
			}
		})
	}
}

func TestClassifyError_MessagePatterns(t *testing.T) {
	tests := []struct {
		name       string
		msg        string
		wantReason FailoverReason
	}{
		{"billing pattern", "insufficient credits for this request", FailoverBilling},
		{"rate limit pattern", "rate limit reached, throttled", FailoverRateLimit},
		{"context overflow", "context length exceeded maximum", FailoverContextOverflow},
		{"auth pattern", "invalid api key provided", FailoverAuth},
		{"model not found", "unknown model gpt-99", FailoverModelNotFound},
		{"usage limit transient", "usage limit exceeded, try again later", FailoverRateLimit},
		{"usage limit permanent", "usage limit exceeded permanently", FailoverBilling},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New(tt.msg)
			c := ClassifyError(err)
			if c.Reason != tt.wantReason {
				t.Errorf("reason: got %s, want %s", c.Reason, tt.wantReason)
			}
		})
	}
}

func TestClassifyError_NetworkError(t *testing.T) {
	err := &mockNetError{msg: "connection timeout"}
	c := ClassifyError(err)
	if c.Reason != FailoverTimeout {
		t.Errorf("reason: got %s, want timeout", c.Reason)
	}
	if !c.Retryable {
		t.Error("network errors should be retryable")
	}
}

func TestClassifyError_Unknown(t *testing.T) {
	err := errors.New("something completely unexpected happened")
	c := ClassifyError(err)
	if c.Reason != FailoverUnknown {
		t.Errorf("reason: got %s, want unknown", c.Reason)
	}
	if !c.Retryable {
		t.Error("unknown errors should be retryable")
	}
}

func TestClassifyError_WrappedError(t *testing.T) {
	inner := &mockHTTPError{code: 429, msg: "rate limited"}
	wrapped := fmt.Errorf("api call failed: %w", inner)
	c := ClassifyError(wrapped)
	if c.Reason != FailoverRateLimit {
		t.Errorf("wrapped error: got %s, want rate_limit", c.Reason)
	}
	if c.StatusCode != 429 {
		t.Errorf("wrapped statusCode: got %d, want 429", c.StatusCode)
	}
}

func TestClassifyError_ShouldCompress(t *testing.T) {
	err := &mockHTTPError{code: 400, msg: "context length exceeded"}
	c := ClassifyError(err)
	if !c.ShouldCompress {
		t.Error("context overflow should set ShouldCompress")
	}
	if c.ShouldFallback {
		t.Error("context overflow should not set ShouldFallback")
	}
}

func TestTruncateMessage(t *testing.T) {
	short := "hello"
	if truncateMessage(short, 500) != short {
		t.Error("short message should not be truncated")
	}
	long := string(make([]byte, 1000))
	if len(truncateMessage(long, 500)) != 500 {
		t.Error("long message should be truncated to 500")
	}
}
