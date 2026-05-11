// anthropic.go: Anthropic-backed Model — config, struct, constructor, and Complete/CompleteStream entry methods.
package model

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// AnthropicConfig wires a plain anthropic-sdk-go client into the Model interface.
type AnthropicConfig struct {
	APIKey      string
	BaseURL     string
	Model       string
	MaxTokens   int
	MaxRetries  int
	System      string
	Temperature *float64
	HTTPClient  *http.Client
}

type anthropicMessages interface {
	New(ctx context.Context, params anthropicsdk.MessageNewParams, opts ...option.RequestOption) (*anthropicsdk.Message, error)
	NewStreaming(ctx context.Context, params anthropicsdk.MessageNewParams, opts ...option.RequestOption) *ssestream.Stream[anthropicsdk.MessageStreamEventUnion]
	CountTokens(ctx context.Context, params anthropicsdk.MessageCountTokensParams, opts ...option.RequestOption) (*anthropicsdk.MessageTokensCount, error)
}

type anthropicModel struct {
	msgs             anthropicMessages
	model            anthropicsdk.Model
	maxTokens        int
	maxRetries       int
	system           string
	temperature      *float64
	configuredAPIKey string
}

func (m *anthropicModel) ModelName() string  { return string(m.model) }
func (m *anthropicModel) ContextWindow() int { return LookupContextWindow(string(m.model)) }

// NewAnthropic constructs a production-ready Anthropic-backed Model.
func NewAnthropic(cfg AnthropicConfig) (Model, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("anthropic: api key required")
	}

	opts := []option.RequestOption{
		// Explicitly set the API key so it overrides any ANTHROPIC_AUTH_TOKEN
		// or ANTHROPIC_API_KEY from the environment (DefaultClientOptions).
		option.WithAPIKey(apiKey),
		// Also set auth token for providers that require Authorization: Bearer
		// (e.g. DeepSeek's Anthropic-compatible endpoint).
		option.WithAuthToken(apiKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	// Wrap the HTTP client with rate limit header capturing.
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Minute,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			},
		}
	}
	httpClient.Transport = &RateLimitCapturingTransport{Base: httpClient.Transport}
	opts = append(opts, option.WithHTTPClient(httpClient))

	client := anthropicsdk.NewClient(opts...)
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	retries := cfg.MaxRetries
	if retries <= 0 {
		retries = 10
	}

	return &anthropicModel{
		msgs:             &client.Messages,
		model:            mapModelName(cfg.Model),
		maxTokens:        maxTokens,
		maxRetries:       retries,
		system:           strings.TrimSpace(cfg.System),
		temperature:      cfg.Temperature,
		configuredAPIKey: apiKey,
	}, nil
}

// Complete issues a non-streaming completion.
func (m *anthropicModel) Complete(ctx context.Context, req Request) (*Response, error) {
	recordModelRequest(ctx, req)
	var resp *Response
	headerOpts := m.requestOptions()
	err := m.doWithRetry(ctx, func(ctx context.Context) error {
		params, err := m.buildParams(req)
		if err != nil {
			return err
		}

		msg, err := m.msgs.New(ctx, params, headerOpts...)
		if err != nil {
			return err
		}

		usage := convertUsage(msg.Usage)
		resp = &Response{
			Message:    convertResponseMessage(*msg),
			Usage:      usage,
			StopReason: string(msg.StopReason),
		}
		recordModelResponse(ctx, resp)
		return nil
	})
	return resp, err
}

// CompleteStream issues a streaming completion, forwarding deltas to cb.
func (m *anthropicModel) CompleteStream(ctx context.Context, req Request, cb StreamHandler) error {
	if cb == nil {
		return errors.New("stream callback required")
	}

	recordModelRequest(ctx, req)

	headerOpts := m.requestOptions()
	return m.doWithRetry(ctx, func(ctx context.Context) error {
		params, err := m.buildParams(req)
		if err != nil {
			return err
		}

		// Pre-count input tokens for accurate usage; ignore errors (non-fatal).
		var usage Usage
		if count, err := m.msgs.CountTokens(ctx, m.countParams(params)); err == nil && count != nil {
			usage.InputTokens = int(count.InputTokens)
			usage.TotalTokens = usage.InputTokens
		}

		stream := m.msgs.NewStreaming(ctx, params, headerOpts...)
		if stream == nil {
			return errors.New("anthropic stream not available")
		}
		defer stream.Close()

		var final anthropicsdk.Message

		for stream.Next() {
			event := stream.Current()
			// Keep aggregate message for the final output.
			if err := final.Accumulate(event); err != nil {
				return fmt.Errorf("accumulate stream: %w", err)
			}

			switch ev := event.AsAny().(type) {
			case anthropicsdk.ContentBlockDeltaEvent:
				if text := ev.Delta.AsTextDelta().Text; text != "" {
					if err := cb(StreamResult{Delta: text}); err != nil {
						return err
					}
				}
			case anthropicsdk.ContentBlockStopEvent:
				if tool := extractToolCall(final); tool != nil {
					if err := cb(StreamResult{ToolCall: tool}); err != nil {
						return err
					}
				}
			case anthropicsdk.MessageDeltaEvent:
				usage.CacheCreationTokens = int(ev.Usage.CacheCreationInputTokens)
				usage.CacheReadTokens = int(ev.Usage.CacheReadInputTokens)
				usage.InputTokens = int(ev.Usage.InputTokens)
				usage.OutputTokens = int(ev.Usage.OutputTokens)
				usage.TotalTokens = usage.InputTokens + usage.OutputTokens
			}
		}

		if err := stream.Err(); err != nil {
			return err
		}

		resp := &Response{
			Message:    convertResponseMessage(final),
			Usage:      usageFromFallback(final.Usage, usage),
			StopReason: string(final.StopReason),
		}
		recordModelResponse(ctx, resp)
		return cb(StreamResult{Final: true, Response: resp})
	})
}
