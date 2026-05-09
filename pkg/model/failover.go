package model

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

// FailoverConfig configures the failover model wrapper.
type FailoverConfig struct {
	Models      []Model                                       // Ordered model list (primary + fallbacks)
	MaxRetries  int                                           // Max retries per model (default 2)
	BackoffBase time.Duration                                 // Base backoff between retries (default 500ms)
	OnFailover  func(from, to string, reason ClassifiedError) // Optional callback on model switch
}

// failoverModel wraps multiple models with automatic failover on classified errors.
type failoverModel struct {
	config  FailoverConfig
	current int
	mu      sync.Mutex
}

// NewFailoverModel creates a failover-aware Model that tries fallback models on failure.
// Returns an error if cfg.Models is empty. If only one model is provided, it still
// wraps it to benefit from error classification and retry logic.
func NewFailoverModel(cfg FailoverConfig) (Model, error) {
	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("failover: at least one model is required")
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 2
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 500 * time.Millisecond
	}
	return &failoverModel{config: cfg}, nil
}

func (f *failoverModel) Complete(ctx context.Context, req Request) (*Response, error) {
	return f.execute(ctx, req, false, nil)
}

func (f *failoverModel) CompleteStream(ctx context.Context, req Request, cb StreamHandler) error {
	// Use a buffering wrapper so partial stream output from a failed model
	// is discarded rather than sent to the downstream consumer. On success,
	// the buffered chunks are flushed to cb in one pass, preventing garbled
	// output across model retries/failovers.
	_, err := f.execute(ctx, req, true, cb)
	return err
}

// bufferingStreamHandler collects stream results in memory. On flush they are
// forwarded to the downstream handler; on discard they are silently dropped.
// This prevents partial output from a failed model attempt from reaching the
// consumer when failover retries with a different model.
type bufferingStreamHandler struct {
	chunks     []StreamResult
	downstream StreamHandler
}

func (b *bufferingStreamHandler) Handle(result StreamResult) error {
	b.chunks = append(b.chunks, result)
	return nil
}

func (b *bufferingStreamHandler) Flush() error {
	for _, chunk := range b.chunks {
		if err := b.downstream(chunk); err != nil {
			return err
		}
	}
	b.chunks = nil
	return nil
}

func (b *bufferingStreamHandler) Discard() {
	b.chunks = nil
}

func (f *failoverModel) execute(ctx context.Context, req Request, stream bool, cb StreamHandler) (*Response, error) {
	f.mu.Lock()
	startIdx := f.current
	f.mu.Unlock()

	models := f.config.Models
	totalModels := len(models)
	var lastErr error

	// For streaming, wrap cb in a buffering handler so partial output from
	// a failed model attempt is discarded rather than delivered downstream.
	var buf *bufferingStreamHandler
	if stream && cb != nil {
		buf = &bufferingStreamHandler{downstream: cb}
	}

	for i := 0; i < totalModels; i++ {
		idx := (startIdx + i) % totalModels
		m := models[idx]

		streamCb := cb
		if buf != nil {
			streamCb = buf.Handle
		}

		resp, err := f.tryModel(ctx, m, req, stream, streamCb)
		if err == nil {
			// Success — flush buffered stream output and pin to this model.
			if buf != nil {
				if flushErr := buf.Flush(); flushErr != nil {
					return nil, flushErr
				}
			}
			f.mu.Lock()
			f.current = idx
			f.mu.Unlock()
			return resp, nil
		}
		lastErr = err

		// Discard partial stream output from the failed attempt before retrying.
		if buf != nil {
			buf.Discard()
		}

		classified := ClassifyError(err)
		fromName := modelName(m)
		slog.Warn("failover: model call failed",
			"model", fromName,
			"reason", classified.Reason,
			"status", classified.StatusCode,
			"retryable", classified.Retryable,
			"fallback", classified.ShouldFallback,
		)

		// If not a fallback-worthy error, don't try next model.
		if !classified.ShouldFallback {
			return nil, err
		}

		// Check context before trying next model.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Notify callback about failover.
		if i+1 < totalModels && f.config.OnFailover != nil {
			nextIdx := (startIdx + i + 1) % totalModels
			toName := modelName(models[nextIdx])
			f.config.OnFailover(fromName, toName, classified)
		}
	}

	return nil, fmt.Errorf("failover: all %d models exhausted: %w", totalModels, lastErr)
}

// tryModel attempts a single model with retries on retryable errors.
func (f *failoverModel) tryModel(ctx context.Context, m Model, req Request, stream bool, cb StreamHandler) (*Response, error) {
	var lastErr error

	for attempt := 0; attempt <= f.config.MaxRetries; attempt++ {
		if attempt > 0 {
			classified := ClassifyError(lastErr)
			if !classified.Retryable {
				return nil, lastErr
			}
			backoff := f.backoff(attempt)
			slog.Debug("failover: retrying",
				"model", modelName(m),
				"attempt", attempt,
				"backoff", backoff,
			)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		var resp *Response
		var err error
		if stream {
			err = m.CompleteStream(ctx, req, cb)
		} else {
			resp, err = m.Complete(ctx, req)
		}
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}

	return nil, lastErr
}

func (f *failoverModel) backoff(attempt int) time.Duration {
	// Exponential backoff: base * 2^(attempt-1), capped at 30s.
	d := f.config.BackoffBase * time.Duration(math.Pow(2, float64(attempt-1)))
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// ModelName delegates to the current active model.
func (f *failoverModel) ModelName() string {
	f.mu.Lock()
	idx := f.current
	f.mu.Unlock()
	return modelName(f.config.Models[idx])
}

// ContextWindow delegates to the current active model.
func (f *failoverModel) ContextWindow() int {
	f.mu.Lock()
	idx := f.current
	f.mu.Unlock()
	if cw, ok := f.config.Models[idx].(ContextWindowProvider); ok {
		return cw.ContextWindow()
	}
	return 0
}

// modelName extracts the name from a model if it implements ModelNamer.
func modelName(m Model) string {
	if n, ok := m.(ModelNamer); ok {
		return n.ModelName()
	}
	return fmt.Sprintf("%T", m)
}
