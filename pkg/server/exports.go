package server

import "github.com/gin-gonic/gin"

// Public re-exports of pkg/server internals that pkg/server/openai needs.
// The two filters live in pkg/server (next to handler_turn.go) because
// the WebSocket handler depends on them; the OpenAI gateway lives in a
// sibling subpackage and can't reach them directly. Rather than move the
// filters or duplicate them, we expose minimal wrappers here so the
// gateway can consume the same battle-tested logic.

// StreamArtifactFilter is the public surface of streamArtifactFilter.
// It strips function-call leakage (e.g. <tool_call>, Anthropic-style
// <invoke>, <|FunctionCallBegin|>) from streaming text deltas before
// they reach SSE subscribers.
//
// Use:
//
//	f := server.NewStreamArtifactFilter()
//	for chunk := range deltas {
//	    if safe := f.Push(chunk); safe != "" {
//	        emit(safe)
//	    }
//	}
//	if tail := f.Flush(); tail != "" {
//	    emit(tail)
//	}
type StreamArtifactFilter struct {
	inner *streamArtifactFilter
}

// NewStreamArtifactFilter builds a fresh filter with empty held state.
func NewStreamArtifactFilter() *StreamArtifactFilter {
	return &StreamArtifactFilter{inner: &streamArtifactFilter{}}
}

// Push consumes a delta chunk and returns whatever is safe to forward
// downstream. Returns "" when the whole chunk is held back; subscribers
// should treat that as "no-op, wait for the next delta".
func (f *StreamArtifactFilter) Push(chunk string) string {
	if f == nil || f.inner == nil {
		return chunk
	}
	return f.inner.Push(chunk)
}

// Flush releases any held-back bytes at end-of-stream.
func (f *StreamArtifactFilter) Flush() string {
	if f == nil || f.inner == nil {
		return ""
	}
	return f.inner.Flush()
}

// CleanAssistantReply is the canonical post-stream cleanup pass: trims
// streaming dot artifacts and strips leaked function-call syntax (Qwen-style
// XML, Claude-style invoke, etc.). Returns "" when nothing meaningful
// remains.
func CleanAssistantReply(raw string) string {
	return cleanAssistantReply(raw)
}

// DefaultTurnTimeout is the public alias for the in-package constant. The
// OpenAI gateway uses it as the upper bound for chat-completions runs so
// it matches the behavior of the WebSocket-driven turn handler.
const DefaultTurnTimeout = defaultTurnTimeout

// SessionValidatorFunc returns a gin.Context-typed callback that validates the
// saker_session cookie (or localhost loopback) and extracts identity. Intended
// for EngineHook-mounted gateways (AG-UI, etc.) that need browser auth but
// run outside the main auth middleware chain.
func (s *Server) SessionValidatorFunc() func(c *gin.Context) (string, string, bool) {
	return func(c *gin.Context) (string, string, bool) {
		if isLocalhost(c.Request) {
			if s.auth.cfg == nil || s.auth.cfg.Password == "" {
				return "localhost", "admin", true
			}
			adminUser := s.auth.cfg.Username
			if adminUser == "" {
				adminUser = "admin"
			}
			return adminUser, "admin", true
		}
		cookie, err := c.Request.Cookie(sessionCookieName)
		if err != nil || !s.auth.validToken(cookie.Value) {
			return "", "", false
		}
		username, role := s.auth.extractTokenInfo(cookie.Value)
		return username, role, true
	}
}
