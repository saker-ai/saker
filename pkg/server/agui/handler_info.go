package agui

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/saker-ai/saker/pkg/conversation"
)

// handleInfo implements GET/POST /v1/agents/run/info — agent discovery.
func (g *Gateway) handleInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"agents": gin.H{
			"default": gin.H{
				"name":        "default",
				"description": "Saker AI Assistant",
			},
		},
	})
}

// handleThreads implements GET /v1/agents/run/threads — returns thread list
// from the conversation store, filtered to the AG-UI client.
func (g *Gateway) handleThreads(c *gin.Context) {
	cs := g.deps.ConversationStore
	if cs == nil {
		c.JSON(http.StatusOK, gin.H{"threads": []any{}})
		return
	}
	identity := identityFromContext(c.Request.Context())
	projectID := identity.ProjectID
	if projectID == "" {
		projectID = "default"
	}
	threads, err := cs.ListThreads(c.Request.Context(), projectID, conversation.ListThreadsOpts{
		Client: aguiClient,
	})
	if err != nil {
		g.deps.Logger.Warn("agui: failed to list threads", "error", err)
		c.JSON(http.StatusOK, gin.H{"threads": []any{}})
		return
	}
	out := make([]gin.H, len(threads))
	for i := range threads {
		out[i] = formatThreadResponse(&threads[i])
	}
	c.JSON(http.StatusOK, gin.H{"threads": out})
}

// handleStop implements POST /v1/agents/run/agent/:agentId/stop/:threadId —
// cancels an in-flight run. Currently a no-op acknowledgement; cancellation
// is handled by the client closing the SSE connection.
func (g *Gateway) handleStop(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

// aguiUnwrapEnvelope handles the CopilotKit single-endpoint envelope format
// where requests arrive as {"method":"agent/run","body":<RunAgentInput>}.
// Returns the inner body bytes if wrapped, or the original bytes otherwise.
func aguiUnwrapEnvelope(c *gin.Context, body []byte) ([]byte, string) {
	var envelope struct {
		Method string          `json:"method"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body, ""
	}
	switch envelope.Method {
	case "info", "threads", "agent/stop":
		return nil, envelope.Method
	case "agent/run":
		if len(envelope.Body) > 0 {
			return envelope.Body, ""
		}
	case "agent/connect":
		return envelope.Body, "agent/connect"
	}
	return body, ""
}

// readBody reads and returns the request body, capped at 10 MB.
func readBody(c *gin.Context) ([]byte, error) {
	const maxBody = 10 * 1024 * 1024
	return io.ReadAll(io.LimitReader(c.Request.Body, maxBody))
}
