package api

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/agent"
	"github.com/saker-ai/saker/pkg/config"
	coreevents "github.com/saker-ai/saker/pkg/core/events"
	"github.com/saker-ai/saker/pkg/logging"
	"github.com/saker-ai/saker/pkg/metrics"
	"github.com/saker-ai/saker/pkg/middleware"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/runtime/skills"
)

func (rt *Runtime) runAgent(prep preparedRun) (runResult, error) {
	return rt.runAgentWithMiddleware(prep)
}

func (rt *Runtime) runAgentWithMiddleware(prep preparedRun, extras ...middleware.Middleware) (runResult, error) {
	logger := logging.From(prep.ctx)

	// Select model based on request tier or subagent mapping
	selectedModel, selectedTier := rt.selectModelForSubagent(prep.normalized.TargetSubagent, prep.normalized.Model)

	// Emit ModelSelected event if a non-default model was selected
	if selectedTier != "" {
		hookAdapter := &runtimeHookAdapter{executor: rt.hooks, recorder: prep.recorder}
		// Best-effort event emission; errors are logged but don't block execution
		if err := hookAdapter.ModelSelected(prep.ctx, coreevents.ModelSelectedPayload{
			ToolName:  prep.normalized.TargetSubagent,
			ModelTier: string(selectedTier),
			Reason:    "subagent model mapping",
		}); err != nil {
			logger.Warn("api: failed to emit ModelSelected event", "error", err)
		}
	}

	// Wrap with failover if configured
	selectedModel = rt.wrapWithFailover(selectedModel)

	// Wrap with metrics last so the instrumentation captures the
	// outermost model surface (post-failover). Provider is left empty;
	// the wrapper infers it from the model name at observation time.
	selectedModel = metrics.WrapModel(selectedModel, "")

	// Determine cache enablement: request-level overrides global default
	enableCache := rt.opts.DefaultEnableCache
	if prep.normalized.EnablePromptCache != nil {
		enableCache = *prep.normalized.EnablePromptCache
	}

	hookAdapter := &runtimeHookAdapter{executor: rt.hooks, recorder: prep.recorder}

	// Override model when persona specifies one.
	if prep.personaProfile != nil && prep.personaProfile.Model != "" {
		entry := config.FailoverModelEntry{Model: prep.personaProfile.Model}
		switch {
		case strings.HasPrefix(prep.personaProfile.Model, "gpt-") ||
			strings.HasPrefix(prep.personaProfile.Model, "o1") ||
			strings.HasPrefix(prep.personaProfile.Model, "o3") ||
			strings.HasPrefix(prep.personaProfile.Model, "o4"):
			entry.Provider = "openai"
		default:
			entry.Provider = "anthropic"
		}
		if personaModel, err := rt.createModelFromEntry(entry); err == nil {
			selectedModel = personaModel
		} else {
			slog.Warn("persona model override warning", "error", err)
		}
	}

	// Use persona-specific system prompt when a persona is active.
	sysPrompt := rt.opts.SystemPrompt
	sysBlocks := rt.systemPromptBlocks
	if prep.personaSystemPrompt != "" {
		sysPrompt = prep.personaSystemPrompt
		sysBlocks = prep.personaPromptBlocks
	}

	// Build tool definitions, filtering out persona-disallowed tools.
	toolDefs := availableTools(rt.registry, prep.toolWhitelist)
	if logger.Enabled(prep.ctx, slog.LevelDebug) {
		tdNames := make([]string, len(toolDefs))
		for i, td := range toolDefs {
			tdNames[i] = td.Name
		}
		logger.Debug("tools sent to model", "count", len(tdNames), "whitelist_size", len(prep.toolWhitelist), "names", tdNames)
	}
	if len(prep.personaDisallowed) > 0 {
		filtered := make([]model.ToolDefinition, 0, len(toolDefs))
		for _, td := range toolDefs {
			if _, blocked := prep.personaDisallowed[canonicalToolName(td.Name)]; !blocked {
				filtered = append(filtered, td)
			}
		}
		toolDefs = filtered
	}

	modelAdapter := &conversationModel{
		base:               selectedModel,
		history:            prep.history,
		prompt:             prep.prompt,
		contentBlocks:      prep.contentBlocks,
		trimmer:            rt.newTrimmer(),
		tools:              toolDefs,
		systemPrompt:       sysPrompt,
		systemPromptBlocks: sysBlocks,
		outputSchema:       effectiveOutputSchema(prep.normalized.OutputSchema, rt.opts.OutputSchema),
		outputMode:         effectiveOutputSchemaMode(prep.normalized.OutputSchemaMode, rt.opts.OutputSchemaMode),
		rulesLoader:        rt.rulesLoader,
		enableCache:        enableCache,
		maxOutputTokens:    rt.opts.MaxOutputTokens,
		overrides:          prep.normalized.ModelOverrides,
		hooks:              hookAdapter,
		recorder:           prep.recorder,
		compactor:          rt.compactor,
		sessionID:          prep.normalized.SessionID,
		detectedLanguage:   prep.detectedLanguage,
		tracer:             rt.tracer,
	}

	toolExec := &runtimeToolExecutor{
		executor:           rt.executor,
		hooks:              hookAdapter,
		history:            prep.history,
		allow:              prep.toolWhitelist,
		root:               rt.sbRoot,
		host:               "localhost",
		sessionID:          prep.normalized.SessionID,
		yolo:               rt.opts.DangerouslySkipPermissions,
		permissionResolver: buildPermissionResolver(hookAdapter, rt.opts.PermissionRequestHandler, rt.opts.ApprovalQueue, rt.opts.ApprovalApprover, rt.opts.ApprovalWhitelistTTL, rt.opts.ApprovalWait),
		tracer:             rt.tracer,
	}

	chainItems := make([]middleware.Middleware, 0, 3+len(rt.opts.Middleware)+len(extras))
	if !rt.opts.DangerouslySkipPermissions {
		chainItems = append(chainItems, newSafetyMiddleware())
	}
	chainItems = append(chainItems, newSubdirHintsMiddleware(rt.sbRoot))
	chainItems = append(chainItems, middleware.NewErrorClassifier())
	if rt.memoryStore != nil {
		chainItems = append(chainItems, middleware.NewMemoryNudge(middleware.MemoryNudgeConfig{
			Store:       rt.memoryStore,
			EveryNTurns: 5,
		}))
	}
	if len(rt.opts.Middleware) > 0 {
		chainItems = append(chainItems, rt.opts.Middleware...)
	}
	if len(extras) > 0 {
		chainItems = append(chainItems, extras...)
	}
	chain := middleware.NewChain(chainItems, middleware.WithTimeout(rt.opts.MiddlewareTimeout))

	logger.Info("agent run starting",
		"session_id", prep.normalized.SessionID,
		"model_tier", string(selectedTier),
		"middleware_count", len(chainItems),
		"max_iterations", rt.opts.MaxIterations,
	)
	agentStart := time.Now()

	// Resolve the canonical model name for budget tracking. Empty when the
	// provider doesn't implement ModelNamer; in that case MaxBudgetUSD is
	// inert (the agent guard requires a name to look up pricing).
	budgetModelName := ""
	if namer, ok := selectedModel.(model.ModelNamer); ok {
		budgetModelName = namer.ModelName()
	}
	// Per-run iteration cap: subagents and other internal call sites can
	// override the runtime-wide default by setting maxIterationsOverride on
	// the preparedRun. Zero falls back to rt.opts.MaxIterations.
	maxIters := rt.opts.MaxIterations
	if prep.maxIterationsOverride != 0 {
		maxIters = prep.maxIterationsOverride
	}
	ag, err := agent.New(modelAdapter, toolExec, agent.Options{
		MaxIterations:       maxIters,
		Timeout:             rt.opts.Timeout,
		Middleware:          chain,
		MaxBudgetUSD:        rt.opts.MaxBudgetUSD,
		MaxTokens:           rt.opts.MaxTokens,
		ModelName:           budgetModelName,
		RepeatLoopThreshold: rt.opts.RepeatLoopThreshold,
		StagnationThreshold: rt.opts.StagnationThreshold,
	})
	if err != nil {
		return runResult{}, err
	}

	agentCtx := agent.NewContext()
	if sessionID := strings.TrimSpace(prep.normalized.SessionID); sessionID != "" {
		agentCtx.Values["session_id"] = sessionID
	}
	// Propagate RequestID through agent context for distributed tracing
	if requestID := strings.TrimSpace(prep.normalized.RequestID); requestID != "" {
		agentCtx.Values["request_id"] = requestID
	}
	if len(prep.normalized.ForceSkills) > 0 {
		agentCtx.Values["request.force_skills"] = append([]string(nil), prep.normalized.ForceSkills...)
	}
	if rt.skReg != nil {
		agentCtx.Values["skills.registry"] = rt.skReg
	}
	out, err := ag.Run(prep.ctx, agentCtx)
	if err != nil {
		logger.Error("agent run failed", "session_id", prep.normalized.SessionID, "error", err, "duration_ms", time.Since(agentStart).Milliseconds())
		return runResult{}, err
	}
	logger.Info("agent run completed",
		"session_id", prep.normalized.SessionID,
		"duration_ms", time.Since(agentStart).Milliseconds(),
		"stop_reason", modelAdapter.stopReason,
		"input_tokens", modelAdapter.usage.InputTokens,
		"output_tokens", modelAdapter.usage.OutputTokens,
	)
	result := runResult{output: out, usage: modelAdapter.usage, reason: modelAdapter.stopReason}
	result = rt.applyOutputSchema(prep.ctx, selectedModel, prep.history, modelAdapter.outputSchema, modelAdapter.outputMode, result)
	if rt.tokens != nil && rt.tokens.IsEnabled() {
		modelName := ""
		if namer, ok := selectedModel.(model.ModelNamer); ok {
			modelName = namer.ModelName()
		}
		stats := tokenStatsFromUsage(result.usage, modelName, prep.normalized.SessionID, prep.normalized.RequestID)
		rt.tokens.Record(stats)
		payload := coreevents.TokenUsagePayload{
			InputTokens:   stats.InputTokens,
			OutputTokens:  stats.OutputTokens,
			TotalTokens:   stats.TotalTokens,
			CacheCreation: stats.CacheCreation,
			CacheRead:     stats.CacheRead,
			Model:         stats.Model,
			SessionID:     stats.SessionID,
			RequestID:     stats.RequestID,
		}
		if rt.hooks != nil {
			//nolint:errcheck // token usage events are non-critical notifications
			rt.hooks.Publish(coreevents.Event{
				Type:      coreevents.TokenUsage,
				SessionID: stats.SessionID,
				RequestID: stats.RequestID,
				Payload:   payload,
			})
		}
		if prep.recorder != nil {
			prep.recorder.Record(coreevents.Event{
				Type:      coreevents.TokenUsage,
				SessionID: stats.SessionID,
				RequestID: stats.RequestID,
				Payload:   payload,
			})
		}
	}
	// Async skill learning from completed tasks.
	if rt.skillLearner != nil && result.reason == "end_turn" && result.output != nil {
		out := result.output
		var toolSummaries []skills.ToolCallSummary
		for _, tc := range out.ToolCalls {
			toolSummaries = append(toolSummaries, skills.ToolCallSummary{
				Name:   tc.Name,
				Params: truncateString(fmt.Sprintf("%v", tc.Input), 60),
			})
		}
		learnerInput := skills.LearningInput{
			SessionID: prep.normalized.SessionID,
			Prompt:    prep.normalized.Prompt,
			Output:    out.Content,
			ToolCalls: toolSummaries,
			TurnCount: int(result.usage.InputTokens+result.usage.OutputTokens) / 1000, // rough proxy
			Success:   true,
		}
		go func() {
			if err := rt.skillLearner.Learn(learnerInput); err != nil {
				logger.Warn("skill learner", "error", err)
			}
		}()
	}

	return result, nil
}
