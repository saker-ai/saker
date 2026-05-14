package openai

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cinience/saker/pkg/runhub"
	"github.com/gin-gonic/gin"
)

// SubmitToolOutputsRequest is the body for POST /v1/runs/:id/submit.
type SubmitToolOutputsRequest struct {
	ToolOutputs []ToolOutput `json:"tool_outputs"`
	Stream      bool         `json:"stream,omitempty"`
}

// ToolOutput carries a single tool call response.
type ToolOutput struct {
	ToolCallID string `json:"tool_call_id"`
	Output     string `json:"output"`
}

// handleRunsSubmit implements POST /v1/runs/:id/submit — delivers a tool
// response to a paused run and optionally streams the continued output.
func (g *Gateway) handleRunsSubmit(c *gin.Context) {
	runID := c.Param("id")
	if runID == "" {
		InvalidRequest(c, "missing run id")
		return
	}

	hubRun, err := g.hub.Get(runID)
	if err != nil {
		if errors.Is(err, runhub.ErrNotFound) {
			NotFound(c, "no such run")
			return
		}
		ServerError(c, "failed to load run: "+err.Error())
		return
	}

	identity := IdentityFromContext(c.Request.Context())
	if !runOwnedByIdentity(hubRun, identity) {
		NotFound(c, "no such run")
		return
	}

	var req SubmitToolOutputsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		InvalidRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	if len(req.ToolOutputs) == 0 {
		InvalidRequestField(c, "tool_outputs", "at least one tool_output is required")
		return
	}

	pa := g.pendingAsks.Lookup(runID)
	if pa == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "run is not awaiting tool output",
			"type":    "invalid_request_error",
			"code":    "unexpected_tool_response",
		}})
		return
	}

	// Find the matching tool output.
	var matched *ToolOutput
	for i := range req.ToolOutputs {
		if req.ToolOutputs[i].ToolCallID == pa.ToolCallID {
			matched = &req.ToolOutputs[i]
			break
		}
	}
	if matched == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "tool_call_id mismatch: none of the provided tool_outputs match the pending call " + pa.ToolCallID,
			"type":    "invalid_request_error",
			"code":    "tool_call_id_mismatch",
		}})
		return
	}

	// Parse the output string as answers.
	answers, action, err := parseToolResponse(json.RawMessage(matched.Output))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "invalid_tool_response_format: " + err.Error(),
			"type":    "invalid_request_error",
			"code":    "invalid_tool_response_format",
		}})
		return
	}

	c.Writer.Header().Set("X-Saker-Run-Id", hubRun.ID)

	newCh := pa.Pause.Reset()

	// Deliver answer — unblocks the askFn goroutine.
	select {
	case pa.AnswerCh <- askAnswer{Answers: answers, Action: action}:
	case <-c.Request.Context().Done():
		ServerError(c, "client disconnected before answer could be delivered")
		return
	}

	if req.Stream {
		g.streamChatSSE(c, hubRun, ExtraBody{}, false, newCh)
	} else {
		// Wait for the run to complete and return the aggregated response.
		g.streamChatSync(c, hubRun, ExtraBody{}, hubRun.ID, makeChatChunkID(hubRun.ID))
	}
}
