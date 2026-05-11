// anthropic_request.go: request payload construction (headers, params, message/tool/system encoding, retry, model lookup).
package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/security"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

var anthropicPredefinedHeaders = map[string]string{
	"accept":         "application/json",
	"anthropic-beta": "interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14",
	"anthropic-dangerous-direct-browser-access": "true",
	"anthropic-version":                         "2023-06-01",
	"content-type":                              "application/json",
	"user-agent":                                "claude-cli/2.0.34 (external, cli)",
	"x-app":                                     "cli",
	"x-stainless-arch":                          "arm64",
	"x-stainless-helper-method":                 "stream",
	"x-stainless-lang":                          "js",
	"x-stainless-os":                            "MacOS",
	"x-stainless-package-version":               "0.68.0",
	"x-stainless-retry-count":                   "0",
	"x-stainless-runtime":                       "node",
	"x-stainless-runtime-version":               "v22.20.0",
	"x-stainless-timeout":                       "600",
}

func anthropicCustomHeadersEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ANTHROPIC_CUSTOM_HEADERS_ENABLED")), "true")
}

func newAnthropicHeaders(defaults, overrides map[string]string) map[string]string {
	merge := func(dst map[string]string, src map[string]string) {
		for k, v := range src {
			norm := strings.ToLower(strings.TrimSpace(k))
			if norm == "" || norm == "x-api-key" {
				continue
			}
			dst[norm] = v
		}
	}

	merged := make(map[string]string)
	if anthropicCustomHeadersEnabled() {
		merge(merged, anthropicPredefinedHeaders)
	}
	merge(merged, defaults)
	merge(merged, overrides)

	if len(merged) == 0 {
		return nil
	}
	return merged
}

func (m *anthropicModel) requestOptions() []option.RequestOption {
	headers := newAnthropicHeaders(nil, nil)

	apiKey := strings.TrimSpace(m.configuredAPIKey)
	if apiKey == "" {
		if envKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); envKey != "" {
			apiKey = envKey
		} else if authToken := strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")); authToken != "" {
			apiKey = authToken
		}
	}
	apiKey = security.ResolveEnv(apiKey)
	if apiKey != "" {
		if headers == nil {
			headers = make(map[string]string)
		}
		headers["x-api-key"] = apiKey
	}

	if len(headers) == 0 {
		return nil
	}
	opts := make([]option.RequestOption, 0, len(headers))
	for key, value := range headers {
		if value == "" {
			continue
		}
		opts = append(opts, option.WithHeader(key, value))
	}
	return opts
}

func (m *anthropicModel) buildParams(req Request) (anthropicsdk.MessageNewParams, error) {
	// Determine system prompt sources: SystemBlocks takes precedence over System.
	systemDefaults := []string{m.system}
	if len(req.SystemBlocks) > 0 {
		systemDefaults = append(systemDefaults, req.SystemBlocks...)
	} else if req.System != "" {
		systemDefaults = append(systemDefaults, req.System)
	}
	systemBlocks, messageParams, err := convertMessages(req.Messages, req.EnablePromptCache, systemDefaults...)
	if err != nil {
		return anthropicsdk.MessageNewParams{}, err
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = m.maxTokens
	}

	params := anthropicsdk.MessageNewParams{
		Model:     m.selectModel(req.Model),
		MaxTokens: int64(maxTokens),
		Messages:  messageParams,
	}

	if len(systemBlocks) > 0 {
		params.System = systemBlocks
	}

	if len(req.Tools) > 0 {
		tools, err := convertTools(req.Tools)
		if err != nil {
			return anthropicsdk.MessageNewParams{}, err
		}
		params.Tools = tools
	}

	if m.temperature != nil {
		params.Temperature = param.NewOpt(*m.temperature)
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}

	if sessionID := strings.TrimSpace(req.SessionID); sessionID != "" {
		params.Metadata = anthropicsdk.MetadataParam{
			UserID: param.NewOpt(sessionID),
		}
	}

	return params, nil
}

func (m *anthropicModel) doWithRetry(ctx context.Context, fn func(context.Context) error) error {
	attempts := 0
	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		// Check context before deciding to retry
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isRetryable(err) || attempts >= m.maxRetries {
			return err
		}
		attempts++
		backoff := time.Duration(attempts*attempts) * 100 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *anthropicsdk.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode != http.StatusUnauthorized
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		//nolint:staticcheck // Temporary is deprecated but retained to treat non-timeout transient errors as retryable (tests rely on this behaviour).
		return netErr.Temporary()
	}
	return true
}

func (m *anthropicModel) selectModel(override string) anthropicsdk.Model {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return mapModelName(trimmed)
	}
	return m.model
}

func (m *anthropicModel) countParams(params anthropicsdk.MessageNewParams) anthropicsdk.MessageCountTokensParams {
	cp := anthropicsdk.MessageCountTokensParams{
		Messages: params.Messages,
		Model:    params.Model,
	}
	if len(params.System) > 0 {
		cp.System = anthropicsdk.MessageCountTokensParamsSystemUnion{OfTextBlockArray: params.System}
	}
	if len(params.Tools) > 0 {
		cp.Tools = convertCountTools(params.Tools)
	}
	return cp
}

func convertCountTools(tools []anthropicsdk.ToolUnionParam) []anthropicsdk.MessageCountTokensToolUnionParam {
	out := make([]anthropicsdk.MessageCountTokensToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		if tool.OfTool != nil {
			out = append(out, anthropicsdk.MessageCountTokensToolUnionParam{OfTool: tool.OfTool})
		}
	}
	return out
}

func convertMessages(msgs []Message, enableCache bool, defaults ...string) ([]anthropicsdk.TextBlockParam, []anthropicsdk.MessageParam, error) {
	var systemBlocks []anthropicsdk.TextBlockParam
	for _, sys := range defaults {
		if trimmed := strings.TrimSpace(sys); trimmed != "" {
			systemBlocks = append(systemBlocks, anthropicsdk.TextBlockParam{Text: trimmed})
		}
	}

	messageParams := make([]anthropicsdk.MessageParam, 0, len(msgs))
	for _, msg := range msgs {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system":
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				systemBlocks = append(systemBlocks, anthropicsdk.TextBlockParam{Text: trimmed})
			}
			continue
		case "assistant":
			content := buildAssistantContent(msg)
			messageParams = append(messageParams, anthropicsdk.MessageParam{
				Role:    anthropicsdk.MessageParamRoleAssistant,
				Content: content,
			})
		case "tool":
			content := buildToolResults(msg)
			messageParams = append(messageParams, anthropicsdk.MessageParam{
				Role:    anthropicsdk.MessageParamRoleUser,
				Content: content,
			})
		default:
			var content []anthropicsdk.ContentBlockParamUnion
			if len(msg.ContentBlocks) > 0 {
				// Include text content alongside content blocks when both exist
				if text := strings.TrimSpace(msg.Content); text != "" {
					content = append(content, anthropicsdk.NewTextBlock(text))
				}
				content = append(content, convertContentBlocks(msg.ContentBlocks)...)
			} else {
				text := msg.Content
				if strings.TrimSpace(text) == "" {
					text = "."
				}
				content = []anthropicsdk.ContentBlockParamUnion{
					anthropicsdk.NewTextBlock(text),
				}
			}
			messageParams = append(messageParams, anthropicsdk.MessageParam{
				Role:    anthropicsdk.MessageParamRoleUser,
				Content: content,
			})
		}
	}

	if len(messageParams) == 0 {
		messageParams = append(messageParams, anthropicsdk.MessageParam{
			Role: anthropicsdk.MessageParamRoleUser,
			Content: []anthropicsdk.ContentBlockParamUnion{
				anthropicsdk.NewTextBlock("."),
			},
		})
	}

	// Apply cache control if enabled
	if enableCache {
		// Mark system blocks for caching. When multiple blocks exist,
		// mark the first (static content) and last (dynamic content) for
		// dual cache breakpoints — matching Claude Code's strategy.
		if len(systemBlocks) > 0 {
			systemBlocks[len(systemBlocks)-1].CacheControl = anthropicsdk.NewCacheControlEphemeralParam()
			if len(systemBlocks) > 1 {
				systemBlocks[0].CacheControl = anthropicsdk.NewCacheControlEphemeralParam()
			}
		}

		// Mark the last 2-3 user messages for caching to optimize multi-turn conversations
		userMsgCount := 0
		for i := len(messageParams) - 1; i >= 0 && userMsgCount < 3; i-- {
			if messageParams[i].Role == anthropicsdk.MessageParamRoleUser && len(messageParams[i].Content) > 0 {
				// Walk backward through content blocks to find the last text block
				cached := false
				for j := len(messageParams[i].Content) - 1; j >= 0; j-- {
					if text := messageParams[i].Content[j].GetText(); text != nil && *text != "" {
						messageParams[i].Content[j] = anthropicsdk.ContentBlockParamUnion{
							OfText: &anthropicsdk.TextBlockParam{
								Text:         *text,
								CacheControl: anthropicsdk.NewCacheControlEphemeralParam(),
							},
						}
						cached = true
						break
					}
				}
				if cached {
					userMsgCount++
				}
			}
		}
	}

	return systemBlocks, messageParams, nil
}

func buildAssistantContent(msg Message) []anthropicsdk.ContentBlockParamUnion {
	blocks := make([]anthropicsdk.ContentBlockParamUnion, 0, 1+len(msg.ToolCalls))
	// Prepend thinking block if reasoning content is present
	if msg.ReasoningContent != "" {
		blocks = append(blocks, anthropicsdk.NewThinkingBlock("", msg.ReasoningContent))
	}
	if strings.TrimSpace(msg.Content) != "" {
		blocks = append(blocks, anthropicsdk.NewTextBlock(msg.Content))
	}
	for _, call := range msg.ToolCalls {
		id := strings.TrimSpace(call.ID)
		name := strings.TrimSpace(call.Name)
		if id == "" || name == "" {
			continue
		}
		blocks = append(blocks, anthropicsdk.NewToolUseBlock(id, cloneValue(call.Arguments), name))
	}
	if len(blocks) == 0 {
		blocks = append(blocks, anthropicsdk.NewTextBlock("."))
	}
	return blocks
}

func buildToolResults(msg Message) []anthropicsdk.ContentBlockParamUnion {
	if len(msg.ToolCalls) == 0 {
		return []anthropicsdk.ContentBlockParamUnion{
			anthropicsdk.NewTextBlock(msg.Content),
		}
	}

	blocks := make([]anthropicsdk.ContentBlockParamUnion, 0, len(msg.ToolCalls)+len(msg.ContentBlocks))
	for _, call := range msg.ToolCalls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			continue
		}
		text := call.Result
		if strings.TrimSpace(text) == "" {
			text = msg.Content
		}
		blocks = append(blocks, anthropicsdk.NewToolResultBlock(id, text, toolResultIsError(text)))
	}
	if len(blocks) == 0 {
		blocks = append(blocks, anthropicsdk.NewTextBlock(msg.Content))
	}

	// Append multimodal content blocks (images, documents) from tool results
	// so they appear in the same user turn as the tool_result.
	if len(msg.ContentBlocks) > 0 {
		blocks = append(blocks, convertContentBlocks(msg.ContentBlocks)...)
	}

	return blocks
}

// convertContentBlocks maps SDK ContentBlocks to Anthropic API content blocks.
func convertContentBlocks(blocks []ContentBlock) []anthropicsdk.ContentBlockParamUnion {
	out := make([]anthropicsdk.ContentBlockParamUnion, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case ContentBlockText:
			text := b.Text
			if strings.TrimSpace(text) == "" {
				text = "."
			}
			out = append(out, anthropicsdk.NewTextBlock(text))
		case ContentBlockImage:
			if b.URL != "" {
				out = append(out, anthropicsdk.NewImageBlock(anthropicsdk.URLImageSourceParam{URL: b.URL}))
			} else if b.Data != "" {
				out = append(out, anthropicsdk.NewImageBlockBase64(b.MediaType, b.Data))
			}
		case ContentBlockDocument:
			if b.Data != "" {
				out = append(out, anthropicsdk.NewDocumentBlock(anthropicsdk.Base64PDFSourceParam{Data: b.Data}))
			}
		default:
			slog.Warn("unknown content block type", "type", b.Type, "action", "skipping")
		}
	}
	if len(out) == 0 {
		out = append(out, anthropicsdk.NewTextBlock("."))
	}
	return out
}

func toolResultIsError(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return false
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return false
	}

	val, ok := payload["error"]
	if !ok {
		return false
	}

	switch t := val.(type) {
	case bool:
		return t
	case string:
		return strings.TrimSpace(t) != ""
	default:
		return t != nil
	}
}

func convertTools(tools []ToolDefinition) ([]anthropicsdk.ToolUnionParam, error) {
	out := make([]anthropicsdk.ToolUnionParam, 0, len(tools))
	for _, def := range tools {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}

		schema, err := encodeSchema(def.Parameters)
		if err != nil {
			return nil, fmt.Errorf("tool %s schema: %w", name, err)
		}

		tool := anthropicsdk.ToolParam{
			Name:        name,
			InputSchema: schema,
		}
		if strings.TrimSpace(def.Description) != "" {
			tool.Description = anthropicsdk.String(def.Description)
		}

		out = append(out, anthropicsdk.ToolUnionParam{OfTool: &tool})
	}
	return out, nil
}

func encodeSchema(raw map[string]any) (anthropicsdk.ToolInputSchemaParam, error) {
	if len(raw) == 0 {
		return anthropicsdk.ToolInputSchemaParam{Type: "object"}, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return anthropicsdk.ToolInputSchemaParam{}, err
	}
	var schema anthropicsdk.ToolInputSchemaParam
	if err := json.Unmarshal(data, &schema); err != nil {
		return anthropicsdk.ToolInputSchemaParam{}, err
	}
	if schema.Type == "" {
		schema.Type = "object"
	}
	return schema, nil
}

func cloneValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		cp := make(map[string]any, len(val))
		for k, v := range val {
			cp[k] = cloneValue(v)
		}
		return cp
	case []any:
		cp := make([]any, len(val))
		for i, el := range val {
			cp[i] = cloneValue(el)
		}
		return cp
	default:
		return val
	}
}

const defaultAnthropicModel = anthropicsdk.ModelClaudeSonnet4_5_20250929

var supportedAnthropicModels = []anthropicsdk.Model{
	anthropicsdk.ModelClaude3_7SonnetLatest,   //nolint:staticcheck // deprecated but still accepted
	anthropicsdk.ModelClaude3_7Sonnet20250219, //nolint:staticcheck // deprecated but still accepted
	anthropicsdk.ModelClaude3_5HaikuLatest,
	anthropicsdk.ModelClaude3_5Haiku20241022,
	anthropicsdk.ModelClaudeHaiku4_5,
	anthropicsdk.ModelClaudeHaiku4_5_20251001,
	anthropicsdk.ModelClaudeSonnet4_20250514,
	anthropicsdk.ModelClaudeSonnet4_0,
	anthropicsdk.ModelClaude4Sonnet20250514,
	anthropicsdk.ModelClaudeSonnet4_5,
	anthropicsdk.ModelClaudeSonnet4_5_20250929,
	anthropicsdk.ModelClaudeOpus4_0,
	anthropicsdk.ModelClaudeOpus4_20250514,
	anthropicsdk.ModelClaude4Opus20250514,
	anthropicsdk.ModelClaudeOpus4_1_20250805,
	anthropicsdk.ModelClaude3OpusLatest,      //nolint:staticcheck // deprecated but still accepted
	anthropicsdk.ModelClaude_3_Opus_20240229, //nolint:staticcheck // deprecated but still accepted
	anthropicsdk.ModelClaude_3_Haiku_20240307,
}

var modelLookup = func() map[string]anthropicsdk.Model {
	lookup := make(map[string]anthropicsdk.Model, len(supportedAnthropicModels))
	for _, model := range supportedAnthropicModels {
		lookup[string(model)] = model
	}
	return lookup
}()

func mapModelName(name string) anthropicsdk.Model {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return defaultAnthropicModel
	}
	if model, ok := modelLookup[trimmed]; ok {
		return model
	}
	// Pass through unknown model names (e.g. deepseek-reasoner via proxy)
	return anthropicsdk.Model(trimmed)
}
