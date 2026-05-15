package api

import (
	"sort"
	"strings"

	"github.com/saker-ai/saker/pkg/message"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/tool"
)

// runtime_helpers_convert.go owns the data conversion helpers used at the
// boundary between the public api package and the model/tool/message
// packages. The loader/composition helpers live in runtime_helpers_loader.go
// and per-session caches/gates in runtime_helpers_session.go.

func availableTools(registry *tool.Registry, whitelist map[string]struct{}) []model.ToolDefinition {
	if registry == nil {
		return nil
	}
	tools := registry.List()
	defs := make([]model.ToolDefinition, 0, len(tools))
	for _, impl := range tools {
		if impl == nil {
			continue
		}
		name := strings.TrimSpace(impl.Name())
		if name == "" {
			continue
		}
		canon := canonicalToolName(name)
		if canon == "" {
			continue
		}
		if len(whitelist) > 0 {
			if _, ok := whitelist[canon]; !ok {
				continue
			}
		}
		defs = append(defs, model.ToolDefinition{
			Name:        name,
			Description: strings.TrimSpace(impl.Description()),
			Parameters:  schemaToMap(impl.Schema()),
		})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

func schemaToMap(schema *tool.JSONSchema) map[string]any {
	if schema == nil {
		return nil
	}
	payload := map[string]any{}
	if schema.Type != "" {
		payload["type"] = schema.Type
	}
	if len(schema.Properties) > 0 {
		payload["properties"] = schema.Properties
	}
	if len(schema.Required) > 0 {
		payload["required"] = append([]string(nil), schema.Required...)
	}
	return payload
}

func convertMessages(msgs []message.Message) []model.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]model.Message, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, model.Message{
			Role:             msg.Role,
			Content:          msg.Content,
			ContentBlocks:    convertContentBlocksToModel(msg.ContentBlocks),
			ToolCalls:        convertToolCalls(msg.ToolCalls),
			ReasoningContent: msg.ReasoningContent,
		})
	}
	return out
}

func convertContentBlocksToModel(blocks []message.ContentBlock) []model.ContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]model.ContentBlock, len(blocks))
	for i, b := range blocks {
		out[i] = model.ContentBlock{
			Type:      model.ContentBlockType(b.Type),
			Text:      b.Text,
			MediaType: b.MediaType,
			Data:      b.Data,
			URL:       b.URL,
		}
	}
	return out
}

func convertAPIContentBlocks(blocks []model.ContentBlock) []message.ContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]message.ContentBlock, len(blocks))
	for i, b := range blocks {
		out[i] = message.ContentBlock{
			Type:      message.ContentBlockType(b.Type),
			Text:      b.Text,
			MediaType: b.MediaType,
			Data:      b.Data,
			URL:       b.URL,
		}
	}
	return out
}

func convertToolCalls(calls []message.ToolCall) []model.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]model.ToolCall, len(calls))
	for i, call := range calls {
		out[i] = model.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: cloneArguments(call.Arguments),
			Result:    call.Result,
		}
	}
	return out
}

func cloneArguments(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	dup := make(map[string]any, len(args))
	for k, v := range args {
		dup[k] = v
	}
	return dup
}
