package agui

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
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

// handleThreads implements GET /v1/agents/run/threads — returns thread list.
// CopilotKit probes this endpoint; we return an empty list since threads are
// managed client-side via WS.
func (g *Gateway) handleThreads(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"threads": []any{}})
}

// aguiUnwrapEnvelope handles the CopilotKit single-endpoint envelope format
// where requests arrive as {"method":"agent/run","body":<RunAgentInput>}.
// Returns the inner body bytes if wrapped, or the original bytes otherwise.
func aguiUnwrapEnvelope(c *gin.Context, body []byte) ([]byte, bool) {
	var envelope struct {
		Method string          `json:"method"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body, false
	}
	switch envelope.Method {
	case "info":
		g := &Gateway{}
		_ = g // envelope info is handled by caller
		return nil, true
	case "agent/run", "agent/connect":
		if len(envelope.Body) > 0 {
			return envelope.Body, false
		}
	}
	return body, false
}

// readBody reads and returns the request body, capped at 10 MB.
func readBody(c *gin.Context) ([]byte, error) {
	const maxBody = 10 * 1024 * 1024
	return io.ReadAll(io.LimitReader(c.Request.Body, maxBody))
}
