// bifrost_adapter.go: Bifrost-backed Model — wraps maximhq/bifrost as a
// drop-in replacement for the Anthropic / OpenAI SDK adapters. The Bifrost
// engine handles 23+ providers, streaming, fallback routing, and provider
// quirks (Anthropic cache_control, DashScope enable_thinking) once they're
// signalled through ExtraParams + the Passthrough context key.
package model

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// slogBifrostLogger adapts slog.Default() to Bifrost's schemas.Logger interface
// so that Bifrost's internal log output (warn/error from SSE parsing, etc.)
// routes through saker's file-based logging instead of writing to stderr and
// breaking the TUI.
type slogBifrostLogger struct{}

func (slogBifrostLogger) Debug(msg string, args ...any) {
	slog.Debug(fmt.Sprintf(msg, args...), "component", "bifrost")
}
func (slogBifrostLogger) Info(msg string, args ...any) {
	slog.Info(fmt.Sprintf(msg, args...), "component", "bifrost")
}
func (slogBifrostLogger) Warn(msg string, args ...any) {
	slog.Warn(fmt.Sprintf(msg, args...), "component", "bifrost")
}
func (slogBifrostLogger) Error(msg string, args ...any) {
	slog.Error(fmt.Sprintf(msg, args...), "component", "bifrost")
}
func (slogBifrostLogger) Fatal(msg string, args ...any) {
	slog.Error(fmt.Sprintf(msg, args...), "component", "bifrost", "fatal", true)
}
func (slogBifrostLogger) SetLevel(schemas.LogLevel)            {}
func (slogBifrostLogger) SetOutputType(schemas.LoggerOutputType) {}
func (slogBifrostLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

// BifrostFallbackSpec describes one fallback provider/model pair plus its
// authentication / network details. Each spec is registered into the same
// bifrostAccount as the primary so Bifrost's SDK-level Fallbacks routing can
// dispatch to it transparently.
type BifrostFallbackSpec struct {
	Provider     schemas.ModelProvider
	Model        string
	APIKey       string
	BaseURL      string
	ExtraHeaders map[string]string
}

// ProviderKeySpec is one extra key registered against the primary provider
// for weighted multi-key load balancing. Bifrost picks a key per-request
// proportional to its Weight (default 1.0). Models optionally restricts the
// key to a whitelist of model names.
type ProviderKeySpec struct {
	APIKey string
	Weight float64
	Models []string
}

// SemanticCacheOptions configures the Bifrost semanticcache plugin. The
// plugin requires an external vector store and an embedding provider; saker
// translates the user-facing config (config.SemanticCacheConfig) into this
// struct in pkg/api/runtime_model.go.
type SemanticCacheOptions struct {
	Provider             string
	EmbeddingModel       string
	Dimension            int
	Threshold            float64
	TTL                  time.Duration
	Namespace            string
	CacheByModel         bool
	CacheByProvider      bool
	ConvHistoryThreshold int
	ExcludeSystemPrompt  bool
	VectorStore          VectorStoreSpec
}

// VectorStoreSpec carries connection details for the vector store backing the
// semantic cache. The Type controls which fields are interpreted.
type VectorStoreSpec struct {
	Type     string // "redis" | "qdrant" | "pinecone" | "weaviate"
	Endpoint string
	APIKey   string
	Username string
	Password string
	Database string
	Headers  map[string]string
}

// TelemetryOptions configures the Bifrost otel plugin to emit OTLP traces
// and optional metrics. saker reuses its existing OTel setup when this is
// nil; passing a non-nil instance attaches a Bifrost-side exporter scoped
// to the LLM dispatch path (per-request tokens, fallbacks, etc.).
type TelemetryOptions struct {
	ServiceName                string
	Protocol                   string // "grpc" | "http"
	Endpoint                   string
	Headers                    map[string]string
	Insecure                   bool
	TraceType                  string
	MetricsEnabled             bool
	MetricsEndpoint            string
	MetricsPushIntervalSeconds int
	Sampling                   float64
}

// BifrostConfig wires a primary provider/key (and optional fallback chain)
// into a Bifrost-backed Model. One BifrostConfig produces one *bifrostModel
// that owns its own engine.
type BifrostConfig struct {
	Provider     schemas.ModelProvider // anthropic / openai / etc.
	ModelName    string                // e.g. "claude-sonnet-4-20250514"
	APIKey       string
	BaseURL      string
	MaxTokens    int
	MaxRetries   int
	Temperature  *float64
	System       string
	ExtraHeaders map[string]string      // Anthropic 伪装 header / Authorization overrides
	ExtraParams  map[string]interface{} // DashScope enable_thinking 等 outbound 透传
	// Provider-specific key configurations. Each is optional and applies only
	// when Provider matches. Bifrost requires these for providers whose auth
	// model can't be expressed as a single API key:
	//   - Bedrock: AWS access key/secret/region (or IAM role with empty keys)
	//   - Vertex:  GCP project + region + service-account JSON
	//   - Azure:   endpoint + (api key OR client_id/client_secret/tenant_id)
	//   - Ollama:  server URL (typically no auth, just base URL)
	BedrockKeyConfig *schemas.BedrockKeyConfig
	VertexKeyConfig  *schemas.VertexKeyConfig
	AzureKeyConfig   *schemas.AzureKeyConfig
	OllamaKeyConfig  *schemas.OllamaKeyConfig
	// FallbackProviders carries every fallback's full provider/key spec so the
	// embedded Account can authenticate cross-provider fallback attempts.
	FallbackProviders []BifrostFallbackSpec
	// AdditionalKeys is the multi-key pool for the primary provider. Each
	// entry is registered as a sibling schemas.Key in bifrostAccount;
	// Bifrost balances requests by Weight (default 1.0). Cross-provider
	// keys go through FallbackProviders instead.
	AdditionalKeys []ProviderKeySpec
	// SemanticCache, when non-nil, attaches the Bifrost semanticcache plugin
	// (vector-similarity prompt cache backed by the configured external store).
	SemanticCache *SemanticCacheOptions
	// Telemetry, when non-nil, attaches the Bifrost otel plugin to ship
	// per-request traces (and optional metrics) over OTLP.
	Telemetry *TelemetryOptions
	// OnFailover is invoked once per request when Bifrost's SDK-level fallback
	// chain dispatches to a non-primary provider/model. Implemented via the
	// failover-observer LLM plugin registered in NewBifrost.
	OnFailover  func(from, to string, statusCode int, message string)
	Concurrency int // 0 = use Bifrost default
	BufferSize  int // 0 = use Bifrost default
}

// providerEntry holds the per-provider key set / network config registered
// into bifrostAccount. One entry per ModelProvider; the keys slice is
// pre-built at NewBifrost time and returned verbatim from GetKeysForProvider.
//
// Multi-key load balancing is achieved by populating keys with more than one
// entry — Bifrost selects per-request proportional to schemas.Key.Weight.
// Provider-specific typed key configs (Bedrock IAM, Vertex SA, Azure
// client_secret, Ollama URL) are stitched onto the primary key only; siblings
// authenticate via the bearer Value alone.
type providerEntry struct {
	keys         []schemas.Key
	baseURL      string
	extraHeaders map[string]string
	concurrency  int
	bufferSize   int
}

// bifrostAccount implements schemas.Account with multiple configured providers
// to support cross-provider fallback. Bifrost expects a long-lived account so
// we keep the static map on the struct; GetKeysForProvider returns a fresh
// slice each call.
type bifrostAccount struct {
	providers map[schemas.ModelProvider]*providerEntry
}

func (a *bifrostAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	out := make([]schemas.ModelProvider, 0, len(a.providers))
	for p := range a.providers {
		out = append(out, p)
	}
	return out, nil
}

func (a *bifrostAccount) GetKeysForProvider(_ context.Context, providerKey schemas.ModelProvider) ([]schemas.Key, error) {
	entry, ok := a.providers[providerKey]
	if !ok {
		return nil, nil
	}
	// Defensive copy so Bifrost's per-request weighted selection can't mutate
	// the registered set across concurrent calls. Each schemas.Key is value-
	// typed; nested pointers (BedrockKeyConfig etc.) are intentionally shared
	// because they're treated as immutable post-construction.
	out := make([]schemas.Key, len(entry.keys))
	copy(out, entry.keys)
	return out, nil
}

func (a *bifrostAccount) GetConfigForProvider(providerKey schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	entry, ok := a.providers[providerKey]
	if !ok {
		return nil, fmt.Errorf("bifrost: provider %q not configured", providerKey)
	}
	cfg := &schemas.ProviderConfig{
		NetworkConfig: schemas.NetworkConfig{
			BaseURL:      entry.baseURL,
			ExtraHeaders: entry.extraHeaders,
		},
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{
			Concurrency: entry.concurrency,
			BufferSize:  entry.bufferSize,
		},
	}
	cfg.CheckAndSetDefaults()
	return cfg, nil
}

// BifrostProviderAnthropic / BifrostProviderOpenAI are exported aliases for the
// underlying schemas.ModelProvider constants so callers in other saker packages
// (e.g. pkg/api) can address them without importing bifrost schemas directly.
const (
	BifrostProviderAnthropic = schemas.Anthropic
	BifrostProviderOpenAI    = schemas.OpenAI
)

// BifrostRebuilder is implemented by any Bifrost-backed Model that can produce
// a sibling Model with the same primary config plus a fallback chain wired in.
// Used by saker's runtime failover plumbing to lift cross-provider routing into
// Bifrost's SDK-level Fallbacks instead of stacking saker-side wrappers.
type BifrostRebuilder interface {
	RebuildWithFallbacks(specs []BifrostFallbackSpec, onFailover func(from, to string, statusCode int, message string)) (Model, error)
}

type bifrostModel struct {
	engine       *bifrost.Bifrost
	provider     schemas.ModelProvider
	modelName    string
	maxTokens    int
	temperature  *float64
	system       string
	extraParams  map[string]interface{}
	fallbacks    []schemas.Fallback
	extraHeaders map[string]string
	// srcConfig retains the inputs used to construct this model so
	// RebuildWithFallbacks can spin up a sibling instance with the same primary
	// auth/network details plus a fallback chain.
	srcConfig BifrostConfig
}

// NewBifrost constructs a Bifrost-backed Model. The engine is instantiated
// once per call; callers that need a shared engine across many models should
// reuse a single bifrostModel.
//
// When cfg.FallbackProviders is non-empty, all fallback specs are registered
// in the same bifrostAccount so Bifrost's SDK-level Fallbacks routing can
// dispatch cross-provider. cfg.OnFailover is wired through a failover-observer
// LLM plugin that fires once per request when the resolved provider differs
// from the requested primary.
func NewBifrost(cfg BifrostConfig) (Model, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if cfg.Provider == "" {
		return nil, errors.New("bifrost: provider required")
	}
	// API key is required for most providers, but a few authenticate via
	// typed key configs (Bedrock IAM, Vertex service account, Azure
	// client_secret, Ollama which is often keyless). Treat the presence of
	// any typed config as satisfying the auth requirement.
	hasTypedAuth := cfg.BedrockKeyConfig != nil || cfg.VertexKeyConfig != nil ||
		cfg.AzureKeyConfig != nil || cfg.OllamaKeyConfig != nil
	if apiKey == "" && !hasTypedAuth {
		return nil, errors.New("bifrost: api key or provider-specific key config required")
	}
	if strings.TrimSpace(cfg.ModelName) == "" {
		return nil, errors.New("bifrost: model name required")
	}

	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	account := &bifrostAccount{
		providers: map[schemas.ModelProvider]*providerEntry{
			cfg.Provider: {
				keys:         buildPrimaryKeys(apiKey, cfg),
				baseURL:      strings.TrimSpace(cfg.BaseURL),
				extraHeaders: cfg.ExtraHeaders,
				concurrency:  cfg.Concurrency,
				bufferSize:   cfg.BufferSize,
			},
		},
	}

	// Register every fallback's provider/key/network details into the account
	// so cross-provider Bifrost-level fallback can authenticate.
	fallbacks := make([]schemas.Fallback, 0, len(cfg.FallbackProviders))
	for _, fb := range cfg.FallbackProviders {
		if fb.Provider == "" || strings.TrimSpace(fb.Model) == "" {
			continue
		}
		// Same-provider fallback (different model) — keep the primary's auth;
		// only register provider entry if it hasn't been added yet.
		if _, exists := account.providers[fb.Provider]; !exists {
			fbKey := strings.TrimSpace(fb.APIKey)
			if fbKey == "" {
				// Cross-provider fallback without auth would 401 immediately;
				// skip it rather than registering a broken entry.
				continue
			}
			account.providers[fb.Provider] = &providerEntry{
				keys: []schemas.Key{{
					ID:     "saker-fallback",
					Value:  schemas.EnvVar{Val: fbKey},
					// Bifrost v1.5+: WhiteList{} denies every model;
					// WhiteList{"*"} is the unrestricted/allow-all marker.
					// Saker fallback keys aren't model-pinned, so they
					// must opt in to "*" or every request is rejected.
					Models: schemas.WhiteList{"*"},
					Weight: 1,
				}},
				baseURL:      strings.TrimSpace(fb.BaseURL),
				extraHeaders: fb.ExtraHeaders,
				concurrency:  cfg.Concurrency,
				bufferSize:   cfg.BufferSize,
			}
		}
		fallbacks = append(fallbacks, schemas.Fallback{
			Provider: fb.Provider,
			Model:    strings.TrimSpace(fb.Model),
		})
	}

	bifrostCfg := schemas.BifrostConfig{
		Account:         account,
		InitialPoolSize: 16,
		Logger:          slogBifrostLogger{},
	}
	plugins := make([]schemas.LLMPlugin, 0, 2)
	if cfg.OnFailover != nil && len(fallbacks) > 0 {
		plugins = append(plugins, newFailoverObserverPlugin(cfg.Provider, strings.TrimSpace(cfg.ModelName), cfg.OnFailover))
	}
	if sink := currentObservationSink(); sink != nil {
		plugins = append(plugins, newObservationPlugin(cfg.Provider, strings.TrimSpace(cfg.ModelName), sink))
	}
	if len(plugins) > 0 {
		bifrostCfg.LLMPlugins = plugins
	}

	engine, err := bifrost.Init(context.Background(), bifrostCfg)
	if err != nil {
		return nil, fmt.Errorf("bifrost: init: %w", err)
	}

	return &bifrostModel{
		engine:       engine,
		provider:     cfg.Provider,
		modelName:    strings.TrimSpace(cfg.ModelName),
		maxTokens:    maxTokens,
		temperature:  cfg.Temperature,
		system:       strings.TrimSpace(cfg.System),
		extraParams:  cfg.ExtraParams,
		fallbacks:    fallbacks,
		extraHeaders: cfg.ExtraHeaders,
		srcConfig:    cfg,
	}, nil
}

// RebuildWithFallbacks returns a fresh bifrostModel that shares the primary's
// provider/key/network details but has the supplied fallback chain (and an
// optional OnFailover observer) wired into Bifrost's SDK-level routing. The
// original engine is not mutated — callers swap to the rebuilt instance.
func (m *bifrostModel) RebuildWithFallbacks(specs []BifrostFallbackSpec, onFailover func(from, to string, statusCode int, message string)) (Model, error) {
	cfg := m.srcConfig
	cfg.FallbackProviders = append([]BifrostFallbackSpec(nil), specs...)
	cfg.OnFailover = onFailover
	return NewBifrost(cfg)
}

func (m *bifrostModel) ModelName() string  { return m.modelName }
func (m *bifrostModel) ContextWindow() int { return LookupContextWindow(m.modelName) }

// Complete issues a non-streaming chat completion through the Bifrost engine.
func (m *bifrostModel) Complete(ctx context.Context, req Request) (*Response, error) {
	recordModelRequest(ctx, req)

	bctx, brequest := m.buildRequest(ctx, req)
	bresp, berr := m.engine.ChatCompletionRequest(bctx, brequest)
	if berr != nil {
		return nil, mapBifrostError(berr)
	}
	if bresp == nil || len(bresp.Choices) == 0 || bresp.Choices[0].ChatNonStreamResponseChoice == nil {
		return nil, errors.New("bifrost: empty response")
	}

	choice := bresp.Choices[0]
	resp := &Response{
		Message: convertBifrostMessage(choice.ChatNonStreamResponseChoice.Message),
		Usage:   convertBifrostUsage(bresp.Usage),
	}
	if choice.FinishReason != nil {
		resp.StopReason = *choice.FinishReason
	}
	recordModelResponse(ctx, resp)
	return resp, nil
}

// CompleteStream issues a streaming chat completion. Bifrost emits OpenAI-style
// delta chunks; we accumulate tool-call arguments locally and re-emit them as
// saker StreamResults.
func (m *bifrostModel) CompleteStream(ctx context.Context, req Request, cb StreamHandler) error {
	if cb == nil {
		return errors.New("stream callback required")
	}
	recordModelRequest(ctx, req)

	bctx, brequest := m.buildRequest(ctx, req)
	stream, berr := m.engine.ChatCompletionStreamRequest(bctx, brequest)
	if berr != nil {
		return mapBifrostError(berr)
	}

	var (
		toolAcc      = newBifrostToolCallAccumulator()
		textBuf      strings.Builder
		reasoning    strings.Builder
		usage        Usage
		stopReason   string
		responseID   string
		emittedTools = make(map[uint16]bool)
	)

	for chunk := range stream {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			return mapBifrostError(chunk.BifrostError)
		}
		if chunk.BifrostChatResponse == nil {
			continue
		}
		resp := chunk.BifrostChatResponse
		if resp.ID != "" {
			responseID = resp.ID
		}
		if resp.Usage != nil {
			usage = convertBifrostUsage(resp.Usage)
		}
		if len(resp.Choices) == 0 {
			continue
		}
		choice := resp.Choices[0]
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			stopReason = *choice.FinishReason
		}
		if choice.ChatStreamResponseChoice == nil || choice.ChatStreamResponseChoice.Delta == nil {
			continue
		}
		delta := choice.ChatStreamResponseChoice.Delta

		// Text content delta
		if delta.Content != nil && *delta.Content != "" {
			textBuf.WriteString(*delta.Content)
			if err := cb(StreamResult{Delta: *delta.Content}); err != nil {
				return err
			}
		}

		// Reasoning content (thinking block from DeepSeek/Kimi/Anthropic)
		if delta.Reasoning != nil && *delta.Reasoning != "" {
			reasoning.WriteString(*delta.Reasoning)
		}

		// Tool call deltas — accumulate by index, emit completed calls as we
		// see the next call's first delta or the stream ends.
		for _, tc := range delta.ToolCalls {
			toolAcc.Append(tc)
		}

		// When finish_reason fires, finalize all accumulated tool calls.
		if stopReason != "" {
			for _, completed := range toolAcc.Drain() {
				if emittedTools[completed.idx] {
					continue
				}
				emittedTools[completed.idx] = true
				call := completed.toToolCall()
				if call == nil {
					continue
				}
				if err := cb(StreamResult{ToolCall: call}); err != nil {
					return err
				}
			}
		}
	}
	_ = responseID

	// Drain any remaining tool calls that weren't bracketed by a finish_reason.
	for _, completed := range toolAcc.Drain() {
		if emittedTools[completed.idx] {
			continue
		}
		emittedTools[completed.idx] = true
		call := completed.toToolCall()
		if call == nil {
			continue
		}
		if err := cb(StreamResult{ToolCall: call}); err != nil {
			return err
		}
	}

	finalMsg := Message{
		Role:             string(schemas.ChatMessageRoleAssistant),
		Content:          textBuf.String(),
		ReasoningContent: reasoning.String(),
	}
	for _, completed := range toolAcc.All() {
		if call := completed.toToolCall(); call != nil {
			finalMsg.ToolCalls = append(finalMsg.ToolCalls, *call)
		}
	}
	resp := &Response{
		Message:    finalMsg,
		Usage:      usage,
		StopReason: stopReason,
	}
	recordModelResponse(ctx, resp)
	return cb(StreamResult{Final: true, Response: resp})
}

// buildRequest converts a saker Request to the Bifrost wire format and
// prepares the BifrostContext (deadline propagation + ExtraParams flag).
func (m *bifrostModel) buildRequest(ctx context.Context, req Request) (*schemas.BifrostContext, *schemas.BifrostChatRequest) {
	deadline, _ := ctx.Deadline()
	bctx := schemas.NewBifrostContext(ctx, deadline)
	if len(m.extraParams) > 0 {
		bctx.SetValue(schemas.BifrostContextKeyPassthroughExtraParams, true)
	}

	messages := buildBifrostMessages(req, m.system)
	if req.EnablePromptCache {
		applyBifrostCacheControl(messages)
	}

	params := &schemas.ChatParameters{
		MaxCompletionTokens: ptrInt(req.MaxTokens, m.maxTokens),
		Temperature:         coalesceFloat(req.Temperature, m.temperature),
		Tools:               buildBifrostTools(req.Tools),
		ExtraParams:         m.extraParams,
	}
	if req.ResponseFormat != nil {
		var rf interface{} = map[string]interface{}{"type": req.ResponseFormat.Type}
		if req.ResponseFormat.JSONSchema != nil {
			rf = map[string]interface{}{
				"type": "json_schema",
				"json_schema": map[string]interface{}{
					"name":   req.ResponseFormat.JSONSchema.Name,
					"schema": req.ResponseFormat.JSONSchema.Schema,
					"strict": req.ResponseFormat.JSONSchema.Strict,
				},
			}
		}
		params.ResponseFormat = &rf
	}

	modelName := m.modelName
	if strings.TrimSpace(req.Model) != "" {
		modelName = strings.TrimSpace(req.Model)
	}

	return bctx, &schemas.BifrostChatRequest{
		Provider:  m.provider,
		Model:     modelName,
		Input:     messages,
		Params:    params,
		Fallbacks: m.fallbacks,
	}
}

func ptrInt(req, fallback int) *int {
	if req > 0 {
		return &req
	}
	if fallback > 0 {
		return &fallback
	}
	return nil
}

func coalesceFloat(a, b *float64) *float64 {
	if a != nil {
		return a
	}
	return b
}

// buildPrimaryKeys constructs the schemas.Key slice that bifrostAccount
// returns for the primary provider. The first entry carries the typed
// per-provider key configs (Bedrock/Vertex/Azure/Ollama) so Bifrost can
// authenticate providers whose auth model needs more than a bearer; siblings
// from cfg.AdditionalKeys are simple Value+Weight rows for weighted
// load-balancing.
//
// Empty AdditionalKeys yields a single-element slice — exactly the
// pre-multi-key behaviour, so existing callers see no functional change.
func buildPrimaryKeys(apiKey string, cfg BifrostConfig) []schemas.Key {
	primary := schemas.Key{
		ID:    "saker-default",
		Value: schemas.EnvVar{Val: apiKey},
		// Bifrost v1.5 model-filter semantics: WhiteList{} (empty) means
		// "deny every model"; WhiteList{"*"} is the unrestricted marker.
		// The primary key has to authorize whatever ModelName the caller
		// passed (claude-sonnet-4-*, glm-5.1, qwen-*, …), so we opt in to
		// "*" rather than maintaining a static allow-list per vendor.
		Models: schemas.WhiteList{"*"},
		Weight: 1,
	}
	if cfg.BedrockKeyConfig != nil {
		primary.BedrockKeyConfig = cfg.BedrockKeyConfig
	}
	if cfg.VertexKeyConfig != nil {
		primary.VertexKeyConfig = cfg.VertexKeyConfig
	}
	if cfg.AzureKeyConfig != nil {
		primary.AzureKeyConfig = cfg.AzureKeyConfig
	}
	if cfg.OllamaKeyConfig != nil {
		primary.OllamaKeyConfig = cfg.OllamaKeyConfig
	}

	keys := make([]schemas.Key, 0, 1+len(cfg.AdditionalKeys))
	keys = append(keys, primary)
	for i, extra := range cfg.AdditionalKeys {
		extraKey := strings.TrimSpace(extra.APIKey)
		if extraKey == "" {
			continue
		}
		weight := extra.Weight
		if weight <= 0 {
			weight = 1
		}
		var allowed schemas.WhiteList
		if len(extra.Models) > 0 {
			allowed = schemas.WhiteList(append([]string(nil), extra.Models...))
		} else {
			// Empty Models means "no whitelist specified by the caller";
			// in Bifrost v1.5 this requires the explicit "*" sentinel —
			// a literal empty slice would cause selectKeyFromProvider to
			// reject the key for every model.
			allowed = schemas.WhiteList{"*"}
		}
		keys = append(keys, schemas.Key{
			ID:     fmt.Sprintf("saker-extra-%d", i+1),
			Value:  schemas.EnvVar{Val: extraKey},
			Models: allowed,
			Weight: weight,
		})
	}
	return keys
}
