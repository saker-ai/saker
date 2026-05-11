// runner_execute.go: per-task execution loop (runOne) and timeout helpers.
package terminalbench

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/eval/dataset"
	"github.com/cinience/saker/pkg/message"
	"github.com/cinience/saker/pkg/middleware"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/tool"
)

// runOne executes the per-task pipeline. It NEVER returns an error: every
// failure is folded into TaskResult so the worker pool can keep going and the
// JSONL stream stays well-formed.
func (r *Runner) runOne(ctx context.Context, task dataset.Task) (res TaskResult) {
	res = TaskResult{
		Name:      task.Name,
		Category:  task.Category,
		StartedAt: time.Now(),
	}
	// Named return is load-bearing: the deferred Duration mutation is only
	// observable to callers because `res` is the actual return slot. With an
	// unnamed return, `return res` would copy the struct before the defer
	// fired, leaving Duration=0 in the report — that's how this stayed broken.
	defer func() { res.Duration = time.Since(res.StartedAt) }()

	if strings.TrimSpace(task.SkipReason) != "" && r.cfg.SkipIncompatible {
		res.Skipped = true
		res.SkipReason = task.SkipReason
		res.Stage = "skip"
		return res
	}

	taskCtx, cancel := context.WithTimeout(ctx, r.taskCap(task))
	defer cancel()

	// All builtin tools (bash/file/grep/glob) call PrepareSession with the
	// session id derived from ctx. Without this, they fall back to
	// "default" and dockerenv spawns a SECOND container — agent edits land
	// there while the verifier runs in the runner's task container, so
	// changes never reach test.sh. Pin the session id to task.Name so the
	// dockerenv cache returns the SAME container across runner + tools.
	taskCtx = context.WithValue(taskCtx, middleware.SessionIDContextKey, task.Name)

	env, err := r.cfg.EnvFactory(task)
	if err != nil {
		res.Stage = "env-init"
		res.ErrorMsg = err.Error()
		return res
	}

	ps, err := env.PrepareSession(taskCtx, sandboxenv.SessionContext{SessionID: task.Name})
	if err != nil {
		res.Stage = "prepare-session"
		res.ErrorMsg = err.Error()
		return res
	}
	// dockerenv populates Meta["image_digest"] when the local image has a
	// RepoDigest (i.e. it was pulled from a registry). Locally-built images
	// have no digest — we leave the field empty, which still serializes
	// cleanly thanks to omitempty.
	if ps != nil && ps.Meta != nil {
		if d, ok := ps.Meta["image_digest"].(string); ok {
			res.ImageDigest = d
		}
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 1*time.Minute)
		_ = env.CloseSession(closeCtx, ps)
		closeCancel()
	}()

	uploader, _ := env.(archiveUploader)
	if uploader == nil {
		res.Stage = "env-upload-capability"
		res.ErrorMsg = "execution environment does not support tar uploads (CopyArchiveTo)"
		return res
	}

	// guestRoot is the in-container cwd we treat as "the agent's workspace".
	// dockerenv now publishes the image-declared WORKDIR via PreparedSession,
	// so we trust that over the legacy /app fallback. environment.tar must
	// land here too — TB2 tasks expect the tarball contents at the same
	// place the test.sh later treats as cwd.
	guestRoot := defaultGuestWorkdir
	if ps != nil && strings.TrimSpace(ps.GuestCwd) != "" {
		guestRoot = ps.GuestCwd
	}

	if task.EnvironmentTar != "" {
		envFile, openErr := task.OpenEnvironment()
		if openErr != nil {
			res.Stage = "open-environment-tar"
			res.ErrorMsg = openErr.Error()
			return res
		}
		if envFile != nil {
			uploadErr := uploader.CopyArchiveTo(taskCtx, ps, guestRoot, envFile)
			envFile.Close()
			if uploadErr != nil {
				res.Stage = "upload-environment"
				res.ErrorMsg = uploadErr.Error()
				return res
			}
		}
	}

	registry := tool.NewRegistry()
	if err := registerBuiltinTools(registry, env, guestRoot); err != nil {
		res.Stage = "register-tools"
		res.ErrorMsg = err.Error()
		return res
	}
	exec := tool.NewExecutor(registry, nil)

	history := message.NewHistory()
	mdl, err := r.cfg.ModelFactory(taskCtx)
	if err != nil {
		res.Stage = "model-init"
		res.ErrorMsg = err.Error()
		return res
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
		return res
	}

	agentCtx := agent.NewContext()
	finalOut, runErr := ag.Run(taskCtx, agentCtx)
	if runErr != nil {
		res.Stage = "agent-run"
		res.ErrorMsg = runErr.Error()
		// Fall through — partial completion can still pass tests.
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
	// Prefer the agent-level structured StopReason when set (max_budget,
	// max_tokens, max_iterations, repeat_loop, aborted_*). It carries the
	// loop's own decision, which is more actionable than the model's
	// "end_turn"/"stop" string. Fall back to the model-level reason.
	if finalOut != nil && finalOut.StopReason != "" && finalOut.StopReason != agent.StopReasonCompleted {
		res.StopReason = string(finalOut.StopReason)
	} else {
		res.StopReason = bridge.StopReason()
	}
	// Capture the verbatim provider failure into the transcript so post-mortem
	// analysis doesn't have to cross-reference results.jsonl. Stored as a
	// synthetic "system" entry to keep the file replayable.
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

	if verifyErr := runVerifier(taskCtx, env, uploader, ps, task, &res, r.cfg.TerminalTimeout, r.cfg.OutputDir, r.cfg.VerifierEnv); verifyErr != nil && res.ErrorMsg == "" {
		res.Stage = "verify"
		res.ErrorMsg = verifyErr.Error()
	}
	return res
}

// taskCap is the wall-clock bound for one task: model loop + verifier.
func (r *Runner) taskCap(task dataset.Task) time.Duration {
	cap := r.cfg.TaskTimeout
	if task.AgentTimeout > 0 && task.AgentTimeout < cap {
		cap = task.AgentTimeout
	}
	terminal := r.cfg.TerminalTimeout
	if task.TerminalTimeout > 0 && task.TerminalTimeout < terminal {
		terminal = task.TerminalTimeout
	}
	return cap + 2*terminal
}

func (r *Runner) taskAgentCap(task dataset.Task) time.Duration {
	cap := r.cfg.TaskTimeout
	if task.AgentTimeout > 0 && task.AgentTimeout < cap {
		cap = task.AgentTimeout
	}
	return cap
}
