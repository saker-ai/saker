package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/saker-ai/saker/pkg/tool"
)

const killTaskDescription = `Terminates a running asynchronous bash task started with bash(async=true).

Use this when:
- A long-running background process is no longer needed (dev server, log tail).
- A task is consuming resources and you want to reclaim them.
- The user explicitly asks to stop a background task.

The task_id is returned in the bash tool's result when async=true. After
termination, the task transitions to a terminal state and bash_status will
report it as killed. Subsequent bash_output calls return any buffered output.`

var killTaskSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"task_id": map[string]interface{}{
			"type":        "string",
			"description": "Async task ID returned by bash(async=true).",
		},
	},
	Required: []string{"task_id"},
}

// KillTaskTool terminates async bash tasks.
type KillTaskTool struct{}

func NewKillTaskTool() *KillTaskTool { return &KillTaskTool{} }

func (k *KillTaskTool) Name() string { return "kill_task" }

func (k *KillTaskTool) Description() string { return killTaskDescription }

func (k *KillTaskTool) Schema() *tool.JSONSchema { return killTaskSchema }

func (k *KillTaskTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	id, err := parseKillTaskID(params)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := DefaultAsyncTaskManager().Kill(id); err != nil {
		payload := map[string]interface{}{
			"task_id": id,
			"status":  "error",
			"error":   err.Error(),
		}
		out, _ := json.Marshal(payload) //nolint:errcheck // best-effort JSON
		return &tool.ToolResult{Success: false, Output: string(out), Data: payload}, err
	}
	payload := map[string]interface{}{
		"task_id": id,
		"status":  "killed",
	}
	out, _ := json.Marshal(payload) //nolint:errcheck // best-effort JSON
	return &tool.ToolResult{Success: true, Output: string(out), Data: payload}, nil
}

func parseKillTaskID(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params["task_id"]
	if !ok {
		return "", errors.New("task_id is required")
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("task_id must be string: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("task_id cannot be empty")
	}
	return value, nil
}
