package model

import (
	"errors"
	"net"
	"net/http"
	"strings"
)

// FailoverReason categorizes why an API call failed, determining recovery strategy.
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

// ClassifiedError holds the structured classification of an API error with recovery hints.
type ClassifiedError struct {
	Reason         FailoverReason
	StatusCode     int
	Message        string
	Retryable      bool
	ShouldCompress bool
	ShouldFallback bool
}

// HTTPStatusError is implemented by SDK errors that carry an HTTP status code.
type HTTPStatusError interface {
	error
	HTTPStatusCode() int
}

// billingPatterns indicate permanent billing exhaustion.
var billingPatterns = []string{
	"insufficient credits",
	"insufficient_quota",
	"credit balance",
	"credits have been exhausted",
	"payment required",
	"billing hard limit",
	"exceeded your current quota",
	"account is deactivated",
}

// rateLimitPatterns indicate transient rate limiting.
var rateLimitPatterns = []string{
	"rate limit",
	"rate_limit",
	"too many requests",
	"throttled",
	"requests per minute",
	"tokens per minute",
	"requests per day",
	"try again in",
	"please retry after",
	"resource_exhausted",
}

// contextOverflowPatterns indicate context window exceeded.
var contextOverflowPatterns = []string{
	"context length",
	"context size",
	"maximum context",
	"token limit",
	"too many tokens",
	"reduce the length",
	"exceeds the limit",
	"context window",
	"prompt is too long",
	"max_tokens",
	"maximum number of tokens",
	"input is too long",
}

// modelNotFoundPatterns indicate invalid or unavailable model.
var modelNotFoundPatterns = []string{
	"is not a valid model",
	"invalid model",
	"model not found",
	"model_not_found",
	"does not exist",
	"no such model",
	"unknown model",
	"unsupported model",
}

// authPatterns indicate authentication/authorization failures.
var authPatterns = []string{
	"invalid api key",
	"invalid_api_key",
	"authentication",
	"unauthorized",
	"forbidden",
	"invalid token",
	"access denied",
}

// usageLimitPatterns need disambiguation (could be billing OR rate_limit).
var usageLimitPatterns = []string{
	"usage limit",
	"quota",
	"limit exceeded",
	"key limit exceeded",
}

// usageLimitTransientSignals confirm the usage limit is transient.
var usageLimitTransientSignals = []string{
	"try again",
	"retry",
	"resets at",
	"reset in",
	"wait",
	"window",
}

// ClassifyError classifies an API error into a structured recovery recommendation.
// Priority: status code → message pattern matching → transport heuristics → unknown.
func ClassifyError(err error) ClassifiedError {
	if err == nil {
		return ClassifiedError{Reason: FailoverUnknown}
	}

	statusCode := extractStatusCode(err)
	errMsg := strings.ToLower(err.Error())

	build := func(reason FailoverReason, retryable, compress, fallback bool) ClassifiedError {
		return ClassifiedError{
			Reason:         reason,
			StatusCode:     statusCode,
			Message:        truncateMessage(err.Error(), 500),
			Retryable:      retryable,
			ShouldCompress: compress,
			ShouldFallback: fallback,
		}
	}

	// 1. HTTP status code classification
	if statusCode > 0 {
		if c := classifyByStatus(statusCode, errMsg, build); c != nil {
			return *c
		}
	}

	// 2. Message pattern matching (no status code or unclassified status)
	if c := classifyByMessage(errMsg, build); c != nil {
		return *c
	}

	// 3. Transport / timeout heuristics
	var netErr net.Error
	if errors.As(err, &netErr) {
		return build(FailoverTimeout, true, false, false)
	}

	// 4. Fallback: unknown (retryable with backoff)
	return build(FailoverUnknown, true, false, false)
}

type buildFn func(reason FailoverReason, retryable, compress, fallback bool) ClassifiedError

func classifyByStatus(code int, msg string, build buildFn) *ClassifiedError {
	switch {
	case code == http.StatusUnauthorized: // 401
		c := build(FailoverAuth, false, false, true)
		return &c

	case code == http.StatusForbidden: // 403
		if containsAny(msg, []string{"key limit exceeded", "spending limit"}) {
			c := build(FailoverBilling, false, false, true)
			return &c
		}
		c := build(FailoverAuth, false, false, true)
		return &c

	case code == http.StatusPaymentRequired: // 402
		c := classify402(msg, build)
		return &c

	case code == http.StatusNotFound: // 404
		c := build(FailoverModelNotFound, false, false, true)
		return &c

	case code == http.StatusRequestEntityTooLarge: // 413
		c := build(FailoverContextOverflow, true, true, false)
		return &c

	case code == http.StatusTooManyRequests: // 429
		c := build(FailoverRateLimit, true, false, true)
		return &c

	case code == http.StatusBadRequest: // 400
		return classify400(msg, build)

	case code == http.StatusInternalServerError || code == http.StatusBadGateway: // 500, 502
		c := build(FailoverServerError, true, false, false)
		return &c

	case code == http.StatusServiceUnavailable || code == 529: // 503, 529
		c := build(FailoverOverloaded, true, false, true)
		return &c

	case code >= 400 && code < 500:
		c := build(FailoverFormatError, false, false, true)
		return &c

	case code >= 500 && code < 600:
		c := build(FailoverServerError, true, false, false)
		return &c
	}
	return nil
}

func classify402(msg string, build buildFn) ClassifiedError {
	hasUsageLimit := containsAny(msg, usageLimitPatterns)
	hasTransient := containsAny(msg, usageLimitTransientSignals)
	if hasUsageLimit && hasTransient {
		return build(FailoverRateLimit, true, false, true)
	}
	return build(FailoverBilling, false, false, true)
}

func classify400(msg string, build buildFn) *ClassifiedError {
	if containsAny(msg, contextOverflowPatterns) {
		c := build(FailoverContextOverflow, true, true, false)
		return &c
	}
	if containsAny(msg, modelNotFoundPatterns) {
		c := build(FailoverModelNotFound, false, false, true)
		return &c
	}
	if containsAny(msg, rateLimitPatterns) {
		c := build(FailoverRateLimit, true, false, true)
		return &c
	}
	if containsAny(msg, billingPatterns) {
		c := build(FailoverBilling, false, false, true)
		return &c
	}
	c := build(FailoverFormatError, false, false, true)
	return &c
}

func classifyByMessage(msg string, build buildFn) *ClassifiedError {
	// Billing
	if containsAny(msg, billingPatterns) {
		c := build(FailoverBilling, false, false, true)
		return &c
	}
	// Rate limit
	if containsAny(msg, rateLimitPatterns) {
		c := build(FailoverRateLimit, true, false, true)
		return &c
	}
	// Usage limit disambiguation
	if containsAny(msg, usageLimitPatterns) {
		if containsAny(msg, usageLimitTransientSignals) {
			c := build(FailoverRateLimit, true, false, true)
			return &c
		}
		c := build(FailoverBilling, false, false, true)
		return &c
	}
	// Context overflow
	if containsAny(msg, contextOverflowPatterns) {
		c := build(FailoverContextOverflow, true, true, false)
		return &c
	}
	// Auth
	if containsAny(msg, authPatterns) {
		c := build(FailoverAuth, false, false, true)
		return &c
	}
	// Model not found
	if containsAny(msg, modelNotFoundPatterns) {
		c := build(FailoverModelNotFound, false, false, true)
		return &c
	}
	return nil
}

// extractStatusCode walks the error chain to find an HTTP status code.
func extractStatusCode(err error) int {
	var current error = err
	for i := 0; i < 5 && current != nil; i++ {
		if httpErr, ok := current.(HTTPStatusError); ok {
			return httpErr.HTTPStatusCode()
		}
		// Check for a StatusCode field via interface
		type statusCoder interface{ StatusCode() int }
		if sc, ok := current.(statusCoder); ok {
			return sc.StatusCode()
		}
		current = errors.Unwrap(current)
	}
	return 0
}

func containsAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func truncateMessage(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
