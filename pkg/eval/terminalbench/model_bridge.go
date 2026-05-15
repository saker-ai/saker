// Package terminalbench drives the Terminal-Bench 2 evaluation loop. The
// modelBridge here adapts a provider-side model.Model (Complete/CompleteStream)
// into the agent.Model.Generate signature the agent loop expects, holding a
// per-task message.History on the side.
//
// This is intentionally lighter than pkg/api.conversationModel: the evaluator
// does not need hooks, MCP, skills, middleware, prompt-cache, runaway warnings
// or auto-compaction — every task starts with an empty history and lives at
// most one Run.
package terminalbench

import (
	"context"
	"errors"
	"strings"

	"github.com/saker-ai/saker/pkg/agent"
	"github.com/saker-ai/saker/pkg/message"
	"github.com/saker-ai/saker/pkg/model"
)

// modelBridge implements agent.Model on top of model.Model.
type modelBridge struct {
	base    model.Model
	history *message.History
	system  string
	tools   []model.ToolDefinition
	prompt  string // injected once on the first Generate call
	usage   model.Usage
	stop    string
	// perCall captures one model.Usage delta per Generate invocation, so the
	// runner can render a per-iteration token timeline (cache miss spotting,
	// runaway-context detection) without having to wrap every adapter.
	perCall []model.Usage
	// lastErr is the most recent error returned by base.CompleteStream; the
	// runner consumes it to write the verbatim provider failure into the
	// transcript when the agent loop aborts.
	lastErr error
}

func newModelBridge(base model.Model, history *message.History, system, prompt string, tools []model.ToolDefinition) *modelBridge {
	return &modelBridge{
		base:    base,
		history: history,
		system:  system,
		tools:   tools,
		prompt:  prompt,
	}
}

// Generate runs one model turn. On the first call it appends the user prompt
// to history; every subsequent call just re-sends the running transcript so
// the agent loop can keep reasoning over tool results.
func (m *modelBridge) Generate(ctx context.Context, _ *agent.Context) (*agent.ModelOutput, error) {
	if m == nil || m.base == nil {
		return nil, errors.New("terminalbench: model is nil")
	}
	if m.history == nil {
		return nil, errors.New("terminalbench: history is nil")
	}

	if strings.TrimSpace(m.prompt) != "" {
		m.history.Append(message.Message{Role: "user", Content: strings.TrimSpace(m.prompt)})
		m.prompt = ""
	}

	snapshot := m.history.All()
	req := model.Request{
		Messages: convertHistoryToModel(snapshot),
		Tools:    m.tools,
		System:   m.system,
	}

	var resp *model.Response
	if err := m.base.CompleteStream(ctx, req, func(sr model.StreamResult) error {
		if sr.Final && sr.Response != nil {
			resp = sr.Response
		}
		return nil
	}); err != nil {
		m.lastErr = err
		return nil, err
	}
	if resp == nil {
		err := errors.New("terminalbench: model returned no final response")
		m.lastErr = err
		return nil, err
	}
	m.lastErr = nil
	m.perCall = append(m.perCall, resp.Usage)
	m.usage = mergeUsage(m.usage, resp.Usage)
	m.stop = resp.StopReason

	assistant := message.Message{
		Role:             resp.Message.Role,
		Content:          strings.TrimSpace(resp.Message.Content),
		ReasoningContent: resp.Message.ReasoningContent,
	}
	if assistant.Role == "" {
		assistant.Role = "assistant"
	}
	if len(resp.Message.ToolCalls) > 0 {
		assistant.ToolCalls = make([]message.ToolCall, len(resp.Message.ToolCalls))
		for i, tc := range resp.Message.ToolCalls {
			assistant.ToolCalls[i] = message.ToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			}
		}
	}
	m.history.Append(assistant)

	out := &agent.ModelOutput{
		Content: assistant.Content,
		Done:    len(assistant.ToolCalls) == 0,
	}
	if len(assistant.ToolCalls) > 0 {
		out.ToolCalls = make([]agent.ToolCall, len(assistant.ToolCalls))
		for i, tc := range assistant.ToolCalls {
			out.ToolCalls[i] = agent.ToolCall{
				ID:    tc.ID,
				Name:  tc.Name,
				Input: tc.Arguments,
			}
		}
	}
	return out, nil
}

// Usage returns aggregate token usage observed across every model call.
func (m *modelBridge) Usage() model.Usage { return m.usage }

// StopReason returns the most recent stop reason emitted by the provider.
func (m *modelBridge) StopReason() string { return m.stop }

// PerCallUsage returns one model.Usage per Generate invocation, in call order.
func (m *modelBridge) PerCallUsage() []model.Usage { return m.perCall }

// LastError returns the most recent base.CompleteStream failure (or nil).
// Used to surface the verbatim provider error into the per-task transcript
// when the agent loop returns an error.
func (m *modelBridge) LastError() error { return m.lastErr }

// convertHistoryToModel translates message.History records into the provider
// payload, preserving tool calls and content blocks.
func convertHistoryToModel(msgs []message.Message) []model.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]model.Message, len(msgs))
	for i, msg := range msgs {
		converted := model.Message{
			Role:             msg.Role,
			Content:          msg.Content,
			ReasoningContent: msg.ReasoningContent,
		}
		if len(msg.ContentBlocks) > 0 {
			converted.ContentBlocks = make([]model.ContentBlock, len(msg.ContentBlocks))
			for j, b := range msg.ContentBlocks {
				converted.ContentBlocks[j] = model.ContentBlock{
					Type:      model.ContentBlockType(b.Type),
					Text:      b.Text,
					MediaType: b.MediaType,
					Data:      b.Data,
					URL:       b.URL,
				}
			}
		}
		if len(msg.ToolCalls) > 0 {
			converted.ToolCalls = make([]model.ToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				converted.ToolCalls[j] = model.ToolCall{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
					Result:    tc.Result,
				}
			}
		}
		out[i] = converted
	}
	return out
}

func mergeUsage(a, b model.Usage) model.Usage {
	merged := model.Usage{
		InputTokens:         a.InputTokens + b.InputTokens,
		OutputTokens:        a.OutputTokens + b.OutputTokens,
		CacheReadTokens:     a.CacheReadTokens + b.CacheReadTokens,
		CacheCreationTokens: a.CacheCreationTokens + b.CacheCreationTokens,
	}
	merged.TotalTokens = a.TotalTokens + b.TotalTokens
	if merged.TotalTokens == 0 {
		merged.TotalTokens = merged.InputTokens + merged.OutputTokens
	}
	return merged
}
