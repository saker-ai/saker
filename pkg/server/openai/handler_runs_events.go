package openai

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/runhub"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
)

// keepaliveReconnect is the heartbeat cadence for the reconnect SSE
// stream. Slightly longer than the chat-completions cadence because
// reconnect clients are typically background reconnect loops with
// looser idle expectations.
const keepaliveReconnect = 30 * time.Second

// handleRunsEvents serves GET /v1/runs/:id/events — the reconnect
// endpoint that lets a client resume an in-flight run after dropping
// the original POST /v1/chat/completions stream.
//
// Inputs:
//   - path :id          — the run id returned in X-Saker-Run-Id
//   - query last_event_id (preferred) OR header Last-Event-ID — the
//     last seq the client successfully consumed; the server replays
//     events with seq strictly greater than this value
//
// Auth model: the run must belong to the same tenant the request is
// authenticated as. A cross-tenant request gets HTTP 404 (NOT 403) so
// we never confirm the existence of someone else's run id.
//
// Failure modes:
//   - unknown run id, or cross-tenant access → 404
//   - aged-out replay window (sink gone or returned err) → 410 Gone
//   - hijacker-incompatible writer (should be impossible under gin) → 500
//
// On success, the handler writes a stream identical in shape to the
// chat-completions SSE: each event is `id: <run_id>:<seq>\ndata: <json>\n\n`,
// terminated with `data: [DONE]\n\n` when the run reaches a terminal
// state and the subscriber channel closes. The qualified id format is
// the wire-protocol contract — see writeChunkSSE for why.
func (g *Gateway) handleRunsEvents(c *gin.Context) {
	runID := c.Param("id")
	if runID == "" {
		InvalidRequest(c, "missing run id")
		return
	}

	hubRun, err := g.hub.Get(runID)
	if err != nil {
		// runhub.ErrNotFound → 404. Any other error (including DB
		// transport failures from PersistentHub.Get) → 500 so operators
		// can distinguish "client asked for a missing id" from "the
		// store is broken".
		if errors.Is(err, runhub.ErrNotFound) {
			NotFound(c, "no such run")
			return
		}
		ServerError(c, "failed to load run: "+err.Error())
		return
	}

	identity := IdentityFromContext(c.Request.Context())
	if !runOwnedByIdentity(hubRun, identity) {
		// Don't leak existence — same shape as the unknown-id path.
		NotFound(c, "no such run")
		return
	}

	sinceSeq, perr := parseLastEventID(c, runID)
	if perr != nil {
		// Cross-run cursor → 404 to avoid existence-leak (same shape as
		// the unknown-id path). Anything else (malformed, missing colon,
		// non-numeric seq) is a client bug → 400.
		if errors.Is(perr, errLastEventIDForeignRun) {
			NotFound(c, "no such run")
			return
		}
		InvalidRequestField(c, "last_event_id",
			"last_event_id must be in the format <run_id>:<seq>")
		return
	}

	eventsCh, backfill, recoverable, unsub := hubRun.SubscribeSince(sinceSeq)
	if !recoverable {
		// Ring aged out AND the sink couldn't fill the prefix (or no
		// sink configured at all). The client must restart from the
		// beginning of the run instead — surface the OpenAI-style
		// error envelope on a 410.
		writeReplayUnrecoverable(c, runID, sinceSeq)
		return
	}
	defer unsub()

	// Mirror the chat-completions handler: emit X-Saker-Trace-Id BEFORE
	// the SSE preamble. A reconnect client that already has the original
	// trace id can ignore this; one that doesn't (e.g. dropped + restarted
	// the connection from scratch) still gets a stitchable handle for the
	// reconnect-side spans.
	if traceID := trace.SpanContextFromContext(c.Request.Context()).TraceID(); traceID.IsValid() {
		c.Writer.Header().Set("X-Saker-Trace-Id", traceID.String())
	}

	flusher := PrepareSSE(c)
	if flusher == nil {
		ServerError(c, "stream not supported by underlying writer")
		return
	}

	for _, e := range backfill {
		if err := writeChunkSSE(c.Writer, runID, e); err != nil {
			return
		}
	}
	flusher.Flush()

	// If the run is already terminal and the subscriber chan was closed
	// during/before backfill, the for-range below still hits the closed
	// channel on the first read and emits [DONE] cleanly. No special
	// case needed.

	keepalive := time.NewTicker(keepaliveReconnect)
	defer keepalive.Stop()

	clientCtx := c.Request.Context()
	for {
		select {
		case e, ok := <-eventsCh:
			if !ok {
				// Annotate non-clean terminations (cancelled/expired/failed)
				// with an OpenAI-shaped error frame BEFORE [DONE]; reconnect
				// callers can distinguish "stream ended naturally" from
				// "underlying run was killed". See terminal_error.go.
				_ = writeTerminalErrorIfNeeded(c.Writer, hubRun)
				_ = WriteDone(c.Writer)
				flusher.Flush()
				return
			}
			if err := writeChunkSSE(c.Writer, runID, e); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if err := WriteComment(c.Writer, "keepalive"); err != nil {
				return
			}
			flusher.Flush()
		case <-clientCtx.Done():
			// Reconnect endpoint never cancels the underlying run on
			// disconnect — the producer goroutine is owned by the
			// originating chat-completions request, not by us.
			return
		}
	}
}

// runOwnedByIdentity returns true when the run can be served to the
// authenticated identity. Tenant scoping mirrors the chat-completions
// handler: we use APIKeyID as the canonical tenant key, falling back to
// Username when the row has no key id (legacy / dev-bypass paths).
//
// Runs with an empty TenantID are rejected — an empty tenant means the
// run was created without proper auth and should not be accessible.
func runOwnedByIdentity(r *runhub.Run, id Identity) bool {
	if r.TenantID == "" {
		return false
	}
	tenant := id.APIKeyID
	if tenant == "" {
		tenant = id.Username
	}
	return tenant == r.TenantID
}

// errLastEventIDFormat surfaces a malformed reconnect cursor (missing
// colon, empty parts, non-numeric seq). The handler maps it to a 400.
var errLastEventIDFormat = errors.New("last_event_id format invalid")

// errLastEventIDForeignRun fires when the parsed cursor's run id doesn't
// match the path :id. The handler maps it to a 404 (existence-leak
// prevention — we don't tell the caller whether the foreign run exists).
var errLastEventIDForeignRun = errors.New("last_event_id refers to a different run")

// parseLastEventID extracts the resume cursor. The query parameter
// `last_event_id` wins because it survives proxies that strip
// `Last-Event-ID`. Both empty → (0, nil) (replay everything).
//
// Wire format: `<run_id>:<seq>`. The run id portion MUST equal the
// path's :id; the seq MUST be a non-negative integer. The previous
// bare-integer format is no longer accepted — clients reading old
// responses must update their cursor extraction (see
// examples/21-openai-gateway).
func parseLastEventID(c *gin.Context, expectedRunID string) (int, error) {
	raw := c.Query("last_event_id")
	if raw == "" {
		raw = c.GetHeader("Last-Event-ID")
	}
	if raw == "" {
		return 0, nil
	}
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, errLastEventIDFormat
	}
	if parts[0] != expectedRunID {
		return 0, errLastEventIDForeignRun
	}
	seq, err := strconv.Atoi(parts[1])
	if err != nil || seq < 0 {
		return 0, errLastEventIDFormat
	}
	return seq, nil
}

// writeReplayUnrecoverable emits the OpenAI-style error envelope for a
// 410 Gone reply. We use 410 (and not 404) so a client distinguishes
// "the run never existed / is not yours" from "the run is real but the
// replay window has passed."
func writeReplayUnrecoverable(c *gin.Context, runID string, sinceSeq int) {
	c.AbortWithStatusJSON(http.StatusGone, gin.H{
		"error": gin.H{
			"message": "event replay unrecoverable: requested last_event_id is older than the persisted retention window",
			"type":    "invalid_request_error",
			"code":    "event_replay_unrecoverable",
			"param":   "last_event_id",
			"run_id":  runID,
			"since":   sinceSeq,
		},
	})
}
