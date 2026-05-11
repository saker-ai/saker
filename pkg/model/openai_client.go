package model

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
)

// openai_client.go contains the package-private client wiring: configuration,
// constructor, retry/backoff loop, and request-option assembly. The actual
// per-call request building lives in openai_request.go, response decoding in
// openai_response.go, and streaming-specific code in openai_stream.go.

// OpenAIConfig configures the OpenAI-backed Model.
type OpenAIConfig struct {
	APIKey       string
	BaseURL      string // Optional: for Azure or proxies
	Model        string // e.g., "gpt-4o", "gpt-4-turbo"
	MaxTokens    int
	MaxRetries   int
	System       string
	Temperature  *float64
	HTTPClient   *http.Client
	UseResponses bool // true = /responses API, false = /chat/completions
	// ExtraBody injects vendor-specific top-level JSON fields into every
	// request body via option.WithJSONSet. Used for Dashscope-flavored
	// extensions like {"enable_thinking": true} that aren't part of the
	// OpenAI schema. Each entry is applied as a separate WithJSONSet call.
	ExtraBody map[string]any
}

type openaiChatCompletions interface {
	New(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) (*openai.ChatCompletion, error)
	NewStreaming(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) *ssestream.Stream[openai.ChatCompletionChunk]
}

type openaiModel struct {
	completions openaiChatCompletions
	model       string
	maxTokens   int
	maxRetries  int
	system      string
	temperature *float64
	extraBody   map[string]any
}

func (m *openaiModel) ModelName() string  { return m.model }
func (m *openaiModel) ContextWindow() int { return LookupContextWindow(m.model) }

const (
	defaultOpenAIModel      = "gpt-4o"
	defaultOpenAIMaxTokens  = 4096
	defaultOpenAIMaxRetries = 10
)

// NewOpenAI constructs a production-ready OpenAI-backed Model.
func NewOpenAI(cfg OpenAIConfig) (Model, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("openai: api key required")
	}

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}

	client := openai.NewClient(opts...)
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultOpenAIMaxTokens
	}
	retries := cfg.MaxRetries
	if retries <= 0 {
		retries = defaultOpenAIMaxRetries
	}

	modelName := strings.TrimSpace(cfg.Model)
	if modelName == "" {
		modelName = defaultOpenAIModel
	}

	// Defensive copy so callers can mutate their config map afterwards
	// without racing with in-flight requests.
	var extraBody map[string]any
	if len(cfg.ExtraBody) > 0 {
		extraBody = make(map[string]any, len(cfg.ExtraBody))
		for k, v := range cfg.ExtraBody {
			extraBody[k] = v
		}
	}

	return &openaiModel{
		completions: &client.Chat.Completions,
		model:       modelName,
		maxTokens:   maxTokens,
		maxRetries:  retries,
		system:      strings.TrimSpace(cfg.System),
		temperature: cfg.Temperature,
		extraBody:   extraBody,
	}, nil
}

// extraBodyOpts builds one option.WithJSONSet per ExtraBody entry. Returned
// as a slice so callers can spread it into the SDK call alongside their own
// per-request options. Returns nil when there are no extras (cheap zero-alloc
// happy path for vanilla OpenAI usage).
func (m *openaiModel) extraBodyOpts() []option.RequestOption {
	if len(m.extraBody) == 0 {
		return nil
	}
	opts := make([]option.RequestOption, 0, len(m.extraBody))
	for k, v := range m.extraBody {
		opts = append(opts, option.WithJSONSet(k, v))
	}
	return opts
}

func (m *openaiModel) doWithRetry(ctx context.Context, fn func(context.Context) error) error {
	attempts := 0
	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isOpenAIRetryable(err) || attempts >= m.maxRetries {
			return err
		}
		attempts++
		backoff := time.Duration(attempts*attempts) * 100 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

func isOpenAIRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		// Don't retry authentication errors
		return apiErr.StatusCode != http.StatusUnauthorized
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		//nolint:staticcheck // Temporary is deprecated but retained for transient errors
		return netErr.Temporary()
	}
	return true
}

func (m *openaiModel) selectModel(override string) string {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return trimmed
	}
	return m.model
}
