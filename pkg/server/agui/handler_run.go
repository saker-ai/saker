package agui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/server"
	toolbuiltin "github.com/saker-ai/saker/pkg/tool/builtin"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguisse "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
)

const keepaliveInterval = 15 * time.Second

// handleRun implements POST /v1/agents/run — the main AG-UI streaming endpoint.
func (g *Gateway) handleRun(c *gin.Context) {
	body, err := readBody(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "failed to read request body: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}

	inner, envelopeMethod := aguiUnwrapEnvelope(c, body)
	switch envelopeMethod {
	case "info":
		g.handleInfo(c)
		return
	case "threads":
		g.handleThreads(c)
		return
	case "agent/stop":
		g.handleStop(c)
		return
	case "agent/connect":
		g.handleConnect(c, inner)
		return
	}
	if inner != nil {
		body = inner
	}

	var input aguitypes.RunAgentInput
	if err := json.Unmarshal(body, &input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "invalid JSON body: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}

	if len(input.Messages) == 0 {
		g.handleInfo(c)
		return
	}

	threadID := input.ThreadID
	if threadID == "" {
		threadID = "thread_" + uuid.New().String()
	}
	runID := input.RunID
	if runID == "" {
		runID = "run_" + uuid.New().String()
	}

	identity := identityFromContext(c.Request.Context())
	sakerReq := messagesToRequest(input, identity)

	projectID := identity.ProjectID
	if projectID == "" {
		projectID = "default"
	}

	g.ensureThread(c.Request.Context(), threadID, identity)

	turnID := runID
	if g.deps.ConversationStore != nil {
		if tid, err := g.deps.ConversationStore.OpenTurn(c.Request.Context(), threadID, ""); err == nil {
			turnID = tid
		}
	}

	g.persistUserMessage(c.Request.Context(), threadID, turnID, projectID, sakerReq.Prompt)

	ctx, cancel := context.WithTimeout(c.Request.Context(), server.DefaultTurnTimeout)
	defer cancel()

	sideCh := make(chan sideEvent, 8)
	ctx = toolbuiltin.WithAskQuestionFunc(ctx, g.makeAskQuestionHandler(runID, sideCh))

	sakerReq.Metadata = mergeMetadata(sakerReq.Metadata, map[string]any{
		"_agui_run_id":             runID,
		"_agui_permission_handler": g.makePermissionHandler(runID, sideCh),
	})

	eventCh, err := g.deps.Runtime.RunStream(ctx, sakerReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "failed to start run: " + err.Error(),
			"type":    "server_error",
		}})
		return
	}

	g.streamSSE(c, ctx, eventCh, sideCh, threadID, runID, turnID, projectID)
}

// streamSSE writes the AG-UI event stream to the client as SSE.
func (g *Gateway) streamSSE(c *gin.Context, ctx context.Context, eventCh <-chan api.StreamEvent, sideCh <-chan sideEvent, threadID, runID, turnID, projectID string) {
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()

	sseW := aguisse.NewSSEWriter().WithLogger(g.deps.Logger)
	state := newStreamState(threadID, runID)
	filter := server.NewStreamArtifactFilter()
	var accumulated strings.Builder

	writeSSE(w, sseW, aguievents.NewRunStartedEvent(threadID, runID))
	flusher.Flush()

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				state.finalize(w, sseW, filter)
				flusher.Flush()
				g.persistAssistantMessage(context.Background(), threadID, turnID, projectID, accumulated.String())
				if g.deps.ConversationStore != nil {
					_ = g.deps.ConversationStore.CloseTurn(context.Background(), turnID, "completed")
				}
				return
			}
			if evt.Type == api.EventContentBlockDelta && evt.Delta != nil && evt.Delta.Text != "" {
				accumulated.WriteString(evt.Delta.Text)
			}
			state.translateEvent(w, sseW, evt, filter)
			flusher.Flush()

		case se := <-sideCh:
			writeSSE(w, sseW, se.event)
			flusher.Flush()

		case <-keepalive.C:
			if _, err := fmt.Fprintf(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()

		case <-ctx.Done():
			writeSSE(w, sseW, aguievents.NewRunErrorEvent("request cancelled", aguievents.WithRunID(runID)))
			writeSSE(w, sseW, aguievents.NewRunFinishedEvent(threadID, runID))
			flusher.Flush()
			return
		}
	}
}

func mergeMetadata(base, extra map[string]any) map[string]any {
	if base == nil {
		return extra
	}
	for k, v := range extra {
		base[k] = v
	}
	return base
}
