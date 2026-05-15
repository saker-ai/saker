package eval

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/tool"
)

// NoopModel is a model that returns an empty assistant response.
// Use it for offline evals that only need to exercise the runtime pipeline.
type NoopModel struct{}

func (NoopModel) Complete(context.Context, model.Request) (*model.Response, error) {
	return &model.Response{
		Message:    model.Message{Role: "assistant", Content: "ok"},
		StopReason: "end_turn",
	}, nil
}

func (NoopModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	resp, err := NoopModel{}.Complete(ctx, req)
	if err != nil {
		return err
	}
	if cb != nil {
		return cb(model.StreamResult{Final: true, Response: resp})
	}
	return nil
}

// EchoModel returns the last user message content as the assistant response.
// Useful for testing message flow through the agent loop.
type EchoModel struct{}

func (EchoModel) Complete(_ context.Context, req model.Request) (*model.Response, error) {
	content := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			content = req.Messages[i].Content
			break
		}
	}
	return &model.Response{
		Message:    model.Message{Role: "assistant", Content: content},
		StopReason: "end_turn",
	}, nil
}

func (EchoModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	resp, err := EchoModel{}.Complete(ctx, req)
	if err != nil {
		return err
	}
	if cb != nil {
		return cb(model.StreamResult{Final: true, Response: resp})
	}
	return nil
}

// ToolCallModel returns a tool call on the first invocation, then text on subsequent calls.
// Useful for testing tool execution flow through the agent loop.
type ToolCallModel struct {
	ToolName   string
	ToolParams map[string]any
	called     bool
}

func (m *ToolCallModel) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	if !m.called {
		m.called = true
		return &model.Response{
			Message: model.Message{
				Role: "assistant",
				ToolCalls: []model.ToolCall{
					{ID: "eval_call_1", Name: m.ToolName, Arguments: m.ToolParams},
				},
			},
			StopReason: "tool_use",
		}, nil
	}
	return &model.Response{
		Message:    model.Message{Role: "assistant", Content: "done"},
		StopReason: "end_turn",
	}, nil
}

func (m *ToolCallModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	resp, err := m.Complete(ctx, req)
	if err != nil {
		return err
	}
	if cb != nil {
		return cb(model.StreamResult{Final: true, Response: resp})
	}
	return nil
}

// RuntimeOption configures a test runtime.
type RuntimeOption func(*api.Options)

// WithCustomTools adds custom tools to the runtime.
func WithCustomTools(tools ...tool.Tool) RuntimeOption {
	return func(o *api.Options) {
		o.CustomTools = append(o.CustomTools, tools...)
	}
}

// WithModel sets the model for the runtime.
func WithModel(m model.Model) RuntimeOption {
	return func(o *api.Options) {
		o.Model = m
	}
}

// WithSystemPrompt sets a custom system prompt.
func WithSystemPrompt(prompt string) RuntimeOption {
	return func(o *api.Options) {
		o.SystemPrompt = prompt
	}
}

// NewTestRuntime creates a runtime with sensible defaults for offline evals.
// Uses NoopModel by default. The caller can override with WithModel().
func NewTestRuntime(t *testing.T, opts ...RuntimeOption) *api.Runtime {
	t.Helper()
	base := api.Options{
		ProjectRoot:         t.TempDir(),
		Model:               NoopModel{},
		EnabledBuiltinTools: []string{},
	}
	for _, opt := range opts {
		opt(&base)
	}
	rt, err := api.New(context.Background(), base)
	if err != nil {
		t.Fatalf("eval.NewTestRuntime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// NewLLMRuntime creates a runtime backed by a real LLM provider.
//
// Model resolution order:
//  1. modelName parameter (if non-empty)
//  2. EVAL_MODEL environment variable
//  3. ANTHROPIC_MODEL environment variable
//  4. Default: "claude-haiku-4-5-20251001"
//
// Also reads ANTHROPIC_API_KEY (or ANTHROPIC_AUTH_TOKEN) and ANTHROPIC_BASE_URL
// from environment, matching the Claude Code local configuration pattern.
// Skips the test if no API key is available.
func NewLLMRuntime(t *testing.T, modelName string, opts ...RuntimeOption) *api.Runtime {
	t.Helper()
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	}
	if apiKey == "" {
		t.Skip("skipping: ANTHROPIC_API_KEY not set")
	}

	// Resolve model name from parameter → env → default.
	if modelName == "" {
		modelName = os.Getenv("EVAL_MODEL")
	}
	if modelName == "" {
		modelName = os.Getenv("ANTHROPIC_MODEL")
	}
	if modelName == "" {
		modelName = "claude-opus-4-6"
	}

	baseURL := os.Getenv("ANTHROPIC_BASE_URL")

	provider := &model.AnthropicProvider{
		APIKey:    apiKey,
		BaseURL:   baseURL,
		ModelName: modelName,
	}
	mdl, err := provider.Model(context.Background())
	if err != nil {
		t.Fatalf("eval.NewLLMRuntime: create model: %v", err)
	}

	t.Logf("eval: using model=%s base_url=%q", modelName, baseURL)

	base := api.Options{
		ProjectRoot: t.TempDir(),
		Model:       mdl,
	}
	for _, opt := range opts {
		opt(&base)
	}
	rt, err := api.New(context.Background(), base)
	if err != nil {
		t.Fatalf("eval.NewLLMRuntime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// NewLLMModel creates a raw model.Model backed by a real Anthropic provider.
// Use this when you need to call the model directly (e.g. to inspect tool calls
// before execution). Skips the test if no API key is available.
func NewLLMModel(t *testing.T, modelName string) model.Model {
	t.Helper()
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	}
	if apiKey == "" {
		t.Skip("skipping: ANTHROPIC_API_KEY not set")
	}
	if modelName == "" {
		modelName = os.Getenv("EVAL_MODEL")
	}
	if modelName == "" {
		modelName = os.Getenv("ANTHROPIC_MODEL")
	}
	if modelName == "" {
		modelName = "claude-opus-4-6"
	}
	baseURL := os.Getenv("ANTHROPIC_BASE_URL")

	provider := &model.AnthropicProvider{
		APIKey:    apiKey,
		BaseURL:   baseURL,
		ModelName: modelName,
	}
	mdl, err := provider.Model(context.Background())
	if err != nil {
		t.Fatalf("eval.NewLLMModel: %v", err)
	}
	t.Logf("eval: using model=%s base_url=%q", modelName, baseURL)
	return mdl
}

// AssertToolCall checks that the response contains a tool call with the given name.
func AssertToolCall(t *testing.T, resp *api.Response, toolName string) {
	t.Helper()
	if resp == nil || resp.Result == nil {
		t.Fatalf("AssertToolCall: nil response")
	}
	for _, tc := range resp.Result.ToolCalls {
		if tc.Name == toolName {
			return
		}
	}
	names := make([]string, len(resp.Result.ToolCalls))
	for i, tc := range resp.Result.ToolCalls {
		names[i] = tc.Name
	}
	t.Errorf("AssertToolCall: expected tool %q, got %v", toolName, names)
}

// AssertContains checks that the response output contains the given substring.
func AssertContains(t *testing.T, resp *api.Response, substr string) {
	t.Helper()
	if resp == nil || resp.Result == nil {
		t.Fatalf("AssertContains: nil response")
	}
	if !strings.Contains(resp.Result.Output, substr) {
		t.Errorf("AssertContains: output %q does not contain %q",
			truncate(resp.Result.Output, 200), substr)
	}
}
