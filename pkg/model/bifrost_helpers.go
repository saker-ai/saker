// bifrost_helpers.go: type-conversion helpers between saker model.* types and
// Bifrost's schemas.* wire types. Kept in a separate file so the adapter core
// (bifrost_adapter.go) stays focused on engine lifecycle / streaming flow.
package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// ─── Outbound conversion (saker → Bifrost) ────────────────────────────────────

// buildBifrostMessages converts saker Messages to Bifrost ChatMessages, prepending
// system content as a system role message. Tool messages map to the Bifrost
// `tool` role + ToolCallID; multimodal messages use ContentBlocks.
func buildBifrostMessages(req Request, defaultSystem string) []schemas.ChatMessage {
	out := make([]schemas.ChatMessage, 0, len(req.Messages)+1+len(req.SystemBlocks))

	// System content: SystemBlocks > Request.System > defaultSystem (model.system)
	systemTexts := collectSystemTexts(req, defaultSystem)
	for _, txt := range systemTexts {
		s := txt
		out = append(out, schemas.ChatMessage{
			Role:    schemas.ChatMessageRoleSystem,
			Content: &schemas.ChatMessageContent{ContentStr: &s},
		})
	}

	for _, msg := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system":
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				s := trimmed
				out = append(out, schemas.ChatMessage{
					Role:    schemas.ChatMessageRoleSystem,
					Content: &schemas.ChatMessageContent{ContentStr: &s},
				})
			}
		case "assistant":
			out = append(out, buildAssistantBifrostMessage(msg))
		case "tool":
			out = append(out, buildToolBifrostMessages(msg)...)
		default:
			out = append(out, buildUserBifrostMessage(msg))
		}
	}

	if len(out) == 0 || !hasNonSystemMessage(out) {
		dot := "."
		out = append(out, schemas.ChatMessage{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: &dot},
		})
	}
	return out
}

func collectSystemTexts(req Request, defaultSystem string) []string {
	var texts []string
	if trimmed := strings.TrimSpace(defaultSystem); trimmed != "" {
		texts = append(texts, trimmed)
	}
	if len(req.SystemBlocks) > 0 {
		for _, sb := range req.SystemBlocks {
			if trimmed := strings.TrimSpace(sb); trimmed != "" {
				texts = append(texts, trimmed)
			}
		}
	} else if trimmed := strings.TrimSpace(req.System); trimmed != "" {
		texts = append(texts, trimmed)
	}
	return texts
}

func hasNonSystemMessage(msgs []schemas.ChatMessage) bool {
	for _, m := range msgs {
		if m.Role != schemas.ChatMessageRoleSystem {
			return true
		}
	}
	return false
}

func buildUserBifrostMessage(msg Message) schemas.ChatMessage {
	if len(msg.ContentBlocks) > 0 {
		blocks := make([]schemas.ChatContentBlock, 0, len(msg.ContentBlocks)+1)
		if text := strings.TrimSpace(msg.Content); text != "" {
			t := text
			blocks = append(blocks, schemas.ChatContentBlock{
				Type: schemas.ChatContentBlockTypeText,
				Text: &t,
			})
		}
		blocks = append(blocks, convertSakerContentBlocks(msg.ContentBlocks)...)
		return schemas.ChatMessage{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentBlocks: blocks},
		}
	}
	text := msg.Content
	if strings.TrimSpace(text) == "" {
		text = "."
	}
	return schemas.ChatMessage{
		Role:    schemas.ChatMessageRoleUser,
		Content: &schemas.ChatMessageContent{ContentStr: &text},
	}
}

func buildAssistantBifrostMessage(msg Message) schemas.ChatMessage {
	chatMsg := schemas.ChatMessage{
		Role: schemas.ChatMessageRoleAssistant,
	}

	if strings.TrimSpace(msg.Content) != "" {
		s := msg.Content
		chatMsg.Content = &schemas.ChatMessageContent{ContentStr: &s}
	}

	if len(msg.ToolCalls) > 0 || msg.ReasoningContent != "" {
		am := &schemas.ChatAssistantMessage{}
		if msg.ReasoningContent != "" {
			r := msg.ReasoningContent
			am.Reasoning = &r
		}
		for i, call := range msg.ToolCalls {
			id := strings.TrimSpace(call.ID)
			name := strings.TrimSpace(call.Name)
			if id == "" || name == "" {
				continue
			}
			argsJSON, _ := json.Marshal(call.Arguments)
			toolType := "function"
			tcID := id
			tcName := name
			am.ToolCalls = append(am.ToolCalls, schemas.ChatAssistantMessageToolCall{
				Index: uint16(i),
				Type:  &toolType,
				ID:    &tcID,
				Function: schemas.ChatAssistantMessageToolCallFunction{
					Name:      &tcName,
					Arguments: string(argsJSON),
				},
			})
		}
		chatMsg.ChatAssistantMessage = am
	}

	if chatMsg.Content == nil && chatMsg.ChatAssistantMessage == nil {
		dot := "."
		chatMsg.Content = &schemas.ChatMessageContent{ContentStr: &dot}
	}
	return chatMsg
}

// buildToolBifrostMessages emits one Bifrost `tool` role message per saker
// ToolCall, since Bifrost's ChatToolMessage carries a single ToolCallID.
func buildToolBifrostMessages(msg Message) []schemas.ChatMessage {
	if len(msg.ToolCalls) == 0 {
		// No tool calls — fall back to plain user content with the message text.
		text := msg.Content
		if strings.TrimSpace(text) == "" {
			text = "."
		}
		return []schemas.ChatMessage{{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: &text},
		}}
	}
	out := make([]schemas.ChatMessage, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			continue
		}
		text := call.Result
		if strings.TrimSpace(text) == "" {
			text = msg.Content
		}
		s := text
		idCopy := id
		out = append(out, schemas.ChatMessage{
			Role:            schemas.ChatMessageRoleTool,
			Content:         &schemas.ChatMessageContent{ContentStr: &s},
			ChatToolMessage: &schemas.ChatToolMessage{ToolCallID: &idCopy},
		})
	}
	// Append multimodal blocks (images/docs from tool result) as a follow-up
	// user message so they appear in the same logical turn.
	if len(msg.ContentBlocks) > 0 {
		blocks := convertSakerContentBlocks(msg.ContentBlocks)
		if len(blocks) > 0 {
			out = append(out, schemas.ChatMessage{
				Role:    schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{ContentBlocks: blocks},
			})
		}
	}
	return out
}

func convertSakerContentBlocks(blocks []ContentBlock) []schemas.ChatContentBlock {
	out := make([]schemas.ChatContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case ContentBlockText:
			text := b.Text
			if strings.TrimSpace(text) == "" {
				continue
			}
			t := text
			out = append(out, schemas.ChatContentBlock{
				Type: schemas.ChatContentBlockTypeText,
				Text: &t,
			})
		case ContentBlockImage:
			url := b.URL
			if url == "" && b.Data != "" {
				media := b.MediaType
				if media == "" {
					media = "image/jpeg"
				}
				url = "data:" + media + ";base64," + b.Data
			}
			if url == "" {
				continue
			}
			out = append(out, schemas.ChatContentBlock{
				Type:           schemas.ChatContentBlockTypeImage,
				ImageURLStruct: &schemas.ChatInputImage{URL: url},
			})
		case ContentBlockDocument:
			file := &schemas.ChatInputFile{}
			if b.Data != "" {
				data := b.Data
				file.FileData = &data
			}
			if b.URL != "" {
				url := b.URL
				file.FileURL = &url
			}
			if b.MediaType != "" {
				ft := b.MediaType
				file.FileType = &ft
			}
			if file.FileData == nil && file.FileURL == nil && file.FileID == nil {
				continue
			}
			out = append(out, schemas.ChatContentBlock{
				Type: schemas.ChatContentBlockTypeFile,
				File: file,
			})
		}
	}
	return out
}

// applyBifrostCacheControl mirrors anthropic_request.go:291-325 — mark first +
// last system blocks ephemeral, plus the last text block of the most recent
// 3 user messages. Bifrost forwards CacheControl to Anthropic provider only;
// other providers will ignore it.
func applyBifrostCacheControl(messages []schemas.ChatMessage) {
	// Find system message indices.
	var sysIdx []int
	for i, m := range messages {
		if m.Role == schemas.ChatMessageRoleSystem {
			sysIdx = append(sysIdx, i)
		}
	}
	if len(sysIdx) > 0 {
		markLastSystemTextEphemeral(&messages[sysIdx[len(sysIdx)-1]])
		if len(sysIdx) > 1 {
			markLastSystemTextEphemeral(&messages[sysIdx[0]])
		}
	}

	userMarked := 0
	for i := len(messages) - 1; i >= 0 && userMarked < 3; i-- {
		if messages[i].Role != schemas.ChatMessageRoleUser {
			continue
		}
		if markLastUserTextEphemeral(&messages[i]) {
			userMarked++
		}
	}
}

// markLastSystemTextEphemeral converts a system message's ContentStr into a
// single ephemeral text ContentBlock, or marks the last text block ephemeral
// when ContentBlocks is already populated.
func markLastSystemTextEphemeral(msg *schemas.ChatMessage) {
	if msg == nil || msg.Content == nil {
		return
	}
	if msg.Content.ContentStr != nil && *msg.Content.ContentStr != "" {
		text := *msg.Content.ContentStr
		blocks := []schemas.ChatContentBlock{{
			Type:         schemas.ChatContentBlockTypeText,
			Text:         &text,
			CacheControl: &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral},
		}}
		msg.Content = &schemas.ChatMessageContent{ContentBlocks: blocks}
		return
	}
	if msg.Content.ContentBlocks != nil {
		for j := len(msg.Content.ContentBlocks) - 1; j >= 0; j-- {
			b := &msg.Content.ContentBlocks[j]
			if b.Type == schemas.ChatContentBlockTypeText && b.Text != nil && *b.Text != "" {
				b.CacheControl = &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral}
				return
			}
		}
	}
}

// markLastUserTextEphemeral marks the last text block of a user message
// ephemeral. Returns true when a marker was applied (counting toward the 3-msg
// budget). Promotes ContentStr to ContentBlocks lazily.
func markLastUserTextEphemeral(msg *schemas.ChatMessage) bool {
	if msg == nil || msg.Content == nil {
		return false
	}
	if msg.Content.ContentStr != nil && *msg.Content.ContentStr != "" {
		text := *msg.Content.ContentStr
		blocks := []schemas.ChatContentBlock{{
			Type:         schemas.ChatContentBlockTypeText,
			Text:         &text,
			CacheControl: &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral},
		}}
		msg.Content = &schemas.ChatMessageContent{ContentBlocks: blocks}
		return true
	}
	if msg.Content.ContentBlocks != nil {
		for j := len(msg.Content.ContentBlocks) - 1; j >= 0; j-- {
			b := &msg.Content.ContentBlocks[j]
			if b.Type == schemas.ChatContentBlockTypeText && b.Text != nil && *b.Text != "" {
				b.CacheControl = &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral}
				return true
			}
		}
	}
	return false
}

// buildBifrostTools maps saker ToolDefinitions to Bifrost ChatTool[function].
// Parameters become a JSON-Schema-shaped ToolFunctionParameters with an
// OrderedMap properties — providers (esp. OpenAI) are sensitive to key order.
func buildBifrostTools(tools []ToolDefinition) []schemas.ChatTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]schemas.ChatTool, 0, len(tools))
	for _, def := range tools {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		fn := &schemas.ChatToolFunction{Name: name}
		if d := strings.TrimSpace(def.Description); d != "" {
			desc := d
			fn.Description = &desc
		}
		if params := buildToolParameters(def.Parameters); params != nil {
			fn.Parameters = params
		}
		out = append(out, schemas.ChatTool{
			Type:     schemas.ChatToolTypeFunction,
			Function: fn,
		})
	}
	return out
}

func buildToolParameters(raw map[string]any) *schemas.ToolFunctionParameters {
	if len(raw) == 0 {
		return &schemas.ToolFunctionParameters{
			Type:       "object",
			Properties: schemas.NewOrderedMap(),
		}
	}
	params := &schemas.ToolFunctionParameters{Type: "object"}
	if t, ok := raw["type"].(string); ok && t != "" {
		params.Type = t
	}
	if d, ok := raw["description"].(string); ok && d != "" {
		desc := d
		params.Description = &desc
	}
	if props, ok := raw["properties"].(map[string]any); ok {
		params.Properties = schemas.OrderedMapFromMap(props)
	} else {
		params.Properties = schemas.NewOrderedMap()
	}
	switch req := raw["required"].(type) {
	case []string:
		params.Required = append([]string(nil), req...)
	case []any:
		for _, v := range req {
			if s, ok := v.(string); ok {
				params.Required = append(params.Required, s)
			}
		}
	}
	return params
}

// ─── Inbound conversion (Bifrost → saker) ─────────────────────────────────────

// convertBifrostMessage flattens Bifrost's ChatMessage (with its embedded
// ChatAssistantMessage / ChatToolMessage) into saker's flat Message struct.
func convertBifrostMessage(msg *schemas.ChatMessage) Message {
	if msg == nil {
		return Message{Role: string(schemas.ChatMessageRoleAssistant)}
	}
	out := Message{Role: string(msg.Role)}
	if out.Role == "" {
		out.Role = string(schemas.ChatMessageRoleAssistant)
	}

	if msg.Content != nil {
		if msg.Content.ContentStr != nil {
			out.Content = *msg.Content.ContentStr
		}
		if len(msg.Content.ContentBlocks) > 0 {
			var sb strings.Builder
			for _, b := range msg.Content.ContentBlocks {
				if b.Type == schemas.ChatContentBlockTypeText && b.Text != nil {
					sb.WriteString(*b.Text)
				}
			}
			if out.Content == "" {
				out.Content = sb.String()
			}
		}
	}

	if msg.ChatAssistantMessage != nil {
		am := msg.ChatAssistantMessage
		if am.Reasoning != nil {
			out.ReasoningContent = *am.Reasoning
		}
		for _, tc := range am.ToolCalls {
			if call := bifrostToolCallToSaker(tc); call != nil {
				out.ToolCalls = append(out.ToolCalls, *call)
			}
		}
	}
	return out
}

func bifrostToolCallToSaker(tc schemas.ChatAssistantMessageToolCall) *ToolCall {
	id := ""
	if tc.ID != nil {
		id = *tc.ID
	}
	name := ""
	if tc.Function.Name != nil {
		name = *tc.Function.Name
	}
	if id == "" && name == "" && tc.Function.Arguments == "" {
		return nil
	}
	args := decodeToolArgs(tc.Function.Arguments)
	return &ToolCall{
		ID:        id,
		Name:      name,
		Arguments: args,
	}
}

func decodeToolArgs(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		// Fall back to wrapping the raw string so callers always get a non-nil
		// map with the original payload preserved for debugging.
		return map[string]any{"_raw": trimmed}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

func convertBifrostUsage(u *schemas.BifrostLLMUsage) Usage {
	if u == nil {
		return Usage{}
	}
	out := Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.PromptTokensDetails != nil {
		out.CacheReadTokens = u.PromptTokensDetails.CachedReadTokens
		out.CacheCreationTokens = u.PromptTokensDetails.CachedWriteTokens
	}
	return out
}

// bifrostStatusError wraps a Bifrost error message with its HTTP status code so
// saker's ClassifyError can extract the status via the HTTPStatusError
// interface. The underlying message is preserved verbatim so string-matchers
// (e.g. compact_restore.go's isPromptTooLong) keep working unchanged.
type bifrostStatusError struct {
	msg    string
	status int
}

func (e *bifrostStatusError) Error() string       { return e.msg }
func (e *bifrostStatusError) HTTPStatusCode() int { return e.status }

// mapBifrostError preserves the original Bifrost error message so downstream
// string-matchers (e.g. compact_restore.go's isPromptTooLong) keep working,
// and attaches the HTTP status code so ClassifyError picks the right reason.
func mapBifrostError(berr *schemas.BifrostError) error {
	if berr == nil {
		return errors.New("bifrost: unknown error")
	}
	status := 0
	if berr.StatusCode != nil {
		status = *berr.StatusCode
	}
	msg := ""
	if berr.Error != nil {
		if berr.Error.Error != nil && berr.Error.Error.Error() != "" {
			msg = berr.Error.Error.Error()
		} else if berr.Error.Message != "" {
			msg = berr.Error.Message
		}
	}
	if msg == "" && status > 0 {
		msg = fmt.Sprintf("bifrost: http %d", status)
	}
	if msg == "" {
		msg = "bifrost: unknown error"
	}
	if status > 0 {
		return &bifrostStatusError{msg: msg, status: status}
	}
	return errors.New(msg)
}

// ClassifyBifrostError classifies a typed Bifrost error using saker's
// FailoverReason taxonomy. Convenience wrapper for callers that already hold
// a *schemas.BifrostError; identical to ClassifyError(mapBifrostError(berr)).
func ClassifyBifrostError(berr *schemas.BifrostError) ClassifiedError {
	return ClassifyError(mapBifrostError(berr))
}

// ─── Tool-call accumulator (stream deltas → finalized calls) ──────────────────

// bifrostCompletedToolCall carries the per-index aggregation state. Bifrost's
// stream chunks deliver tool-call args as incremental string deltas keyed by
// Index; we concatenate args, latch ID/name on first sight, and emit once
// finalized. Named separately from openai_stream.go's toolCallAccumulator to
// avoid a collision while both adapters coexist.
type bifrostCompletedToolCall struct {
	idx     uint16
	id      string
	name    string
	argsBuf strings.Builder
}

func (c *bifrostCompletedToolCall) toToolCall() *ToolCall {
	id := strings.TrimSpace(c.id)
	name := strings.TrimSpace(c.name)
	if id == "" && name == "" {
		return nil
	}
	return &ToolCall{
		ID:        id,
		Name:      name,
		Arguments: decodeToolArgs(c.argsBuf.String()),
	}
}

type bifrostToolCallAccumulator struct {
	order []uint16
	items map[uint16]*bifrostCompletedToolCall
}

func newBifrostToolCallAccumulator() *bifrostToolCallAccumulator {
	return &bifrostToolCallAccumulator{items: make(map[uint16]*bifrostCompletedToolCall)}
}

func (t *bifrostToolCallAccumulator) Append(tc schemas.ChatAssistantMessageToolCall) {
	cur, ok := t.items[tc.Index]
	if !ok {
		cur = &bifrostCompletedToolCall{idx: tc.Index}
		t.items[tc.Index] = cur
		t.order = append(t.order, tc.Index)
	}
	if tc.ID != nil && *tc.ID != "" && cur.id == "" {
		cur.id = *tc.ID
	}
	if tc.Function.Name != nil && *tc.Function.Name != "" && cur.name == "" {
		cur.name = *tc.Function.Name
	}
	if tc.Function.Arguments != "" {
		cur.argsBuf.WriteString(tc.Function.Arguments)
	}
}

func (t *bifrostToolCallAccumulator) All() []*bifrostCompletedToolCall {
	out := make([]*bifrostCompletedToolCall, 0, len(t.order))
	for _, idx := range t.order {
		if c, ok := t.items[idx]; ok {
			out = append(out, c)
		}
	}
	return out
}

// Drain returns the same slice as All — kept as a separate method for caller
// readability (Drain is invoked when the stream signals completion).
func (t *bifrostToolCallAccumulator) Drain() []*bifrostCompletedToolCall {
	return t.All()
}
