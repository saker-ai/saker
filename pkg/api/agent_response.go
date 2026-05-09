package api

import (
	"maps"
	"strings"

	"github.com/cinience/saker/pkg/agent"
	coreevents "github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/model"
)

func (rt *Runtime) buildResponse(prep preparedRun, result runResult) *Response {
	events := []coreevents.Event(nil)
	if prep.recorder != nil {
		events = prep.recorder.Drain()
	}
	settings := rt.Settings()
	resp := &Response{
		Mode:            prep.mode,
		RequestID:       prep.normalized.RequestID,
		Result:          convertRunResult(result),
		CommandResults:  prep.commandResults,
		SkillResults:    prep.skillResults,
		Subagent:        prep.subagentResult,
		HookEvents:      events,
		ProjectConfig:   settings,
		Settings:        settings,
		SandboxSnapshot: rt.sandboxReport(),
		Tags:            maps.Clone(prep.normalized.Tags),
	}
	return resp
}

func (rt *Runtime) sandboxReport() SandboxReport {
	report := snapshotSandbox(rt.sandbox)

	var roots []string
	if root := strings.TrimSpace(rt.sbRoot); root != "" {
		roots = append(roots, root)
	}
	report.Roots = normalizeStrings(roots)

	allowed := make([]string, 0, len(rt.opts.Sandbox.AllowedPaths))
	for _, path := range rt.opts.Sandbox.AllowedPaths {
		if clean := strings.TrimSpace(path); clean != "" {
			allowed = append(allowed, clean)
		}
	}
	for _, path := range additionalSandboxPaths(rt.settings) {
		if clean := strings.TrimSpace(path); clean != "" {
			allowed = append(allowed, clean)
		}
	}
	report.AllowedPaths = normalizeStrings(allowed)

	domains := rt.opts.Sandbox.NetworkAllow
	if len(domains) == 0 {
		domains = defaultNetworkAllowList()
	}
	var cleanedDomains []string
	for _, domain := range domains {
		if host := strings.TrimSpace(domain); host != "" {
			cleanedDomains = append(cleanedDomains, host)
		}
	}
	report.AllowedDomains = normalizeStrings(cleanedDomains)
	return report
}

func convertRunResult(res runResult) *Result {
	if res.output == nil {
		return nil
	}
	toolCalls := make([]model.ToolCall, len(res.output.ToolCalls))
	for i, call := range res.output.ToolCalls {
		toolCalls[i] = model.ToolCall{Name: call.Name, Arguments: call.Input}
	}
	// StopReason precedence:
	//   1. Agent-level structured reason (max_budget, max_tokens, repeat_loop,
	//      aborted_*, max_iterations) — these are decisions the loop owns and
	//      should not be hidden by a "stop" coming back from the model.
	//   2. Otherwise, the conversation model's reason (the provider's own
	//      "end_turn" / "stop" / "tool_use" string) — preserves back-compat
	//      with callers that key off these well-known values.
	stopReason := res.reason
	if agentReason := string(res.output.StopReason); agentReason != "" && agentReason != string(agent.StopReasonCompleted) {
		stopReason = agentReason
	} else if stopReason == "" && agentReason != "" {
		stopReason = agentReason
	}
	return &Result{
		Output:     res.output.Content,
		ToolCalls:  toolCalls,
		Usage:      res.usage,
		StopReason: stopReason,
	}
}