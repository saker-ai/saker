package agui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/saker-ai/saker/pkg/api"

	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
)

// messagesToRequest converts AG-UI RunAgentInput into a saker api.Request.
// Uses the ThreadID as SessionID so saker's runtime manages conversation
// history across turns. The latest user message becomes the Prompt.
func messagesToRequest(input aguitypes.RunAgentInput, identity Identity) api.Request {
	var prompt string
	for i := len(input.Messages) - 1; i >= 0; i-- {
		if input.Messages[i].Role == aguitypes.RoleUser {
			prompt = extractTextContent(input.Messages[i].Content)
			break
		}
	}

	if len(input.Context) > 0 {
		var parts []string
		for _, ctx := range input.Context {
			parts = append(parts, fmt.Sprintf("%s: %s", ctx.Description, ctx.Value))
		}
		prompt = strings.Join(parts, "\n") + "\n\n" + prompt
	}

	req := api.Request{
		Prompt:    prompt,
		SessionID: input.ThreadID,
	}
	if identity.Username != "" {
		req.User = identity.Username
	}

	return req
}

// extractTextContent coerces AG-UI message content (typed as any) into a
// plain string. Handles: string, nil, []InputContent (joins text parts),
// and arbitrary JSON.
func extractTextContent(content any) string {
	if content == nil {
		return ""
	}
	switch v := content.(type) {
	case string:
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		var s string
		if json.Unmarshal(b, &s) == nil {
			return s
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(b, &parts) == nil {
			var texts []string
			for _, p := range parts {
				if p.Type == "text" && p.Text != "" {
					texts = append(texts, p.Text)
				}
			}
			if len(texts) > 0 {
				return strings.Join(texts, "\n")
			}
		}
		return string(b)
	}
}
