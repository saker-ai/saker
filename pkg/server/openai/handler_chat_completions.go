package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/runhub"
	"github.com/cinience/saker/pkg/server"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
)

// keepaliveInterval is how often we emit an SSE comment frame to keep
// proxies (cloudflare, nginx) from idling out the connection. 15 s is
// well below typical 60 s defaults.
const keepaliveInterval = 15 * time.Second

// handleChatCompletions implements POST /v1/chat/completions.
//
// Flow:
//  1. Read + decode body.
//  2. Validate model / messages / extra_body.
//  3. Resolve identity from authMiddleware → tag the saker request.
//  4. MessagesToRequest folds OpenAI messages → saker Request (Ephemeral=true).
//  5. Register hub.Run with cancel func tied to producer goroutine.
//  6. Spawn producer that drains Runtime.RunStream → translates → publishes.
//  7. Consumer (this goroutine) writes SSE (stream=true) or aggregates a
//     single chat.completion JSON (stream=false).
func (g *Gateway) handleChatCompletions(c *gin.Context) {
	maxBody := g.deps.Options.MaxRequestBodyBytes
	if maxBody <= 0 {
		maxBody = 10 * 1024 * 1024
	}
	rawBody, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBody))
	if err != nil {
		InvalidRequest(c, "failed to read request body: "+err.Error())
		return
	}
	if int64(len(rawBody)) >= maxBody {
		InvalidRequest(c, fmt.Sprintf("request body exceeds %d bytes", maxBody))
		return
	}

	var req ChatRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		InvalidRequest(c, "invalid JSON body: "+err.Error())
		return
	}

	if strings.TrimSpace(req.Model) == "" {
		InvalidRequestField(c, "model", "field 'model' is required")
		return
	}
	if len(req.Messages) == 0 {
		InvalidRequestField(c, "messages", "field 'messages' must contain at least one message")
		return
	}

	extra, err := ParseExtraBody(req.ExtraBody)
	if err != nil {
		InvalidRequest(c, err.Error())
		return
	}

	// P0 limitation: ask_user_question_mode=tool_call requires the
	// pending-tool-call registry + second-POST resume protocol from §10. That
	// lands in P1; reject the value loudly so clients don't silently fall
	// back to "ask in text" behavior they didn't ask for.
	if extra.AskUserQuestionMode == AskQuestionToolCall {
		InvalidRequestField(c, "extra_body.ask_user_question_mode",
			"value 'tool_call' is not implemented in this build (P0). "+
				"Use 'fallback' or 'disabled', or omit the field.")
		return
	}

	tier := ResolveModelTier(req.Model)
	sakerReq, err := MessagesToRequest(req.Messages, extra, tier)
	if err != nil {
		InvalidRequest(c, err.Error())
		return
	}

	// Forward the OpenAI standard sampling knobs (temperature, top_p,
	// max_tokens, stop, seed, tool_choice, parallel_tool_calls) onto the
	// agent runtime. Provider adapters that don't consume a given field
	// silently ignore it, so this is safe to attach unconditionally.
	sakerReq.ModelOverrides = buildModelOverrides(req)

	identity := IdentityFromContext(c.Request.Context())
	if identity.Username != "" {
		sakerReq.User = identity.Username
	}
	tenantID := identity.APIKeyID
	if tenantID == "" {
		tenantID = identity.Username
	}

	expiresAfter := g.deps.Options.ExpiresAfter()
	if extra.ExpiresAfterSeconds > 0 {
		expiresAfter = time.Duration(extra.ExpiresAfterSeconds) * time.Second
	}
	turnTimeout := server.DefaultTurnTimeout
	if expiresAfter > turnTimeout {
		expiresAfter = turnTimeout
	}

	// Producer ctx is detached from the client unless cancel_on_disconnect
	// is set. The detached path lets the run keep going (and stay
	// reconnectable in P1) after the client closes the SSE socket.
	var (
		producerCtx    context.Context
		producerCancel context.CancelFunc
	)
	if extra.EffectiveCancelOnDisconnect() {
		producerCtx, producerCancel = context.WithTimeout(c.Request.Context(), turnTimeout)
	} else {
		producerCtx, producerCancel = context.WithTimeout(context.Background(), turnTimeout)
	}

	hubRun, err := g.hub.Create(runhub.CreateOptions{
		SessionID: sakerReq.SessionID,
		TenantID:  tenantID,
		ExpiresAt: time.Now().Add(expiresAfter),
		Cancel:    producerCancel,
	})
	if err != nil {
		producerCancel()
		if errors.Is(err, runhub.ErrCapacity) {
			RateLimited(c, "too many in-flight runs; try again later")
			return
		}
		ServerError(c, "failed to register run: "+err.Error())
		return
	}

	hubRun.SetStatus(runhub.RunStatusInProgress)

	// Surface the run id so clients can correlate against server logs and
	// (in P1) reconnect via /v1/runs/{id}/events. Set BEFORE PrepareSSE /
	// c.JSON, both of which write headers/status to the wire.
	c.Writer.Header().Set("X-Saker-Run-Id", hubRun.ID)
	// Surface the OTel trace id so a client can stitch its own span tree
	// to the server's `runhub.publish` → `runhub.batch.flush` chain in
	// Jaeger / Tempo. Empty when no provider is installed (the global
	// noop returns an empty SpanContext); we still write the header so
	// downstream proxies see a deterministic shape and don't add their
	// own. Set BEFORE the body writes (PrepareSSE / c.JSON) — a header
	// emitted after WriteHeader is silently dropped by net/http.
	if traceID := trace.SpanContextFromContext(c.Request.Context()).TraceID(); traceID.IsValid() {
		c.Writer.Header().Set("X-Saker-Trace-Id", traceID.String())
	}

	// P0 policy: never inject toolbuiltin.AskQuestionFunc into producerCtx.
	// All three human_input_modes resolve to the AskUserQuestion fallback
	// path (askuserquestion.go:82-91), which surfaces a graceful
	// "ask in your reply text instead" message to the LLM. The pause /
	// resume tool_call path lands in P1 along with the pending-tool-call
	// registry. PermissionRequestHandler stays whatever the runtime was
	// started with — the gateway doesn't override it per-request.
	g.deps.Logger.Info("openai gateway run starting",
		"run_id", hubRun.ID,
		"tenant", tenantID,
		"model", req.Model,
		"stream", req.Stream,
		"human_input_mode", extra.EffectiveHumanInputMode(),
		"cancel_on_disconnect", extra.EffectiveCancelOnDisconnect(),
	)

	eventCh, err := g.deps.Runtime.RunStream(producerCtx, sakerReq)
	if err != nil {
		producerCancel()
		g.hub.Finish(hubRun.ID, runhub.RunStatusFailed)
		InvalidRequest(c, err.Error())
		return
	}

	chunkID := makeChatChunkID(hubRun.ID)
	includeUsage := req.Stream && parseIncludeUsage(req.StreamOptions)

	go g.runChatProducer(eventCh, hubRun, producerCancel, chunkID, req.Model, extra.ExposeToolCalls, includeUsage)

	if req.Stream {
		g.streamChatSSE(c, hubRun, extra, includeUsage)
	} else {
		g.streamChatSync(c, hubRun, extra, req.Model, chunkID)
	}
}

// parseIncludeUsage looks up stream_options.include_usage and returns true
// only when explicitly set to true. Any non-bool / missing value yields
// false (forward-compat: unknown stream_options keys are ignored per the
// OpenAI spec).
func parseIncludeUsage(opts map[string]any) bool {
	if opts == nil {
		return false
	}
	v, ok := opts["include_usage"]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// runChatProducer drains the saker stream, translates each event into
// OpenAI chat.completion.chunk envelopes, JSON-marshals them, and
// publishes onto the hub's ring + subscribers. On stream end (channel
// closed), the run is marked terminal so subscribers see chan-close.
//
// producerCancel is the WithTimeout cancel returned alongside producerCtx;
// the producer defers it so the timer goroutine is reclaimed promptly when
// the saker stream finishes naturally instead of waiting out the 45-min
// turn timeout.
func (g *Gateway) runChatProducer(eventCh <-chan api.StreamEvent, hubRun *runhub.Run, producerCancel context.CancelFunc, chunkID, model string, exposeTools, includeUsage bool) {
	defer producerCancel()
	builder := newChatChunkBuilder(chunkID, hubRun.ID, model, g.deps.Options.ErrorDetailMode)
	filter := server.NewStreamArtifactFilter()

	finalStatus := runhub.RunStatusCompleted
	for evt := range eventCh {
		if evt.Type == api.EventError {
			finalStatus = runhub.RunStatusFailed
		}
		chunks, _ := builder.translate(evt, exposeTools, filter)
		for _, ch := range chunks {
			data, err := json.Marshal(ch)
			if err != nil {
				continue
			}
			hubRun.Publish("chunk", data)
		}
	}

	// If the saker stream closed without ever firing a finish-bearing
	// chunk, synthesize a "stop" so SDKs see a clean end-of-stream.
	if builder.finish == "" {
		chunk := builder.envelope(ChatChoice{
			Index:        0,
			Delta:        &ChatMessageOut{},
			FinishReason: "stop",
		})
		if data, err := json.Marshal(chunk); err == nil {
			hubRun.Publish("chunk", data)
		}
	}

	// Always emit a usage envelope when we observed any token counts, but
	// publish it with type="usage" so subscribers can decide whether to
	// forward it. SSE forwards only when stream_options.include_usage=true
	// (OpenAI spec requires the empty-choices frame to be opt-in); the sync
	// path always consumes it for the response.usage field.
	_ = includeUsage // SSE path filters by event Type, not by this flag
	if chunk, ok := builder.usageChunk(); ok {
		if data, err := json.Marshal(chunk); err == nil {
			hubRun.Publish("usage", data)
		}
	}

	g.hub.Finish(hubRun.ID, finalStatus)
}

// streamChatSSE writes the per-run event stream to the client as SSE.
// On client disconnect, honors cancel_on_disconnect (forces true when
// human_input_mode=never, see ExtraBody.EffectiveCancelOnDisconnect).
//
// includeUsage controls whether the trailing "usage" event (always
// produced when the runtime reports any token counts) is forwarded to the
// client. OpenAI requires this frame to be opt-in via
// stream_options.include_usage; old SDKs that don't ask for it would be
// confused by an empty-choices chunk and we'd break their final-message
// detection.
func (g *Gateway) streamChatSSE(c *gin.Context, hubRun *runhub.Run, extra ExtraBody, includeUsage bool) {
	flusher := PrepareSSE(c)
	if flusher == nil {
		ServerError(c, "stream not supported by underlying writer")
		return
	}

	eventsCh, backfill, unsub := hubRun.Subscribe()
	defer unsub()

	emit := func(e runhub.Event) error {
		if e.Type == "usage" && !includeUsage {
			return nil
		}
		return writeChunkSSE(c.Writer, hubRun.ID, e)
	}

	// Replay any events that landed in the ring before we subscribed.
	for _, e := range backfill {
		if err := emit(e); err != nil {
			return
		}
	}
	flusher.Flush()

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()

	clientCtx := c.Request.Context()

	for {
		select {
		case e, ok := <-eventsCh:
			if !ok {
				// Annotate non-clean terminations (cancelled/expired/failed)
				// with an OpenAI-shaped error frame BEFORE [DONE]; lets the
				// client distinguish a killed stream from one that ran to
				// completion. See pkg/server/openai/terminal_error.go.
				_ = writeTerminalErrorIfNeeded(c.Writer, hubRun)
				_ = WriteDone(c.Writer)
				flusher.Flush()
				return
			}
			if err := emit(e); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if err := WriteComment(c.Writer, "keepalive"); err != nil {
				return
			}
			flusher.Flush()
		case <-clientCtx.Done():
			if extra.EffectiveCancelOnDisconnect() {
				_ = g.hub.Cancel(hubRun.ID)
			}
			return
		}
	}
}

// writeChunkSSE serializes one ring event onto the SSE wire. Data is
// already JSON; we just wrap it with id: + data: lines.
//
// The id line uses the qualified format `<run_id>:<seq>`. Including the
// run id in the cursor lets parseLastEventID reject reconnect cursors
// from other runs (cross-run leak / probe protection) and gives clients
// a self-describing token they don't need to combine with separate
// state. This is a wire-protocol breaking change vs the legacy bare-int
// format; clients written against the old wire must be updated to
// extract the run id (see examples/21-openai-gateway).
func writeChunkSSE(w io.Writer, runID string, e runhub.Event) error {
	if _, err := fmt.Fprintf(w, "id: %s:%d\n", runID, e.Seq); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", e.Data); err != nil {
		return err
	}
	return nil
}

// streamChatSync collapses every chunk into a single chat.completion
// response and writes it as JSON. Mirrors the OpenAI non-streaming
// shape so SDKs can call this path interchangeably with stream=true.
func (g *Gateway) streamChatSync(c *gin.Context, hubRun *runhub.Run, extra ExtraBody, modelID, chunkID string) {
	eventsCh, backfill, unsub := hubRun.Subscribe()
	defer unsub()

	var (
		contentBuf   strings.Builder
		toolCalls    []ChatToolCall
		finishReason string
		usage        *ChatUsage
	)

	consume := func(e runhub.Event) {
		var chunk ChatCompletionChunk
		if err := json.Unmarshal(e.Data, &chunk); err != nil {
			return
		}
		// Usage piggybacks on the trailing chunk (empty choices, populated
		// usage). Capture it for the synchronous response envelope.
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		for _, ch := range chunk.Choices {
			if ch.Delta == nil {
				continue
			}
			if ch.Delta.Content != "" {
				contentBuf.WriteString(ch.Delta.Content)
			}
			if len(ch.Delta.ToolCalls) > 0 {
				toolCalls = append(toolCalls, ch.Delta.ToolCalls...)
			}
			if ch.FinishReason != "" {
				finishReason = ch.FinishReason
			}
		}
	}

	for _, e := range backfill {
		consume(e)
	}

	timer := time.NewTimer(server.DefaultTurnTimeout)
	defer timer.Stop()

	clientCtx := c.Request.Context()

loop:
	for {
		select {
		case e, ok := <-eventsCh:
			if !ok {
				break loop
			}
			consume(e)
		case <-timer.C:
			ServerError(c, "timeout waiting for completion")
			return
		case <-clientCtx.Done():
			if extra.EffectiveCancelOnDisconnect() {
				_ = g.hub.Cancel(hubRun.ID)
			}
			return
		}
	}

	if finishReason == "" {
		finishReason = "stop"
	}
	msg := &ChatMessageOut{
		Role:    "assistant",
		Content: server.CleanAssistantReply(contentBuf.String()),
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	resp := ChatCompletionResponse{
		ID:      chunkID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelID,
		Choices: []ChatChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
		Usage: usage,
	}
	c.JSON(http.StatusOK, resp)
}

// makeChatChunkID returns a stable chat.completion id derived from the
// hub run id. The "chatcmpl-" prefix mirrors OpenAI's wire format so
// SDKs that prefix-match (e.g. for telemetry) keep working.
func makeChatChunkID(runID string) string {
	return "chatcmpl-" + strings.TrimPrefix(runID, "run_")
}
