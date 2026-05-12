// error_classifier.go: Bifrost-era error classification surface. SDK-level
// routing decisions (auth → no fallback, 429 → fallback, etc.) now live inside
// Bifrost's Fallbacks engine, so this file is intentionally minimal — it only
// preserves the ClassifiedError / FailoverReason taxonomy that bubbles out to
// saker callers via OnFailover callbacks and middleware logging, plus the
// HTTPStatusError interface used by bifrostStatusError to surface status codes.
package model

import (
	"errors"
	"net/http"
	"strings"
)

// FailoverReason categorizes why an API call failed. Used as a stable label in
// logs and the OnFailover callback; not consumed by routing logic anymore.
type FailoverReason string

const (
	FailoverAuth            FailoverReason = "auth"
	FailoverBilling         FailoverReason = "billing"
	FailoverRateLimit       FailoverReason = "rate_limit"
	FailoverOverloaded      FailoverReason = "overloaded"
	FailoverServerError     FailoverReason = "server_error"
	FailoverTimeout         FailoverReason = "timeout"
	FailoverContextOverflow FailoverReason = "context_overflow"
	FailoverModelNotFound   FailoverReason = "model_not_found"
	FailoverFormatError     FailoverReason = "format_error"
	FailoverUnknown         FailoverReason = "unknown"
)

// ClassifiedError is the structured shape surfaced by ClassifyError /
// ClassifyBifrostError. Retryable / ShouldFallback / ShouldCompress are
// informational only — Bifrost's SDK-level Fallbacks engine owns routing.
type ClassifiedError struct {
	Reason         FailoverReason
	StatusCode     int
	Message        string
	Retryable      bool
	ShouldCompress bool
	ShouldFallback bool
}

// HTTPStatusError is implemented by SDK / wrapper errors that carry an HTTP
// status code. bifrostStatusError (bifrost_helpers.go) is the canonical
// implementer in the Bifrost-era code path.
type HTTPStatusError interface {
	error
	HTTPStatusCode() int
}

// ClassifyError maps a saker / Bifrost error to a ClassifiedError. Only HTTP
// status codes are inspected — message-level pattern matching for routing
// decisions has been retired in favor of Bifrost's SDK-level Fallbacks engine.
// String matchers that still need to peek at error messages (compact_restore.go's
// isPromptTooLong) read err.Error() directly.
func ClassifyError(err error) ClassifiedError {
	if err == nil {
		return ClassifiedError{Reason: FailoverUnknown}
	}
	statusCode := extractStatusCode(err)
	msg := truncateMessage(err.Error(), 500)

	build := func(reason FailoverReason, retryable, compress, fallback bool) ClassifiedError {
		return ClassifiedError{
			Reason: reason, StatusCode: statusCode, Message: msg,
			Retryable: retryable, ShouldCompress: compress, ShouldFallback: fallback,
		}
	}

	switch {
	case statusCode == http.StatusUnauthorized, statusCode == http.StatusForbidden:
		return build(FailoverAuth, false, false, true)
	case statusCode == http.StatusPaymentRequired:
		return build(FailoverBilling, false, false, true)
	case statusCode == http.StatusNotFound:
		return build(FailoverModelNotFound, false, false, true)
	case statusCode == http.StatusRequestEntityTooLarge:
		return build(FailoverContextOverflow, true, true, false)
	case statusCode == http.StatusTooManyRequests:
		return build(FailoverRateLimit, true, false, true)
	case statusCode == http.StatusBadRequest:
		// 400 with a "prompt too long"-style message → context overflow.
		if isContextOverflowMsg(strings.ToLower(err.Error())) {
			return build(FailoverContextOverflow, true, true, false)
		}
		return build(FailoverFormatError, false, false, true)
	case statusCode == http.StatusInternalServerError, statusCode == http.StatusBadGateway:
		return build(FailoverServerError, true, false, false)
	case statusCode == http.StatusServiceUnavailable, statusCode == 529:
		return build(FailoverOverloaded, true, false, true)
	case statusCode >= 400 && statusCode < 500:
		return build(FailoverFormatError, false, false, true)
	case statusCode >= 500 && statusCode < 600:
		return build(FailoverServerError, true, false, false)
	}
	return build(FailoverUnknown, true, false, false)
}

func isContextOverflowMsg(msg string) bool {
	for _, p := range []string{"context length", "context window", "prompt is too long", "too many tokens", "token limit", "max_tokens"} {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

func extractStatusCode(err error) int {
	cur := err
	for i := 0; i < 5 && cur != nil; i++ {
		if h, ok := cur.(HTTPStatusError); ok {
			return h.HTTPStatusCode()
		}
		type statusCoder interface{ StatusCode() int }
		if sc, ok := cur.(statusCoder); ok {
			return sc.StatusCode()
		}
		cur = errors.Unwrap(cur)
	}
	return 0
}

func truncateMessage(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
