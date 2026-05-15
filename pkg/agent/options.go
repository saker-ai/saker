package agent

import (
	"context"
	"time"

	"github.com/saker-ai/saker/pkg/middleware"
)

// DefaultMaxIterations is the safety limit applied when MaxIterations is 0.
// This is the bare-core fallback used by callers that don't override it.
// Higher-level surfaces (api.Options, subagents, eval) layer their own
// surface-appropriate defaults on top via withDefaults().
const DefaultMaxIterations = 30

// DefaultSubagentMaxIterations bounds independent agent loops launched as
// subagents. 50 mirrors Claude Code's MAX_AGENT_TURNS in
// other/claude-code/src/utils/hooks/execAgentHook.ts: a self-contained
// task should be able to do meaningful multi-step work without burning
// through the parent's budget.
const DefaultSubagentMaxIterations = 50

// DefaultRepeatLoopThreshold is the number of identical consecutive tool
// calls that aborts Run when RepeatLoopThreshold is 0. Loosened from the
// previous hardcoded "3" so models that legitimately need a few retries
// (e.g. polling, idempotent re-reads) aren't killed prematurely.
const DefaultRepeatLoopThreshold = 5

// DefaultStagnationThreshold is the number of consecutive iterations without
// a "productive" tool call (write/edit) before the agent aborts. This catches
// degenerate loops where the agent explores endlessly (reading files, running
// trivial bash commands) without producing a solution. Zero disables.
const DefaultStagnationThreshold = 15

// RepeatWarningHook is called once per run when the agent observes
// (RepeatLoopThreshold-1) identical consecutive tool calls — i.e. one short
// of the abort. Implementers typically use this to inject a self-correction
// hint into the conversation history so the model has a chance to break out
// of its loop on the next turn.
type RepeatWarningHook func(ctx context.Context, call ToolCall, count int)

// StagnationWarningHook is called when the agent hasn't made a productive
// tool call (write/edit) for (StagnationThreshold-2) iterations. Gives the
// caller a chance to inject guidance before the abort fires.
type StagnationWarningHook func(ctx context.Context, iterationsSinceWrite int)

// Options controls runtime behavior of the Agent.
type Options struct {
	// MaxIterations limits how many cycles Run may execute.
	// Zero applies DefaultMaxIterations; set to -1 for truly unlimited.
	MaxIterations int
	// Timeout bounds the entire Run invocation. Zero disables it.
	Timeout time.Duration
	// Middleware chain. Defaults to an empty chain when nil.
	Middleware *middleware.Chain
	// RepeatLoopThreshold is the count of identical consecutive tool calls
	// that aborts Run with an error. Zero applies DefaultRepeatLoopThreshold;
	// negative values disable detection entirely.
	RepeatLoopThreshold int
	// OnRepeatWarning, when non-nil, is invoked once per distinct tool-call
	// signature when the run hits (RepeatLoopThreshold-1) identical
	// consecutive calls. Useful for injecting a "try a different approach"
	// nudge into the conversation before the abort fires.
	OnRepeatWarning RepeatWarningHook
	// StagnationThreshold aborts Run when N consecutive iterations pass
	// without a "productive" tool call (write or edit). This catches
	// degenerate exploration loops where the agent reads/greps/bashes
	// endlessly without producing output. Zero applies
	// DefaultStagnationThreshold; negative disables.
	StagnationThreshold int
	// OnStagnationWarning is invoked when the stagnation count reaches
	// (StagnationThreshold-2), giving the model a chance to self-correct.
	OnStagnationWarning StagnationWarningHook
	// MaxBudgetUSD aborts Run when the cumulative estimated cost reaches
	// this value (US dollars). Zero disables the check. Requires ModelName
	// to be set so EstimateCost can resolve a price. The check fires after
	// each model call: hitting the cap returns the most recent ModelOutput
	// with StopReason=StopReasonMaxBudget and a nil error.
	MaxBudgetUSD float64
	// MaxTokens aborts Run when the cumulative input+output token count
	// reaches this value. Zero disables the check. Behaves like
	// MaxBudgetUSD: structured stop, not an error.
	MaxTokens int
	// ModelName is the canonical model identifier (e.g.
	// "claude-sonnet-4-5") used by the budget guard to look up pricing
	// via model.EstimateCost. Empty disables MaxBudgetUSD even if it is
	// configured. The MaxTokens guard does not require ModelName.
	ModelName string
}

func (o Options) withDefaults() Options {
	if o.Middleware == nil {
		o.Middleware = middleware.NewChain(nil)
	}
	if o.RepeatLoopThreshold == 0 {
		o.RepeatLoopThreshold = DefaultRepeatLoopThreshold
	}
	if o.StagnationThreshold == 0 {
		o.StagnationThreshold = DefaultStagnationThreshold
	}
	return o
}
