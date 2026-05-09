package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	acpclient "github.com/cinience/saker/pkg/acp/client"
	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/runtime/subagents"
	"github.com/cinience/saker/pkg/tool"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

// subagentMaxIterations chooses the iteration cap for a single subagent run.
// We always honour an explicit unlimited (-1) coming from the runtime — a
// platform deployment that opted out of caps shouldn't have a 50 silently
// re-imposed on its children. Otherwise we apply DefaultSubagentMaxIterations
// (50, mirrors Claude Code's MAX_AGENT_TURNS).
func subagentMaxIterations(runtimeMax int) int {
	if runtimeMax == -1 {
		return -1
	}
	return agent.DefaultSubagentMaxIterations
}

func (rt *Runtime) executeSubagent(ctx context.Context, prompt string, activation skills.ActivationContext, req *Request) (*subagents.Result, string, error) {
	if req == nil {
		return nil, prompt, nil
	}

	def, builtin := applySubagentTarget(req)
	if rt.subMgr == nil {
		return nil, prompt, nil
	}
	meta := map[string]any{
		"entrypoint": req.Mode.EntryPoint,
	}
	if len(req.Metadata) > 0 {
		for k, v := range req.Metadata {
			meta[k] = v
		}
	}
	if session := strings.TrimSpace(req.SessionID); session != "" {
		meta["session_id"] = session
	}
	request := subagents.Request{
		Target:        req.TargetSubagent,
		Instruction:   prompt,
		Activation:    activation,
		ToolWhitelist: normalizeStrings(req.ToolWhitelist),
		Metadata:      meta,
	}
	dispatchCtx := ctx
	if dispatchCtx == nil {
		dispatchCtx = context.Background()
	}
	if subCtx, ok := buildSubagentContext(*req, def, builtin); ok {
		dispatchCtx = subagents.WithContext(dispatchCtx, subCtx)
	}
	res, err := rt.subMgr.Dispatch(dispatchCtx, request)
	if err != nil {
		if errors.Is(err, subagents.ErrDispatchUnauthorized) {
			return nil, prompt, nil
		}
		if errors.Is(err, subagents.ErrNoMatchingSubagent) && req.TargetSubagent == "" {
			return nil, prompt, nil
		}
		return nil, "", err
	}
	text := fmt.Sprint(res.Output)
	if strings.TrimSpace(text) != "" {
		prompt = strings.TrimSpace(text)
	}
	prompt = applyPromptMetadata(prompt, res.Metadata)
	mergeTags(req, res.Metadata)
	applyCommandMetadata(req, res.Metadata)
	return &res, prompt, nil
}

// buildSubagentRunner creates the subagent Runner, optionally wrapping it
// with an ACP runner when external ACP agents are configured.
func (rt *Runtime) buildSubagentRunner() subagents.Runner {
	var runner subagents.Runner = runtimeSubagentRunner{rt: rt}
	if len(rt.opts.ACPAgents) > 0 {
		acpCfg := acpclient.ACPRunnerConfig{Agents: make(map[string]acpclient.ACPAgentConfig, len(rt.opts.ACPAgents))}
		for name, entry := range rt.opts.ACPAgents {
			timeout, _ := time.ParseDuration(entry.Timeout)
			acpCfg.Agents[name] = acpclient.ACPAgentConfig{
				Command: entry.Command,
				Args:    entry.Args,
				Env:     entry.Env,
				Timeout: timeout,
			}
		}
		runner = acpclient.NewACPRunner(acpCfg, runner)
	}
	return runner
}

// buildACPAgentDescriptions generates a description block for detected ACP
// agents so the model knows they are available as subagent_type values.
func buildACPAgentDescriptions(agents map[string]config.ACPAgentEntry) string {
	if len(agents) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\nExternal ACP agents (use these as subagent_type to call external agent processes via ACP protocol):\n")
	for name, entry := range agents {
		sb.WriteString(fmt.Sprintf("- %s: External ACP agent (command: %s). Delegates the task to an external agent process and returns the result.\n", name, entry.Command))
	}
	return sb.String()
}

type runtimeSubagentRunner struct {
	rt *Runtime
}

func (r runtimeSubagentRunner) RunSubagent(ctx context.Context, req subagents.RunRequest) (subagents.Result, error) {
	if r.rt == nil {
		return subagents.Result{}, errors.New("api: runtime is nil")
	}

	// Fork path: when target is empty or "fork", run with inherited context.
	if req.ParentContext.IsFork && subagents.IsForkTarget(req.Target) {
		return r.runFork(ctx, req)
	}

	// Traditional path: run an independent agent loop.
	return r.runTraditional(ctx, req)
}

// runTraditional executes a subagent via handler dispatch or a full agent loop.
// If a handler is registered for the target, the handler result is returned directly.
// Otherwise, a real agent loop runs with the model (bypassing skill/command processing
// to avoid polluting the subagent's instruction).
func (r runtimeSubagentRunner) runTraditional(ctx context.Context, req subagents.RunRequest) (subagents.Result, error) {
	sessionID := req.ParentContext.SessionID
	if sessionID == "" {
		sessionID = req.InstanceID
	}

	normalized := Request{
		Prompt:         req.Instruction,
		SessionID:      sessionID,
		TargetSubagent: req.Target,
		ToolWhitelist:  req.ToolWhitelist,
		Metadata:       cloneArguments(req.Metadata),
		Mode:           r.rt.mode,
	}

	// Extract model tier from metadata (set by runTaskInvocation).
	if m, ok := req.Metadata["task.model"]; ok {
		if tier, ok := m.(string); ok && tier != "" {
			normalized.Model = ModelTier(tier)
		}
	}

	// Try handler dispatch first. If a handler is registered for the target,
	// use its result directly without running a full agent loop.
	if r.rt.subMgr != nil {
		prompt := strings.TrimSpace(req.Instruction)
		activation := normalized.activationContext(prompt)
		subRes, _, err := r.rt.executeSubagent(ctx, prompt, activation, &normalized)
		if err != nil {
			return subagents.Result{Subagent: req.Target, Error: err.Error()}, err
		}
		if subRes != nil {
			// Handler matched and produced a result — return it directly.
			subRes.Subagent = req.Target
			return *subRes, nil
		}
	}

	// No handler matched — run a real agent loop.
	if err := r.rt.beginRun(); err != nil {
		return subagents.Result{Subagent: req.Target, Error: err.Error()}, err
	}
	defer r.rt.endRun()

	if err := r.rt.sessionGate.Acquire(ctx, sessionID); err != nil {
		return subagents.Result{Subagent: req.Target, Error: err.Error()}, err
	}
	defer r.rt.sessionGate.Release(sessionID)

	history := r.rt.histories.Get(sessionID)
	recorder := defaultHookRecorder()
	whitelist := combineToolWhitelists(normalized.ToolWhitelist, nil)
	prep := preparedRun{
		ctx:           ctx,
		prompt:        strings.TrimSpace(req.Instruction),
		history:       history,
		normalized:    normalized,
		recorder:      recorder,
		mode:          normalized.Mode,
		toolWhitelist: whitelist,
		// Subagents get the dedicated 50-iteration cap from
		// agent.DefaultSubagentMaxIterations regardless of the runtime-wide
		// MaxIterations. Mirrors Claude Code's MAX_AGENT_TURNS so a
		// self-contained sub-task has predictable headroom without burning
		// the parent run's budget.
		maxIterationsOverride: subagentMaxIterations(r.rt.opts.MaxIterations),
	}
	defer r.rt.persistHistory(sessionID, history)

	runRes, err := r.rt.runAgentWithMiddleware(prep)
	if err != nil {
		return subagents.Result{Subagent: req.Target, Error: err.Error()}, err
	}

	resp := r.rt.buildResponse(prep, runRes)
	result := subagents.Result{
		Subagent: req.Target,
		Metadata: map[string]any{},
	}
	if resp != nil && resp.Result != nil {
		result.Output = resp.Result.Output
		result.Metadata["usage"] = resp.Result.Usage
		result.Metadata["stop_reason"] = resp.Result.StopReason
	}
	return result, nil
}

// runFork executes a fork subagent that inherits the parent's conversation
// history and system prompt for prompt cache sharing.
func (r runtimeSubagentRunner) runFork(ctx context.Context, req subagents.RunRequest) (subagents.Result, error) {
	if r.rt.histories == nil {
		return subagents.Result{Subagent: subagents.ForkSubagentType, Error: "histories not initialized"}, errors.New("api: histories not initialized")
	}
	parentSessionID := req.ParentContext.SessionID
	if parentSessionID == "" {
		parentSessionID = "default"
	}
	childSessionID := parentSessionID + ":fork-" + req.InstanceID

	// Get parent history and check for recursive forking.
	parentHistory := r.rt.histories.Get(parentSessionID)
	parentMsgs := parentHistory.All()
	if subagents.IsInForkChild(parentMsgs) {
		return subagents.Result{
			Subagent: subagents.ForkSubagentType,
			Error:    "cannot fork from within a fork child",
		}, errors.New("api: cannot fork from within a fork child")
	}

	// Create child history and copy parent messages for cache sharing.
	childHistory := r.rt.histories.Get(childSessionID)
	for _, msg := range parentMsgs {
		childHistory.Append(msg)
	}

	// Build fork directive and append as user message.
	directive := subagents.BuildChildDirective(req.Instruction)

	// Use parent's system prompt if provided, else fall back to runtime default.
	systemPrompt := r.rt.opts.SystemPrompt
	if req.ParentContext.ParentSystemPrompt != "" {
		systemPrompt = req.ParentContext.ParentSystemPrompt
	}

	// Build a prepared run with the inherited state.
	recorder := defaultHookRecorder()
	normalized := Request{
		SessionID: childSessionID,
		Mode:      r.rt.mode,
		Metadata:  cloneArguments(req.Metadata),
	}
	// Extract model tier
	if m, ok := req.Metadata["task.model"]; ok {
		if tier, ok := m.(string); ok && tier != "" {
			normalized.Model = ModelTier(tier)
		}
	}

	// Override system prompt for fork child to use parent's (cache-identical).
	origSystemPrompt := r.rt.opts.SystemPrompt
	r.rt.opts.SystemPrompt = systemPrompt
	defer func() { r.rt.opts.SystemPrompt = origSystemPrompt }()

	prep := preparedRun{
		ctx:        ctx,
		prompt:     directive,
		history:    childHistory,
		normalized: normalized,
		recorder:   recorder,
		mode:       r.rt.mode,
		// Same 50-iter contract as the traditional subagent path — a fork
		// child should not be able to outlive its parent's budget by accident.
		maxIterationsOverride: subagentMaxIterations(r.rt.opts.MaxIterations),
	}

	result, err := r.rt.runAgentWithMiddleware(prep)
	if err != nil {
		return subagents.Result{
			Subagent: subagents.ForkSubagentType,
			Error:    err.Error(),
		}, err
	}

	res := subagents.Result{
		Subagent: subagents.ForkSubagentType,
		Metadata: map[string]any{},
	}
	if result.output != nil {
		res.Output = result.output.Content
		res.Metadata["usage"] = result.usage
		res.Metadata["stop_reason"] = result.reason
	}
	return res, nil
}

func (rt *Runtime) ensureSubagentExecutor() *subagents.Executor {
	if rt == nil || rt.subMgr == nil {
		return nil
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.subStore == nil {
		rt.subStore = subagents.NewMemoryStore()
	}
	if rt.subExec == nil {
		rt.subExec = subagents.NewExecutor(rt.subMgr, rt.subStore, rt.buildSubagentRunner())
	}
	return rt.subExec
}

func (rt *Runtime) spawnSubagent(ctx context.Context, prompt string, activation skills.ActivationContext, req *Request) (subagents.SpawnHandle, error) {
	if rt == nil {
		return subagents.SpawnHandle{}, errors.New("api: runtime is nil")
	}
	exec := rt.ensureSubagentExecutor()
	if exec == nil {
		return subagents.SpawnHandle{}, errors.New("api: subagent manager is not configured")
	}
	if req == nil {
		return subagents.SpawnHandle{}, errors.New("api: request is nil")
	}
	def, builtin := applySubagentTarget(req)
	meta := map[string]any{
		"entrypoint": req.Mode.EntryPoint,
	}
	for k, v := range req.Metadata {
		meta[k] = v
	}
	if session := strings.TrimSpace(req.SessionID); session != "" {
		meta["session_id"] = session
	}
	var parentCtx subagents.Context
	if subCtx, ok := buildSubagentContext(*req, def, builtin); ok {
		parentCtx = subCtx
	}
	// Fork path: mark context as fork and pass parent's system prompt
	// so the child can inherit the parent's conversation for cache sharing.
	if subagents.IsForkTarget(req.TargetSubagent) {
		parentCtx.IsFork = true
		parentCtx.ParentSystemPrompt = rt.opts.SystemPrompt
		if parentCtx.SessionID == "" {
			parentCtx.SessionID = strings.TrimSpace(req.SessionID)
		}
	}
	// Check background flag from metadata.
	background := false
	if bg, ok := meta["task.background"]; ok {
		if b, ok := bg.(bool); ok {
			background = b
		}
	}
	return exec.Spawn(subagents.WithTaskDispatch(ctx), subagents.SpawnRequest{
		Target:        req.TargetSubagent,
		Instruction:   prompt,
		Activation:    activation,
		ToolWhitelist: normalizeStrings(req.ToolWhitelist),
		Metadata:      meta,
		ParentContext: parentCtx,
		Background:    background,
	})
}

func (rt *Runtime) waitSubagent(ctx context.Context, id string, timeout time.Duration) (subagents.WaitResult, error) {
	exec := rt.ensureSubagentExecutor()
	if exec == nil {
		return subagents.WaitResult{}, errors.New("api: subagent manager is not configured")
	}
	return exec.Wait(ctx, subagents.WaitRequest{ID: id, Timeout: timeout})
}

func (rt *Runtime) taskRunner() toolbuiltin.TaskRunner {
	return func(ctx context.Context, req toolbuiltin.TaskRequest) (*tool.ToolResult, error) {
		return rt.runTaskInvocation(ctx, req)
	}
}

func (rt *Runtime) runTaskInvocation(ctx context.Context, req toolbuiltin.TaskRequest) (*tool.ToolResult, error) {
	if rt == nil {
		return nil, errors.New("api: runtime is nil")
	}
	if rt.subMgr == nil {
		return nil, errors.New("api: subagent manager is not configured")
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, errors.New("api: task prompt is empty")
	}
	sessionID := strings.TrimSpace(req.Resume)
	if sessionID == "" {
		sessionID = defaultSessionID(rt.mode.EntryPoint)
	}
	reqPayload := &Request{
		Prompt:         prompt,
		Mode:           rt.mode,
		SessionID:      sessionID,
		TargetSubagent: req.SubagentType,
	}
	if desc := strings.TrimSpace(req.Description); desc != "" {
		reqPayload.Metadata = map[string]any{"task.description": desc}
	}
	if req.Model != "" {
		if reqPayload.Metadata == nil {
			reqPayload.Metadata = map[string]any{}
		}
		reqPayload.Metadata["task.model"] = req.Model
	}
	activation := skills.ActivationContext{Prompt: prompt}
	if len(reqPayload.Metadata) > 0 {
		activation.Metadata = maps.Clone(reqPayload.Metadata)
	}
	// Pass background flag through to SpawnRequest via metadata.
	if req.Background {
		if reqPayload.Metadata == nil {
			reqPayload.Metadata = map[string]any{}
		}
		reqPayload.Metadata["task.background"] = true
	}

	handle, err := rt.spawnSubagent(ctx, prompt, activation, reqPayload)
	if err != nil {
		return nil, err
	}

	// Background mode: return immediately with the task handle ID.
	if req.Background {
		return &tool.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("Agent launched in background with task ID: %s", handle.ID),
			Data: map[string]any{
				"subagent_id": handle.ID,
				"background":  true,
			},
		}, nil
	}

	waited, err := rt.waitSubagent(ctx, handle.ID, 10*time.Minute)
	if err != nil {
		return nil, err
	}
	if waited.TimedOut || waited.Instance.Result == nil {
		return nil, errors.New("api: task execution returned no result")
	}
	res := *waited.Instance.Result
	if len(res.Metadata) > 0 {
		res.Metadata = maps.Clone(res.Metadata)
	}
	if len(res.Metadata) == 0 {
		res.Metadata = map[string]any{}
	}
	res.Metadata["subagent_id"] = handle.ID
	return convertTaskToolResult(res), nil
}

func convertTaskToolResult(res subagents.Result) *tool.ToolResult {
	output := strings.TrimSpace(fmt.Sprint(res.Output))
	if output == "" {
		if res.Subagent != "" {
			output = fmt.Sprintf("subagent %s completed", res.Subagent)
		} else {
			output = "subagent completed"
		}
	}
	data := map[string]any{
		"subagent": res.Subagent,
	}
	if len(res.Metadata) > 0 {
		data["metadata"] = res.Metadata
		if id, ok := res.Metadata["subagent_id"]; ok {
			data["subagent_id"] = id
		}
	}
	if res.Error != "" {
		data["error"] = res.Error
	}
	return &tool.ToolResult{
		Success: res.Error == "",
		Output:  output,
		Data:    data,
	}
}

// selectModelForSubagent returns the appropriate model for the given subagent type.
// Priority: 1) Request.Model override, 2) SubagentModelMapping, 3) default Model.
// Returns the selected model and the tier used (empty string if default).
func (rt *Runtime) selectModelForSubagent(subagentType string, requestTier ModelTier) (model.Model, ModelTier) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	// Priority 1: Request-level override (方案 C)
	if requestTier != "" {
		if m, ok := rt.opts.ModelPool[requestTier]; ok && m != nil {
			return m, requestTier
		}
	}

	// Priority 2: Subagent type mapping (方案 A)
	if rt.opts.SubagentModelMapping != nil {
		canonical := strings.ToLower(strings.TrimSpace(subagentType))
		if tier, ok := rt.opts.SubagentModelMapping[canonical]; ok {
			if rt.opts.ModelPool != nil {
				if m, ok := rt.opts.ModelPool[tier]; ok && m != nil {
					return m, tier
				}
			}
		}
	}

	// Priority 3: Default model
	return rt.opts.Model, ""
}
