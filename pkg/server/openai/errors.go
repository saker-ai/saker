package openai

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ErrorEnvelope is the OpenAI standard error response shape. Every
// non-2xx response from /v1/* paths must serialize to this structure so
// existing OpenAI SDKs (which key off `error.type` and `error.code`)
// surface meaningful messages to the user.
type ErrorEnvelope struct {
	Error ErrorPayload `json:"error"`
}

// ErrorPayload is the inner object. `param` is the offending field name
// when the error came from request validation (mirrors OpenAI's behavior
// for invalid_request_error).
type ErrorPayload struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
}

// Error type constants — mirror the canonical OpenAI strings so client
// SDKs that branch on type get the behavior they expect.
const (
	ErrTypeInvalidRequest    = "invalid_request_error"
	ErrTypeAuthentication    = "authentication_error"
	ErrTypePermission        = "permission_error"
	ErrTypeNotFound          = "not_found_error"
	ErrTypeRateLimit         = "rate_limit_error"
	ErrTypeAPI               = "api_error"
	ErrTypeServerError       = "server_error"
	ErrTypeServiceUnavail    = "service_unavailable_error"
	ErrTypeRequestExpired    = "request_expired_error"
	ErrTypeSessionAwaiting   = "session_awaiting_tool_response"
	ErrTypeRunNotFound       = "run_not_found"
	ErrTypeToolOutputBadFmt  = "tool_outputs_invalid"
	ErrTypeRunExpired        = "run_expired"
	ErrTypeMessagesPrefixMis = "messages_prefix_mismatch"
)

// AbortWith writes an OpenAI-shaped error and aborts the gin context.
// Always returns nil so callers can `return AbortWith(...)` from a
// handler in one line.
func AbortWith(c *gin.Context, status int, errType, message string) error {
	c.AbortWithStatusJSON(status, ErrorEnvelope{Error: ErrorPayload{
		Message: message,
		Type:    errType,
	}})
	return nil
}

// AbortWithParam is the same as AbortWith but also tags which request
// field caused the error — used for invalid_request_error responses so
// SDKs can surface "field X is wrong" cleanly.
func AbortWithParam(c *gin.Context, status int, errType, message, param string) error {
	c.AbortWithStatusJSON(status, ErrorEnvelope{Error: ErrorPayload{
		Message: message,
		Type:    errType,
		Param:   param,
	}})
	return nil
}

// Common preset helpers.

// InvalidRequest is a 400 with errType=invalid_request_error.
func InvalidRequest(c *gin.Context, message string) {
	_ = AbortWith(c, http.StatusBadRequest, ErrTypeInvalidRequest, message)
}

// InvalidRequestField is a 400 tagged with the offending field name.
func InvalidRequestField(c *gin.Context, field, message string) {
	_ = AbortWithParam(c, http.StatusBadRequest, ErrTypeInvalidRequest, message, field)
}

// Unauthorized is a 401 with errType=authentication_error.
func Unauthorized(c *gin.Context, message string) {
	_ = AbortWith(c, http.StatusUnauthorized, ErrTypeAuthentication, message)
}

// Forbidden is a 403 with errType=permission_error.
func Forbidden(c *gin.Context, message string) {
	_ = AbortWith(c, http.StatusForbidden, ErrTypePermission, message)
}

// NotFound is a 404 with errType=not_found_error.
func NotFound(c *gin.Context, message string) {
	_ = AbortWith(c, http.StatusNotFound, ErrTypeNotFound, message)
}

// RateLimited is a 429 with errType=rate_limit_error.
func RateLimited(c *gin.Context, message string) {
	_ = AbortWith(c, http.StatusTooManyRequests, ErrTypeRateLimit, message)
}

// ServerError is a 500 with errType=server_error.
func ServerError(c *gin.Context, message string) {
	_ = AbortWith(c, http.StatusInternalServerError, ErrTypeServerError, message)
}
