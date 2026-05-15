package terminalbench

import (
	"context"
	"errors"
	"fmt"

	"github.com/saker-ai/saker/pkg/agent"
	"github.com/saker-ai/saker/pkg/message"
	"github.com/saker-ai/saker/pkg/tool"
)

// historyToolExecutor wraps tool.Executor with the bookkeeping the agent loop
// depends on: every tool call (success or failure) gets a matching tool_result
// entry appended to the conversation history so the next model.Generate has
// the result available.
//
// Mirrors pkg/api/runtime_tools.go::runtimeToolExecutor.appendToolResult, but
// without hooks/permissions/streaming — the evaluator is fully autonomous.
type historyToolExecutor struct {
	executor *tool.Executor
	history  *message.History
	root     string
}

func newHistoryToolExecutor(exec *tool.Executor, history *message.History, root string) *historyToolExecutor {
	return &historyToolExecutor{executor: exec, history: history, root: root}
}

func (h *historyToolExecutor) Execute(ctx context.Context, call agent.ToolCall, _ *agent.Context) (agent.ToolResult, error) {
	appendResult := func(content string) {
		if h.history == nil {
			return
		}
		h.history.Append(message.Message{
			Role: "tool",
			ToolCalls: []message.ToolCall{{
				ID:        call.ID,
				Name:      call.Name,
				Arguments: call.Input,
				Result:    content,
			}},
		})
	}

	if h.executor == nil {
		err := errors.New("terminalbench: tool executor not initialised")
		appendResult(fmt.Sprintf("Tool execution failed: %v", err))
		return agent.ToolResult{Name: call.Name}, err
	}

	spec := tool.Call{
		Name:   call.Name,
		Params: call.Input,
		Path:   h.root,
	}
	result, err := h.executor.Execute(ctx, spec)

	out := agent.ToolResult{Name: call.Name}
	meta := map[string]any{}
	content := ""
	if result != nil && result.Result != nil {
		out.Output = result.Result.Output
		content = result.Result.Output
		if result.Result.Data != nil {
			meta["data"] = result.Result.Data
		}
	}
	if err != nil {
		meta["error"] = err.Error()
		content = fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	if len(meta) > 0 {
		out.Metadata = meta
	}
	appendResult(content)
	return out, err
}
