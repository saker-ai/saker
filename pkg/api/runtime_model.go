package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/logging"
	"github.com/cinience/saker/pkg/message"
	"github.com/cinience/saker/pkg/middleware"
	"github.com/cinience/saker/pkg/model"
)

// Heuristic thresholds for the runaway-generation warning emitted by
// (*conversationModel).Generate. They are intentionally loose: we want
// noise-free logs in normal operation and a single line of breadcrumb
// when the model regressed into the eddaff17 failure mode (huge output
// budget burned, single empty tool call, no prose).
const (
	// runawayOutputTokenThreshold is the floor (in output tokens) below
	// which we don't bother evaluating — short responses are never
	// "runaway" by definition.
	runawayOutputTokenThreshold = 2000
	// runawayVisibleTextLimit is the max length of trimmed assistant
	// text content that still counts as "no visible reply".
	runawayVisibleTextLimit = 100
	// runawayMinArgsBytes is the max byte length of the smallest tool
	// call's arguments JSON that still counts as "essentially empty".
	// {} alone is 2 bytes; even a one-key payload is normally >20.
	runawayMinArgsBytes = 8
)

// wrapWithFailover wraps a model with failover if configured in settings.
func (rt *Runtime) wrapWithFailover(primary model.Model) model.Model {
	rt.mu.RLock()
	cfg := rt.settings.Failover
	rt.mu.RUnlock()

	if cfg == nil || (cfg.Enabled != nil && !*cfg.Enabled) || len(cfg.Models) == 0 {
		return primary
	}

	models := []model.Model{primary}
	for _, entry := range cfg.Models {
		m, err := rt.createModelFromEntry(entry)
		if err != nil {
			slog.Warn("api: failover: skip model", "provider", entry.Provider, "model", entry.Model, "error", err)
			continue
		}
		models = append(models, m)
	}
	if len(models) <= 1 {
		return primary
	}

	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}
	fm, err := model.NewFailoverModel(model.FailoverConfig{
		Models:     models,
		MaxRetries: maxRetries,
		OnFailover: func(from, to string, reason model.ClassifiedError) {
			slog.Info("api: failover", "from", from, "to", to, "reason", reason.Reason, "status", reason.StatusCode)
		},
	})
	if err != nil {
		slog.Error("api: failover model creation failed", "error", err)
		return primary
	}
	return fm
}

// createModelFromEntry creates a model.Model from a failover config entry.
func (rt *Runtime) createModelFromEntry(entry config.FailoverModelEntry) (model.Model, error) {
	switch strings.ToLower(entry.Provider) {
	case "anthropic":
		return (&model.AnthropicProvider{
			APIKey:    entry.APIKey,
			BaseURL:   entry.BaseURL,
			ModelName: entry.Model,
		}).Model(context.Background())
	case "openai", "dashscope":
		return (&model.OpenAIProvider{
			APIKey:    entry.APIKey,
			BaseURL:   entry.BaseURL,
			ModelName: entry.Model,
		}).Model(context.Background())
	default:
		return nil, fmt.Errorf("unknown provider: %s", entry.Provider)
	}
}

func (rt *Runtime) newTrimmer() *message.Trimmer {
	if rt.opts.TokenLimit <= 0 {
		return nil
	}
	return message.NewTrimmer(rt.opts.TokenLimit, nil)
}

func effectiveOutputSchema(requestSchema, defaultSchema *model.ResponseFormat) *model.ResponseFormat {
	if requestSchema != nil {
		return cloneResponseFormat(requestSchema)
	}
	return cloneResponseFormat(defaultSchema)
}

func effectiveOutputSchemaMode(requestMode, defaultMode OutputSchemaMode) OutputSchemaMode {
	if requestMode != "" {
		return normalizeOutputSchemaMode(requestMode)
	}
	return normalizeOutputSchemaMode(defaultMode)
}

// ----------------- adapters -----------------

type conversationModel struct {
	base               model.Model
	history            *message.History
	prompt             string
	contentBlocks      []model.ContentBlock
	trimmer            *message.Trimmer
	tools              []model.ToolDefinition
	systemPrompt       string
	systemPromptBlocks []string // cache-optimized blocks; when non-empty, takes precedence
	outputSchema       *model.ResponseFormat
	outputMode         OutputSchemaMode
	rulesLoader        *config.RulesLoader
	enableCache        bool // Enable prompt caching for this conversation
	maxOutputTokens    int  // Soft cap on req.MaxTokens; 0 = let provider decide.
	usage              model.Usage
	stopReason         string
	hooks              *runtimeHookAdapter
	recorder           *hookRecorder
	compactor          *compactor
	sessionID          string
	detectedLanguage   string // auto-detected from user prompt; overrides default in dynamic block
}

func (m *conversationModel) Generate(ctx context.Context, _ *agent.Context) (*agent.ModelOutput, error) {
	if m.base == nil {
		return nil, errors.New("model is nil")
	}

	if strings.TrimSpace(m.prompt) != "" || len(m.contentBlocks) > 0 {
		userMsg := message.Message{Role: "user", Content: strings.TrimSpace(m.prompt)}
		if len(m.contentBlocks) > 0 {
			userMsg.ContentBlocks = convertAPIContentBlocks(m.contentBlocks)
		}
		m.history.Append(userMsg)
		if err := m.hooks.UserPrompt(ctx, m.prompt); err != nil {
			return nil, err
		}
		m.prompt = ""
		m.contentBlocks = nil
	}

	if m.compactor != nil {
		// Microcompact: clear old tool outputs before model call (no API cost).
		m.compactor.microcompact(m.history)

		if _, _, err := m.compactor.maybeCompact(ctx, m.history, m.sessionID, m.recorder); err != nil {
			return nil, err
		}
	}

	snapshot := m.history.All()
	if m.trimmer != nil {
		snapshot = m.trimmer.Trim(snapshot)
	}
	systemPrompt := m.systemPrompt
	rulesAppendix := ""
	if m.rulesLoader != nil {
		if rules := m.rulesLoader.GetContent(); rules != "" {
			rulesAppendix = "\n\n## Project Rules\n\n" + rules
		}
	}

	req := model.Request{
		Messages:          convertMessages(snapshot),
		Tools:             m.tools,
		MaxTokens:         m.maxOutputTokens,
		Model:             "",
		Temperature:       nil,
		EnablePromptCache: m.enableCache,
	}
	if len(m.systemPromptBlocks) > 0 {
		// Use cache-optimized blocks; append rules to the last (dynamic) block.
		blocks := append([]string(nil), m.systemPromptBlocks...)
		// Replace language section in the dynamic block if auto-detected.
		if m.detectedLanguage != "" && m.detectedLanguage != "English" {
			lastIdx := len(blocks) - 1
			blocks[lastIdx] = strings.Replace(blocks[lastIdx],
				sectionLanguage("English"),
				sectionLanguage(m.detectedLanguage), 1)
		}
		if rulesAppendix != "" {
			blocks[len(blocks)-1] += rulesAppendix
		}
		req.SystemBlocks = blocks
	} else {
		if m.detectedLanguage != "" && m.detectedLanguage != "English" {
			systemPrompt = strings.Replace(systemPrompt,
				sectionLanguage("English"),
				sectionLanguage(m.detectedLanguage), 1)
		}
		req.System = systemPrompt + rulesAppendix
	}
	if m.outputMode != OutputSchemaModePostProcess {
		req.ResponseFormat = cloneResponseFormat(m.outputSchema)
	}

	// Populate middleware state with model request if available
	if st, ok := ctx.Value(model.MiddlewareStateKey).(*middleware.State); ok && st != nil {
		st.ModelInput = req
		if st.Values == nil {
			st.Values = map[string]any{}
		}
		st.Values["model.request"] = req
	}

	// Use streaming internally: some API proxies return empty tool_use.input
	// in non-streaming mode but work correctly with streaming. Streaming is
	// also the production-standard path for the Anthropic API.
	genLogger := logging.From(ctx)
	genStart := time.Now()
	genLogger.Debug("model.Generate calling CompleteStream", "messages", len(snapshot))

	var resp *model.Response
	if err := m.base.CompleteStream(ctx, req, func(sr model.StreamResult) error {
		if sr.Final && sr.Response != nil {
			resp = sr.Response
		}
		return nil
	}); err != nil {
		genLogger.Error("model.Generate failed", "error", err, "duration_ms", time.Since(genStart).Milliseconds())
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("model returned no final response")
	}
	m.usage = resp.Usage
	m.stopReason = resp.StopReason
	genLogger.Info("model.Generate completed",
		"duration_ms", time.Since(genStart).Milliseconds(),
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
		"stop_reason", resp.StopReason,
		"tool_calls", len(resp.Message.ToolCalls),
	)
	// Observability for runaway generation: when the model burns through a
	// large output budget but emits no real text *and* the tool call has
	// empty/near-empty arguments, it usually means the model walked into a
	// confused state mid-generation (the eddaff17 incident: 4066 output
	// tokens, one generate_image call with no prompt). Surface this so it
	// shows up in logs without changing behaviour.
	if resp.Usage.OutputTokens >= runawayOutputTokenThreshold && len(resp.Message.ToolCalls) > 0 {
		visibleLen := len(strings.TrimSpace(resp.Message.Content))
		smallestArgs := -1
		for _, tc := range resp.Message.ToolCalls {
			if smallestArgs < 0 || len(tc.Arguments) < smallestArgs {
				smallestArgs = len(tc.Arguments)
			}
		}
		if visibleLen < runawayVisibleTextLimit && smallestArgs >= 0 && smallestArgs <= runawayMinArgsBytes {
			genLogger.Warn("model.Generate possible runaway: high output tokens but tiny tool args and no visible text",
				"output_tokens", resp.Usage.OutputTokens,
				"visible_text_chars", visibleLen,
				"smallest_tool_args_bytes", smallestArgs,
				"tool_calls", len(resp.Message.ToolCalls),
				"stop_reason", resp.StopReason,
			)
		}
	}

	// Populate middleware state with model response and usage
	if st, ok := ctx.Value(model.MiddlewareStateKey).(*middleware.State); ok && st != nil {
		st.ModelOutput = resp
		if st.Values == nil {
			st.Values = map[string]any{}
		}
		st.Values["model.response"] = resp
		st.Values["model.usage"] = resp.Usage
		st.Values["model.stop_reason"] = resp.StopReason
	}

	assistant := message.Message{Role: resp.Message.Role, Content: strings.TrimSpace(resp.Message.Content), ReasoningContent: resp.Message.ReasoningContent}
	if len(resp.Message.ToolCalls) > 0 {
		assistant.ToolCalls = make([]message.ToolCall, len(resp.Message.ToolCalls))
		for i, call := range resp.Message.ToolCalls {
			assistant.ToolCalls[i] = message.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments}
		}
	}
	m.history.Append(assistant)

	out := &agent.ModelOutput{Content: assistant.Content, Done: len(assistant.ToolCalls) == 0}
	if len(assistant.ToolCalls) > 0 {
		out.ToolCalls = make([]agent.ToolCall, len(assistant.ToolCalls))
		for i, call := range assistant.ToolCalls {
			out.ToolCalls[i] = agent.ToolCall{ID: call.ID, Name: call.Name, Input: call.Arguments}
		}
		for _, tc := range out.ToolCalls {
			if len(tc.Input) == 0 {
				slog.Warn("tool call has empty arguments", "name", tc.Name, "id", tc.ID, "hint", "API proxy likely stripped tool_use.input")
			}
		}
	}
	return out, nil
}

func (rt *Runtime) applyOutputSchema(
	ctx context.Context,
	mdl model.Model,
	history *message.History,
	schema *model.ResponseFormat,
	mode OutputSchemaMode,
	result runResult,
) runResult {
	if mdl == nil || schema == nil || normalizeOutputSchemaMode(mode) != OutputSchemaModePostProcess || result.output == nil {
		return result
	}
	content := strings.TrimSpace(result.output.Content)
	if content == "" || len(result.output.ToolCalls) > 0 || json.Valid([]byte(content)) {
		return result
	}
	req := model.Request{
		Messages: []model.Message{{
			Role:    "user",
			Content: "Extract the final structured result from the conversation above. Return only the JSON result.",
		}},
		System:            "You are a formatting assistant. Return only the final JSON result with no explanation.",
		MaxTokens:         outputSchemaMaxTokens,
		EnablePromptCache: false,
		ResponseFormat:    cloneResponseFormat(schema),
	}
	if history != nil {
		if snapshot := history.All(); len(snapshot) > 0 {
			if len(snapshot) > outputSchemaMaxHistory {
				snapshot = snapshot[len(snapshot)-outputSchemaMaxHistory:]
			}
			req.Messages = append(convertMessages(snapshot), req.Messages...)
		}
	}
	var resp *model.Response
	if err := mdl.CompleteStream(ctx, req, func(sr model.StreamResult) error {
		if sr.Final && sr.Response != nil {
			resp = sr.Response
		}
		return nil
	}); err != nil || resp == nil {
		return result
	}
	result.output.Content = strings.TrimSpace(resp.Message.Content)
	result.usage = mergeModelUsage(result.usage, resp.Usage)
	if strings.TrimSpace(resp.StopReason) != "" {
		result.reason = resp.StopReason
	}
	return result
}

func mergeModelUsage(base, extra model.Usage) model.Usage {
	merged := model.Usage{
		InputTokens:         base.InputTokens + extra.InputTokens,
		OutputTokens:        base.OutputTokens + extra.OutputTokens,
		CacheReadTokens:     base.CacheReadTokens + extra.CacheReadTokens,
		CacheCreationTokens: base.CacheCreationTokens + extra.CacheCreationTokens,
	}
	merged.TotalTokens = base.TotalTokens + extra.TotalTokens
	if merged.TotalTokens == 0 {
		merged.TotalTokens = merged.InputTokens + merged.OutputTokens
	}
	return merged
}
