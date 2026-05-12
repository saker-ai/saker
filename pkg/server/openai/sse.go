package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// PrepareSSE sets the response headers and disables gin's buffering for
// the lifetime of the SSE stream. Returns the underlying flusher so the
// caller can flush after each event.
//
// Returns nil if the response writer doesn't support flushing — this is
// a programmer error in production (gin's gin.responseWriter always
// implements http.Flusher) but kept defensive for clarity.
func PrepareSSE(c *gin.Context) http.Flusher {
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx proxy buffering
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	flusher.Flush() // flush headers immediately so the client sees the open stream
	return flusher
}

// SSEEvent represents a single Server-Sent Event frame. ID and Retry are
// optional. Data is JSON-marshaled when written via WriteEvent. Use
// WriteRawData if Data is already a JSON string.
type SSEEvent struct {
	// ID is the event identifier (becomes Last-Event-ID on reconnect).
	// Empty means "don't send an id: line".
	ID string
	// Event is the SSE event type. Empty means a default "message" event.
	Event string
	// Data is the JSON-serializable payload.
	Data any
	// Retry is the reconnect delay hint in milliseconds. Zero means
	// "don't send a retry: line" (browser default applies).
	Retry int
}

// WriteEvent writes a single SSE event to w. Always returns the underlying
// I/O error if any so the caller can detect a client disconnect mid-stream.
//
// SSE wire format reference: https://html.spec.whatwg.org/multipage/server-sent-events.html
func WriteEvent(w io.Writer, evt SSEEvent) error {
	if evt.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", evt.ID); err != nil {
			return err
		}
	}
	if evt.Event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", evt.Event); err != nil {
			return err
		}
	}
	if evt.Retry > 0 {
		if _, err := fmt.Fprintf(w, "retry: %s\n", strconv.Itoa(evt.Retry)); err != nil {
			return err
		}
	}

	payload, err := json.Marshal(evt.Data)
	if err != nil {
		return fmt.Errorf("sse: marshal data: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	return nil
}

// WriteDone writes the OpenAI streaming sentinel — `data: [DONE]\n\n` —
// and returns any I/O error. SDKs treat this as the end-of-stream marker.
func WriteDone(w io.Writer) error {
	_, err := fmt.Fprint(w, "data: [DONE]\n\n")
	return err
}

// WriteComment writes an SSE comment line (`: %s\n\n`). Useful for
// keepalive heartbeats; clients ignore comments per the SSE spec.
func WriteComment(w io.Writer, text string) error {
	_, err := fmt.Fprintf(w, ": %s\n\n", text)
	return err
}

// WriteErrorEvent emits an `event: error\ndata: {ErrorEnvelope}\n\n`
// frame mid-stream. Used to surface a non-success run termination
// (cancelled / expired / failed) so the client can distinguish a clean
// end-of-stream from a killed one. The caller is expected to write
// `[DONE]` immediately after — the OpenAI streaming sentinel still
// terminates the conceptual stream; the error frame just annotates the
// reason. EventSource implementations and the official OpenAI SDKs both
// parse the standard `event:` line correctly.
func WriteErrorEvent(w io.Writer, env ErrorEnvelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("sse: marshal error envelope: %w", err)
	}
	if _, err := fmt.Fprintf(w, "event: error\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	return nil
}
