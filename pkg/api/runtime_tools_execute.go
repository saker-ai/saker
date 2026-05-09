package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"time"

	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/artifact"
	coreevents "github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/logging"
	"github.com/cinience/saker/pkg/message"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/sandbox"
	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
)

func (t *runtimeToolExecutor) measureUsage() sandbox.ResourceUsage {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return sandbox.ResourceUsage{MemoryBytes: stats.Alloc}
}

func (t *runtimeToolExecutor) Execute(ctx context.Context, call agent.ToolCall, _ *agent.Context) (agent.ToolResult, error) {
	appendToolResult := func(content string, blocks []model.ContentBlock, artifacts []artifact.ArtifactRef) {
		if t.history != nil {
			msg := message.Message{
				Role: "tool",
				ToolCalls: []message.ToolCall{{
					ID:     call.ID,
					Name:   call.Name,
					Result: content,
				}},
			}
			if len(blocks) > 0 {
				msg.ContentBlocks = convertAPIContentBlocks(blocks)
			}
			if len(artifacts) > 0 {
				msg.Artifacts = append([]artifact.ArtifactRef(nil), artifacts...)
			}
			t.history.Append(msg)
		}
	}
	appendEarlyError := func(err error) error {
		appendToolResult(fmt.Sprintf("Tool execution failed: %v", err), nil, nil)
		return err
	}

	if t.executor == nil {
		return agent.ToolResult{}, appendEarlyError(errors.New("tool executor not initialised"))
	}
	if !t.isAllowed(ctx, call.Name) {
		return agent.ToolResult{}, appendEarlyError(fmt.Errorf("tool %s is not whitelisted", call.Name))
	}

	// Defensive check: if tool call has empty/nil arguments but the tool requires
	// parameters, return a diagnostic error instead of executing with missing params.
	// This commonly happens when an API proxy strips tool_use.input (returns "input": {}).
	if len(call.Input) == 0 {
		if reg := t.executor.Registry(); reg != nil {
			if impl, err := reg.Get(call.Name); err == nil {
				if schema := impl.Schema(); schema != nil && len(schema.Required) > 0 {
					errMsg := fmt.Sprintf(
						"tool %q called with empty arguments but requires %v; "+
							"the API proxy likely stripped tool_use.input — check proxy configuration",
						call.Name, schema.Required)
					slog.Warn("tool call has empty arguments but requires parameters", "tool", call.Name, "id", call.ID, "message", errMsg)
					if t.history != nil {
						t.history.Append(message.Message{
							Role: "tool",
							ToolCalls: []message.ToolCall{{
								ID:     call.ID,
								Name:   call.Name,
								Result: errMsg,
							}},
						})
					}
					return agent.ToolResult{
						Name:     call.Name,
						Output:   errMsg,
						Metadata: map[string]any{"error": "empty_arguments"},
					}, nil
				}
			}
		}
	}

	params, preErr := t.hooks.PreToolUse(ctx, coreToolUsePayload(call))
	if preErr != nil {
		// In yolo mode, skip permission checks — auto-allow everything.
		if t.yolo && errors.Is(preErr, ErrToolUseRequiresApproval) {
			preErr = nil
		} else if errors.Is(preErr, ErrToolUseRequiresApproval) && t.permissionResolver != nil {
			checkParams := call.Input
			if params != nil {
				checkParams = params
			}
			decision, err := t.permissionResolver(ctx, tool.Call{
				Name:      call.Name,
				Params:    checkParams,
				SessionID: t.sessionID,
			}, security.PermissionDecision{
				Action: security.PermissionAsk,
				Tool:   call.Name,
				Rule:   "hook:pre_tool_use",
			})
			if err != nil {
				preErr = err
			} else {
				switch decision.Action {
				case security.PermissionAllow:
					preErr = nil
				case security.PermissionDeny:
					preErr = fmt.Errorf("%w: %s", ErrToolUseDenied, call.Name)
				default:
					preErr = fmt.Errorf("%w: %s", ErrToolUseRequiresApproval, call.Name)
				}
			}
		}
	}
	if preErr != nil {
		// Hook denied execution - still need to add tool_result to history
		errContent := fmt.Sprintf(`{"error":%q}`, preErr.Error())
		appendToolResult(errContent, nil, nil)
		return agent.ToolResult{Name: call.Name, Output: errContent, Metadata: map[string]any{"error": preErr.Error()}}, preErr
	}
	if params != nil {
		call.Input = params
	}

	toolLogger := logging.From(ctx)
	toolStart := time.Now()
	toolLogger.Info("tool.Execute started", "tool", call.Name, "call_id", call.ID)

	callSpec := tool.Call{
		Name:      call.Name,
		Params:    call.Input,
		Path:      t.root,
		Host:      t.host,
		Usage:     t.measureUsage(),
		SessionID: t.sessionID,
	}
	if emit := streamEmitFromContext(ctx); emit != nil {
		callSpec.StreamSink = func(chunk string, isStderr bool) {
			evt := StreamEvent{
				Type:      EventToolExecutionOutput,
				ToolUseID: call.ID,
				Name:      call.Name,
				Output:    chunk,
			}
			evt.IsStderr = &isStderr
			emit(ctx, evt)
		}
	}
	if t.host != "" {
		callSpec.Host = t.host
	}
	exec := t.executor
	if t.permissionResolver != nil {
		exec = exec.WithPermissionResolver(t.permissionResolver)
	}
	result, err := exec.Execute(ctx, callSpec)
	toolDuration := time.Since(toolStart).Milliseconds()
	if err != nil {
		toolLogger.Warn("tool.Execute failed", "tool", call.Name, "call_id", call.ID, "error", err, "duration_ms", toolDuration)
	} else {
		toolLogger.Info("tool.Execute completed", "tool", call.Name, "call_id", call.ID, "duration_ms", toolDuration)
	}
	toolResult := agent.ToolResult{Name: call.Name}
	meta := map[string]any{}
	content := ""
	var blocks []model.ContentBlock
	var artifacts []artifact.ArtifactRef
	if result != nil && result.Result != nil {
		toolResult.Output = result.Result.Output
		meta["data"] = result.Result.Data
		if result.Result.OutputRef != nil {
			meta["output_ref"] = result.Result.OutputRef
		}
		if result.Result.Summary != "" {
			meta["summary"] = result.Result.Summary
		}
		if result.Result.Structured != nil {
			meta["structured"] = result.Result.Structured
		}
		if result.Result.Preview != nil {
			meta["preview"] = result.Result.Preview
		}
		content = result.Result.Output
		if len(result.Result.ContentBlocks) > 0 {
			blocks = append([]model.ContentBlock(nil), result.Result.ContentBlocks...)
			meta["content_blocks"] = blocks
		}
		if len(result.Result.Artifacts) > 0 {
			artifacts = append([]artifact.ArtifactRef(nil), result.Result.Artifacts...)
			meta["artifacts"] = artifacts
		}
	}
	if err != nil {
		meta["error"] = err.Error()
		content = fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	if len(meta) > 0 {
		toolResult.Metadata = meta
	}

	if hookErr := t.hooks.PostToolUse(ctx, coreToolResultPayload(call, result, err)); hookErr != nil && err == nil {
		// Hook failed - still need to add tool_result to history
		appendToolResult(content, blocks, artifacts)
		return toolResult, hookErr
	}

	appendToolResult(content, blocks, artifacts)
	return toolResult, err
}

func coreToolUsePayload(call agent.ToolCall) coreevents.ToolUsePayload {
	return coreevents.ToolUsePayload{Name: call.Name, Params: call.Input}
}

func coreToolResultPayload(call agent.ToolCall, res *tool.CallResult, err error) coreevents.ToolResultPayload {
	payload := coreevents.ToolResultPayload{Name: call.Name}
	if res != nil && res.Result != nil {
		payload.Result = res.Result.Output
		payload.Duration = res.Duration()
	}
	payload.Err = err
	return payload
}