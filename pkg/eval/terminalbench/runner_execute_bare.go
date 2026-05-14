// runner_execute_bare.go: bare agent execution (modelBridge + historyToolExecutor).
//
// This is the original TB2 agent path — a lightweight loop that skips
// middleware, compaction, hooks, and prompt-cache. Kept as the default for
// backward compatibility; pass UseACP=true to exercise the full Runtime.
package terminalbench

import (
	"context"
	"fmt"

	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/eval/dataset"
	"github.com/cinience/saker/pkg/message"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/tool"
)

// runAgentBare runs the agent using modelBridge + historyToolExecutor,
// bypassing the full Saker Runtime. All tool calls execute inside the
// Docker container via the env's builtin tool bindings.
func (r *Runner) runAgentBare(
	ctx context.Context,
	task dataset.Task,
	env sandboxenv.ExecutionEnvironment,
	guestRoot string,
	res *TaskResult,
) {
	registry := tool.NewRegistry()
	if err := registerBuiltinTools(registry, env, guestRoot); err != nil {
		res.Stage = "register-tools"
		res.ErrorMsg = err.Error()
		return
	}
	exec := tool.NewExecutor(registry, nil)

	history := message.NewHistory()
	mdl, err := r.cfg.ModelFactory(ctx)
	if err != nil {
		res.Stage = "model-init"
		res.ErrorMsg = err.Error()
		return
	}

	bridge := newModelBridge(mdl, history, r.cfg.SystemPrompt, task.Instruction, availableTools(registry))
	toolExec := newHistoryToolExecutor(exec, history, guestRoot)

	ag, err := agent.New(bridge, toolExec, agent.Options{
		MaxIterations:       r.cfg.MaxIterations,
		Timeout:             r.taskAgentCap(task),
		RepeatLoopThreshold: r.cfg.RepeatLoopThreshold,
		MaxBudgetUSD:        r.cfg.MaxBudgetUSD,
		MaxTokens:           r.cfg.MaxTokens,
		ModelName:           r.cfg.ModelName,
	})
	if err != nil {
		res.Stage = "agent-init"
		res.ErrorMsg = err.Error()
		return
	}

	agentCtx := agent.NewContext()
	finalOut, runErr := ag.Run(ctx, agentCtx)
	if runErr != nil {
		res.Stage = "agent-run"
		res.ErrorMsg = runErr.Error()
	}
	res.Iterations = agentCtx.Iteration + 1
	usage := bridge.Usage()
	res.InputTokens = usage.InputTokens
	res.OutputTokens = usage.OutputTokens
	res.CacheReadTokens = usage.CacheReadTokens
	res.CacheCreationTokens = usage.CacheCreationTokens
	if calls := bridge.PerCallUsage(); len(calls) > 0 {
		res.IterationTokens = make([]TokenSample, len(calls))
		for i, u := range calls {
			res.IterationTokens[i] = TokenSample{
				Iter:          i + 1,
				Input:         u.InputTokens,
				Output:        u.OutputTokens,
				CacheRead:     u.CacheReadTokens,
				CacheCreation: u.CacheCreationTokens,
			}
		}
	}

	if finalOut != nil && finalOut.StopReason != "" && finalOut.StopReason != agent.StopReasonCompleted {
		res.StopReason = string(finalOut.StopReason)
	} else {
		res.StopReason = bridge.StopReason()
	}

	if runErr != nil {
		if lastErr := bridge.LastError(); lastErr != nil {
			history.Append(message.Message{
				Role:    "system",
				Content: fmt.Sprintf("[runner] model.Generate failed: %s", lastErr.Error()),
			})
		} else {
			history.Append(message.Message{
				Role:    "system",
				Content: fmt.Sprintf("[runner] agent loop aborted: %s (stop_reason=%s)", runErr.Error(), res.StopReason),
			})
		}
	}
	if !r.cfg.DisableTranscripts {
		if path, terr := writeTranscript(r.cfg.OutputDir, task.Name, history.All()); terr != nil && res.ErrorMsg == "" {
			res.Stage = "write-transcript"
			res.ErrorMsg = terr.Error()
		} else if path != "" {
			res.TranscriptPath = path
		}
	}
}
