package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/saker-ai/saker/pkg/middleware"
	"github.com/saker-ai/saker/pkg/model"
)

var (
	ErrMaxIterations = errors.New("max iterations reached")
	ErrNilModel      = errors.New("agent: model is nil")
)

// StopReason is a structured enum describing why Run terminated. Mirrors
// Claude Code's reason taxonomy in other/claude-code/src/query.ts. Used by
// dashboards, evaluators, and tests to switch on termination cause without
// parsing free-form strings.
type StopReason string

const (
	// StopReasonCompleted — model returned with Done=true or no further tool
	// calls; the run finished organically.
	StopReasonCompleted StopReason = "completed"
	// StopReasonMaxIterations — hit the MaxIterations cap. Run also returns
	// ErrMaxIterations so legacy callers using errors.Is keep working.
	StopReasonMaxIterations StopReason = "max_iterations"
	// StopReasonMaxBudget — cumulative cost crossed Options.MaxBudgetUSD.
	StopReasonMaxBudget StopReason = "max_budget"
	// StopReasonMaxTokens — cumulative tokens crossed Options.MaxTokens.
	StopReasonMaxTokens StopReason = "max_tokens"
	// StopReasonRepeatLoop — RepeatLoopThreshold identical tool calls in a row.
	StopReasonRepeatLoop StopReason = "repeat_loop"
	// StopReasonStagnation — N iterations without a productive tool call.
	StopReasonStagnation StopReason = "stagnation"
	// StopReasonContextCancel — caller canceled the parent context.
	StopReasonContextCancel StopReason = "aborted_context"
	// StopReasonContextDeadline — context deadline / Options.Timeout fired.
	StopReasonContextDeadline StopReason = "aborted_deadline"
	// StopReasonModelError — model.Generate returned an error.
	StopReasonModelError StopReason = "model_error"
)

// classifyError maps an error to the StopReason that best describes why the
// loop is exiting. Context cancel/deadline errors must NOT be lumped into
// "model_error" — that conflates real provider failures with task-budget
// timeouts and pollutes the stop_reason histogram.
func classifyError(err error) StopReason {
	if err == nil {
		return StopReasonModelError
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return StopReasonContextDeadline
	}
	if errors.Is(err, context.Canceled) {
		return StopReasonContextCancel
	}
	return StopReasonModelError
}

// Model produces the next output for the agent given the current context.
type Model interface {
	Generate(ctx context.Context, c *Context) (*ModelOutput, error)
}

// ToolExecutor performs a tool call emitted by the model.
type ToolExecutor interface {
	Execute(ctx context.Context, call ToolCall, c *Context) (ToolResult, error)
}

// ToolCall describes a discrete tool invocation request.
type ToolCall struct {
	ID    string
	Name  string
	Input map[string]any
}

// ToolResult holds the outcome of a tool invocation.
type ToolResult struct {
	Name     string
	Output   string
	Metadata map[string]any
}

// ModelOutput is the result returned by a Model.Generate call.
type ModelOutput struct {
	Content   string
	ToolCalls []ToolCall
	Done      bool
	// StopReason is set by the agent loop on every Run exit path. The
	// zero value (empty string) means "not yet decided" so individual
	// Model.Generate implementations don't need to populate it.
	StopReason StopReason
}

// Agent drives the core loop, invoking middleware, model, and tools.
type Agent struct {
	model Model
	tools ToolExecutor
	opts  Options
	mw    *middleware.Chain
}

// New constructs an Agent with the provided collaborators.
func New(model Model, tools ToolExecutor, opts Options) (*Agent, error) {
	if model == nil {
		return nil, ErrNilModel
	}
	applied := opts.withDefaults()
	return &Agent{
		model: model,
		tools: tools,
		opts:  applied,
		mw:    applied.Middleware,
	}, nil
}

// Run executes the agent loop. It terminates when the model returns a final
// output (Done or no tool calls), the context is canceled, the cumulative
// token/cost cap is hit, or an error occurs. Every exit path annotates the
// returned ModelOutput.StopReason.
func (a *Agent) Run(ctx context.Context, c *Context) (*ModelOutput, error) {
	if a == nil {
		return nil, errors.New("agent is nil")
	}
	if c == nil {
		c = NewContext()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if a.opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.opts.Timeout)
		defer cancel()
	}

	stateValues := map[string]any{}
	if len(c.Values) > 0 {
		for k, v := range c.Values {
			stateValues[k] = v
		}
	}
	state := &middleware.State{
		Agent:  c,
		Values: stateValues,
	}
	ctx = context.WithValue(ctx, model.MiddlewareStateKey, state)

	if err := a.mw.Execute(ctx, middleware.StageBeforeAgent, state); err != nil {
		return nil, err
	}

	var last *ModelOutput
	iteration := 0

	// Apply default iteration limit when not explicitly configured.
	maxIter := a.opts.MaxIterations
	if maxIter == 0 {
		maxIter = DefaultMaxIterations
	}

	// Track recent tool calls to detect infinite retry loops. warned holds the
	// signatures we have already nudged via OnRepeatWarning so the hook fires
	// at most once per distinct call signature within a single Run.
	var recentCalls []toolCallSig
	warned := map[toolCallSig]bool{}
	threshold := a.opts.RepeatLoopThreshold

	// Stagnation detection: track iterations since last productive tool call
	// (write/edit). Aborts when agent explores endlessly without producing code.
	stagnationThreshold := a.opts.StagnationThreshold
	if stagnationThreshold == 0 {
		stagnationThreshold = DefaultStagnationThreshold
	}
	itersSinceProductive := 0
	stagnationWarned := false

	for {
		if err := ctx.Err(); err != nil {
			annotateContextErr(last, err)
			return last, err
		}
		if maxIter > 0 && iteration >= maxIter {
			annotate(last, StopReasonMaxIterations)
			return last, ErrMaxIterations
		}

		c.Iteration = iteration
		state.Iteration = iteration

		if err := a.mw.Execute(ctx, middleware.StageBeforeModel, state); err != nil {
			annotate(last, classifyError(err))
			return last, err
		}

		// Inject middleware state into context so model can populate ModelInput/ModelOutput
		modelCtx := context.WithValue(ctx, model.MiddlewareStateKey, state)
		out, err := a.model.Generate(modelCtx, c)
		if err != nil {
			annotate(last, classifyError(err))
			return last, err
		}
		if out == nil {
			annotate(last, StopReasonModelError)
			return last, errors.New("model returned nil output")
		}

		last = out
		c.LastModelOutput = out
		state.ModelOutput = out

		if err := a.mw.Execute(ctx, middleware.StageAfterModel, state); err != nil {
			annotate(last, classifyError(err))
			return last, err
		}

		// Refresh cumulative usage/cost from the latest model response. The
		// conversation model adapter publishes resp.Usage onto the
		// middleware state under "model.usage" after every Complete call.
		updateCumulative(c, state, a.opts.ModelName)

		// Token budget: structured stop, no error.
		if a.opts.MaxTokens > 0 && totalTokens(c.CumulativeUsage) >= a.opts.MaxTokens {
			out.StopReason = StopReasonMaxTokens
			_ = a.mw.Execute(ctx, middleware.StageAfterAgent, state)
			return out, nil
		}
		// USD budget: structured stop, no error. Requires ModelName so
		// EstimateCost can resolve a price; otherwise the guard is inert.
		if a.opts.MaxBudgetUSD > 0 && a.opts.ModelName != "" && c.CumulativeCostUSD >= a.opts.MaxBudgetUSD {
			out.StopReason = StopReasonMaxBudget
			_ = a.mw.Execute(ctx, middleware.StageAfterAgent, state)
			return out, nil
		}

		if out.Done || len(out.ToolCalls) == 0 {
			out.StopReason = StopReasonCompleted
			if err := a.mw.Execute(ctx, middleware.StageAfterAgent, state); err != nil {
				return last, err
			}
			return out, nil
		}

		var firstMiddlewareErr error

		for _, call := range out.ToolCalls {
			state.ToolCall = call
			if err := a.mw.Execute(ctx, middleware.StageBeforeTool, state); err != nil && firstMiddlewareErr == nil {
				firstMiddlewareErr = err
			}

			if a.tools == nil {
				annotate(last, StopReasonModelError)
				return last, fmt.Errorf("tool executor is nil for call %s", call.Name)
			}

			res, err := a.tools.Execute(ctx, call, c)
			if err != nil {
				if res.Name == "" {
					res.Name = call.Name
				}
				if res.Metadata == nil {
					res.Metadata = map[string]any{}
				}
				res.Metadata["is_error"] = true
				res.Metadata["error"] = err.Error()
				if res.Output == "" {
					res.Output = fmt.Sprintf("Tool execution failed: %v", err)
				}
			}

			c.ToolResults = append(c.ToolResults, res)
			state.ToolResult = res

			if err := a.mw.Execute(ctx, middleware.StageAfterTool, state); err != nil && firstMiddlewareErr == nil {
				firstMiddlewareErr = err
			}
		}

		if firstMiddlewareErr != nil {
			annotate(last, classifyError(firstMiddlewareErr))
			return last, firstMiddlewareErr
		}

		// Track tool calls for repeat loop detection.
		for _, call := range out.ToolCalls {
			inputJSON, _ := json.Marshal(call.Input)
			recentCalls = append(recentCalls, toolCallSig{name: call.Name, input: string(inputJSON)})
		}
		if threshold > 0 {
			count := tailRepeatCount(recentCalls)
			if count >= threshold {
				annotate(last, StopReasonRepeatLoop)
				return last, fmt.Errorf("agent: detected repeated tool call loop (same tool called %d+ times with identical parameters)", threshold)
			}
			if count == threshold-1 && a.opts.OnRepeatWarning != nil {
				sig := recentCalls[len(recentCalls)-1]
				if !warned[sig] {
					warned[sig] = true
					var inputMap map[string]any
					_ = json.Unmarshal([]byte(sig.input), &inputMap)
					a.opts.OnRepeatWarning(ctx, ToolCall{Name: sig.name, Input: inputMap}, count)
				}
			}
		}

		// Stagnation detection: check if any tool call this iteration was
		// "productive" (write or edit). If not, increment the counter.
		if stagnationThreshold > 0 {
			productive := false
			for _, call := range out.ToolCalls {
				if isProductiveToolCall(call.Name) {
					productive = true
					break
				}
			}
			if productive {
				itersSinceProductive = 0
				stagnationWarned = false
			} else {
				itersSinceProductive++
			}
			if itersSinceProductive >= stagnationThreshold {
				annotate(last, StopReasonStagnation)
				return last, fmt.Errorf("agent: stagnation detected (%d iterations without write/edit)", stagnationThreshold)
			}
			if !stagnationWarned && itersSinceProductive >= stagnationThreshold-2 && a.opts.OnStagnationWarning != nil {
				stagnationWarned = true
				a.opts.OnStagnationWarning(ctx, itersSinceProductive)
			}
		}

		iteration++
	}
}

// annotate sets the StopReason on out when it is non-nil and the field is
// still empty. Used by exit paths that should record the cause but not
// clobber a more specific reason already chosen by an inner branch.
func annotate(out *ModelOutput, reason StopReason) {
	if out == nil || out.StopReason != "" {
		return
	}
	out.StopReason = reason
}

// annotateContextErr distinguishes deadline-exceeded from generic cancel.
func annotateContextErr(out *ModelOutput, err error) {
	if out == nil {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		annotate(out, StopReasonContextDeadline)
		return
	}
	annotate(out, StopReasonContextCancel)
}

// updateCumulative folds the latest model.Usage published on the middleware
// state into the agent context's running totals. Best-effort: when the
// adapter doesn't publish usage (custom Model implementations), the
// cumulative counters simply don't advance.
func updateCumulative(c *Context, state *middleware.State, modelName string) {
	if c == nil || state == nil || state.Values == nil {
		return
	}
	raw, ok := state.Values["model.usage"]
	if !ok {
		return
	}
	usage, ok := raw.(model.Usage)
	if !ok {
		return
	}
	c.CumulativeUsage.InputTokens += usage.InputTokens
	c.CumulativeUsage.OutputTokens += usage.OutputTokens
	c.CumulativeUsage.TotalTokens += usage.TotalTokens
	c.CumulativeUsage.CacheReadTokens += usage.CacheReadTokens
	c.CumulativeUsage.CacheCreationTokens += usage.CacheCreationTokens
	if modelName != "" {
		c.CumulativeCostUSD = model.EstimateCost(modelName, c.CumulativeUsage).TotalCost
	}
}

// totalTokens returns the headline token count for budget checks. Falls
// back to input+output when TotalTokens isn't populated by the provider.
func totalTokens(u model.Usage) int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.InputTokens + u.OutputTokens
}

// toolCallSig captures the signature of a tool call for repeat detection.
type toolCallSig struct{ name, input string }

// tailRepeatCount returns how many of the most recent entries in calls are
// identical to the very last one (always >= 1 when calls is non-empty).
// It is the building block both the warning hook and the abort check use.
// isProductiveToolCall returns true if the tool name indicates a state-changing
// operation (writing/editing files). Read-only tools (read, grep, glob, bash)
// are not considered productive for stagnation purposes.
func isProductiveToolCall(name string) bool {
	switch name {
	case "write", "edit", "write_file", "write_text_file",
		"notebook_edit", "NotebookEdit", "MultiEdit":
		return true
	}
	return false
}

func tailRepeatCount(calls []toolCallSig) int {
	n := len(calls)
	if n == 0 {
		return 0
	}
	last := calls[n-1]
	count := 0
	for i := n - 1; i >= 0; i-- {
		if calls[i] == last {
			count++
		} else {
			break
		}
	}
	return count
}
