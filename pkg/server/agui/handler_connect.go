package agui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	aguisse "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"

	"github.com/saker-ai/saker/pkg/conversation"
)

// handleConnect implements the AG-UI /agent/connect endpoint. CopilotKit
// calls this when a threadId is set (or changes) to load existing messages.
// Response is an SSE stream: RUN_STARTED → MESSAGES_SNAPSHOT → RUN_FINISHED.
func (g *Gateway) handleConnect(c *gin.Context, body []byte) {
	var input aguitypes.RunAgentInput
	if body != nil {
		if err := json.Unmarshal(body, &input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"message": "invalid JSON body: " + err.Error(),
				"type":    "invalid_request_error",
			}})
			return
		}
	}

	threadID := input.ThreadID
	if threadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "threadId is required for connect",
			"type":    "invalid_request_error",
		}})
		return
	}

	runID := input.RunID
	if runID == "" {
		runID = "run_" + uuid.New().String()
	}

	var aguiMessages []aguitypes.Message
	if g.deps.ConversationStore != nil {
		msgs, err := g.deps.ConversationStore.GetMessages(
			c.Request.Context(), threadID, conversation.GetMessagesOpts{},
		)
		if err != nil {
			g.deps.Logger.Warn("agui connect: failed to load messages",
				"thread_id", threadID, "error", err)
		} else {
			aguiMessages = convertMessages(msgs)
		}
	}

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

	sseW := aguisse.NewSSEWriter().WithLogger(g.deps.Logger)

	writeSSE(w, sseW, aguievents.NewRunStartedEvent(threadID, runID))
	writeSSE(w, sseW, aguievents.NewMessagesSnapshotEvent(aguiMessages))
	writeSSE(w, sseW, aguievents.NewRunFinishedEvent(threadID, runID))
	flusher.Flush()
}

// convertMessages translates conversation.Message rows into AG-UI typed
// messages suitable for a MESSAGES_SNAPSHOT event.
func convertMessages(msgs []conversation.Message) []aguitypes.Message {
	out := make([]aguitypes.Message, 0, len(msgs))
	for i := range msgs {
		m := &msgs[i]
		am := aguitypes.Message{
			ID:         strconv.FormatInt(m.ID, 10),
			Role:       aguitypes.Role(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			am.ToolCalls = convertToolCalls(m.ToolCalls)
		}
		out = append(out, am)
	}
	return out
}

// convertToolCalls deserializes the stored [{id, name, arguments}] JSON
// array into AG-UI typed ToolCall structs.
func convertToolCalls(raw json.RawMessage) []aguitypes.ToolCall {
	var stored []struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil
	}
	out := make([]aguitypes.ToolCall, len(stored))
	for i, tc := range stored {
		args := string(tc.Arguments)
		if args == "" || args == "null" {
			args = "{}"
		}
		out[i] = aguitypes.ToolCall{
			ID:   tc.ID,
			Type: aguitypes.ToolCallTypeFunction,
			Function: aguitypes.FunctionCall{
				Name:      tc.Name,
				Arguments: args,
			},
		}
	}
	return out
}

// formatThreadResponse converts a conversation.Thread to the JSON shape
// CopilotKit expects: {id, title, createdAt, updatedAt}.
func formatThreadResponse(t *conversation.Thread) gin.H {
	return gin.H{
		"id":        t.ID,
		"title":     t.Title,
		"createdAt": fmt.Sprintf("%d", t.CreatedAt.UnixMilli()),
		"updatedAt": fmt.Sprintf("%d", t.UpdatedAt.UnixMilli()),
	}
}
