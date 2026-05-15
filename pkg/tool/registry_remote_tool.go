package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/mcp"
)

type remoteTool struct {
	name        string
	remoteName  string
	description string
	schema      *JSONSchema
	session     *mcp.ClientSession
	timeout     time.Duration
}

func (r *remoteTool) Name() string        { return r.name }
func (r *remoteTool) Description() string { return r.description }
func (r *remoteTool) Schema() *JSONSchema { return r.schema }

func (r *remoteTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	if r.session == nil {
		return nil, fmt.Errorf("mcp session is nil")
	}
	if params == nil {
		params = map[string]interface{}{}
	}
	callCtx := nonNilContext(ctx)
	if r.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(callCtx, r.timeout)
		defer cancel()
	}
	remoteName := r.remoteName
	if remoteName == "" {
		remoteName = r.name
	}
	res, err := r.session.CallTool(callCtx, &mcp.CallToolParams{
		Name:      remoteName,
		Arguments: params,
	})
	if err != nil {
		if r.timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("mcp tool %s timeout after %s: %w", remoteName, r.timeout, err)
		}
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("MCP call returned nil result")
	}
	output := firstTextContent(res.Content)
	if output == "" {
		if payload, err := json.Marshal(res.Content); err == nil {
			output = string(payload)
		}
	}
	tr := &ToolResult{
		Success: true,
		Output:  output,
		Data:    res.Content,
	}
	// Extract media metadata from MCP content for canvas visualization.
	if media := extractMediaFromMCPContent(res.Content); media != nil {
		tr.Structured = media
	}
	return tr, nil
}

func firstTextContent(content []mcp.Content) string {
	for _, part := range content {
		if txt, ok := part.(*mcp.TextContent); ok {
			return txt.Text
		}
	}
	return ""
}

// extractMediaFromMCPContent detects image/audio content in MCP tool results
// and returns structured metadata for canvas visualization.
func extractMediaFromMCPContent(content []mcp.Content) map[string]any {
	for _, part := range content {
		switch c := part.(type) {
		case *mcp.ImageContent:
			return map[string]any{"media_type": "image", "media_url": "data:" + c.MIMEType + ";base64," + base64Encode(c.Data)}
		case *mcp.AudioContent:
			return map[string]any{"media_type": "audio", "media_url": "data:" + c.MIMEType + ";base64," + base64Encode(c.Data)}
		case *mcp.EmbeddedResource:
			if m := extractMediaFromResource(c.Resource); m != nil {
				return m
			}
		}
	}
	return nil
}

func extractMediaFromResource(rc *mcp.ResourceContents) map[string]any {
	if rc == nil || len(rc.Blob) == 0 {
		return nil
	}
	mime := rc.MIMEType
	encoded := base64Encode(rc.Blob)
	switch {
	case strings.HasPrefix(mime, "image/"):
		return map[string]any{"media_type": "image", "media_url": "data:" + mime + ";base64," + encoded}
	case strings.HasPrefix(mime, "audio/"):
		return map[string]any{"media_type": "audio", "media_url": "data:" + mime + ";base64," + encoded}
	case strings.HasPrefix(mime, "video/"):
		return map[string]any{"media_type": "video", "media_url": "data:" + mime + ";base64," + encoded}
	}
	return nil
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
