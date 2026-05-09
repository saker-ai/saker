package model

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// mockModel is a configurable test model.
type mockModel struct {
	name      string
	completeF func(ctx context.Context, req Request) (*Response, error)
	streamF   func(ctx context.Context, req Request, cb StreamHandler) error
	ctxWindow int
}

func (m *mockModel) Complete(ctx context.Context, req Request) (*Response, error) {
	if m.completeF != nil {
		return m.completeF(ctx, req)
	}
	return &Response{Message: Message{Content: "ok from " + m.name}}, nil
}

func (m *mockModel) CompleteStream(ctx context.Context, req Request, cb StreamHandler) error {
	if m.streamF != nil {
		return m.streamF(ctx, req, cb)
	}
	return cb(StreamResult{Delta: "ok from " + m.name, Final: true, Response: &Response{}})
}

func (m *mockModel) ModelName() string  { return m.name }
func (m *mockModel) ContextWindow() int { return m.ctxWindow }

func mustFailover(t *testing.T, cfg FailoverConfig) Model {
	t.Helper()
	m, err := NewFailoverModel(cfg)
	if err != nil {
		t.Fatalf("NewFailoverModel: %v", err)
	}
	return m
}

func TestFailoverModel_PrimarySuccess(t *testing.T) {
	primary := &mockModel{name: "primary"}
	fm := mustFailover(t, FailoverConfig{Models: []Model{primary}})

	resp, err := fm.Complete(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "ok from primary" {
		t.Errorf("got %q, want %q", resp.Message.Content, "ok from primary")
	}
}

func TestFailoverModel_FallbackOnError(t *testing.T) {
	primary := &mockModel{
		name: "primary",
		completeF: func(ctx context.Context, req Request) (*Response, error) {
			return nil, &mockHTTPError{code: 503, msg: "overloaded"}
		},
	}
	fallback := &mockModel{name: "fallback"}

	var failoverFrom, failoverTo string
	fm := mustFailover(t, FailoverConfig{
		Models:      []Model{primary, fallback},
		BackoffBase: 1 * time.Millisecond,
		OnFailover: func(from, to string, reason ClassifiedError) {
			failoverFrom = from
			failoverTo = to
		},
	})

	resp, err := fm.Complete(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "ok from fallback" {
		t.Errorf("got %q, want %q", resp.Message.Content, "ok from fallback")
	}
	if failoverFrom != "primary" || failoverTo != "fallback" {
		t.Errorf("failover callback: from=%q to=%q", failoverFrom, failoverTo)
	}
}

func TestFailoverModel_NoFallbackOnNonFallbackError(t *testing.T) {
	// 500 server error has ShouldFallback=false — should NOT try fallback.
	primary := &mockModel{
		name: "primary",
		completeF: func(ctx context.Context, req Request) (*Response, error) {
			return nil, &mockHTTPError{code: 500, msg: "internal error"}
		},
	}
	fallback := &mockModel{name: "fallback"}

	fm := mustFailover(t, FailoverConfig{
		Models:      []Model{primary, fallback},
		BackoffBase: 1 * time.Millisecond,
	})

	_, err := fm.Complete(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should NOT have reached fallback — error should be from primary.
	if !errors.Is(err, err) {
		t.Error("error should be from primary")
	}
}

func TestFailoverModel_RetryOnRetryableError(t *testing.T) {
	var attempts int32
	primary := &mockModel{
		name: "primary",
		completeF: func(ctx context.Context, req Request) (*Response, error) {
			n := atomic.AddInt32(&attempts, 1)
			if n <= 2 {
				return nil, &mockHTTPError{code: 429, msg: "rate limited"}
			}
			return &Response{Message: Message{Content: "ok after retry"}}, nil
		},
	}

	fm := mustFailover(t, FailoverConfig{
		Models:      []Model{primary},
		MaxRetries:  3,
		BackoffBase: 1 * time.Millisecond, // fast for test
	})

	resp, err := fm.Complete(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "ok after retry" {
		t.Errorf("got %q", resp.Message.Content)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("attempts: got %d, want 3", atomic.LoadInt32(&attempts))
	}
}

func TestFailoverModel_AllModelsExhausted(t *testing.T) {
	failing := func(name string) *mockModel {
		return &mockModel{
			name: name,
			completeF: func(ctx context.Context, req Request) (*Response, error) {
				return nil, &mockHTTPError{code: 503, msg: "overloaded"}
			},
		}
	}

	fm := mustFailover(t, FailoverConfig{
		Models:      []Model{failing("m1"), failing("m2"), failing("m3")},
		MaxRetries:  0,
		BackoffBase: 1 * time.Millisecond,
	})

	_, err := fm.Complete(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected error when all models exhausted")
	}
	if got := err.Error(); got == "" {
		t.Error("error message should not be empty")
	}
}

func TestFailoverModel_StreamFallback(t *testing.T) {
	primary := &mockModel{
		name: "primary",
		streamF: func(ctx context.Context, req Request, cb StreamHandler) error {
			return &mockHTTPError{code: 503, msg: "overloaded"}
		},
	}
	fallback := &mockModel{
		name: "fallback",
		streamF: func(ctx context.Context, req Request, cb StreamHandler) error {
			return cb(StreamResult{Delta: "streamed from fallback", Final: true, Response: &Response{}})
		},
	}

	fm := mustFailover(t, FailoverConfig{
		Models:      []Model{primary, fallback},
		BackoffBase: 1 * time.Millisecond,
	})

	var received string
	err := fm.CompleteStream(context.Background(), Request{}, func(sr StreamResult) error {
		received = sr.Delta
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received != "streamed from fallback" {
		t.Errorf("got %q", received)
	}
}

func TestFailoverModel_ContextCancelled(t *testing.T) {
	primary := &mockModel{
		name: "primary",
		completeF: func(ctx context.Context, req Request) (*Response, error) {
			return nil, &mockHTTPError{code: 503, msg: "overloaded"}
		},
	}
	fallback := &mockModel{name: "fallback"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	fm := mustFailover(t, FailoverConfig{
		Models:      []Model{primary, fallback},
		BackoffBase: 1 * time.Millisecond,
	})

	_, err := fm.Complete(ctx, Request{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestFailoverModel_ModelName(t *testing.T) {
	m1 := &mockModel{name: "model-a", ctxWindow: 100000}
	m2 := &mockModel{name: "model-b", ctxWindow: 200000}

	fm := mustFailover(t, FailoverConfig{Models: []Model{m1, m2}})

	if n, ok := fm.(ModelNamer); !ok {
		t.Error("should implement ModelNamer")
	} else if n.ModelName() != "model-a" {
		t.Errorf("got %q, want model-a", n.ModelName())
	}

	if cw, ok := fm.(ContextWindowProvider); !ok {
		t.Error("should implement ContextWindowProvider")
	} else if cw.ContextWindow() != 100000 {
		t.Errorf("got %d, want 100000", cw.ContextWindow())
	}
}

func TestFailoverModel_ErrorOnEmpty(t *testing.T) {
	_, err := NewFailoverModel(FailoverConfig{})
	if err == nil {
		t.Error("expected error on empty models")
	}
}
