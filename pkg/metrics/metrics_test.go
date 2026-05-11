package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestClassifyErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, StatusOK},
		{"canceled", context.Canceled, StatusCanceled},
		{"deadline", context.DeadlineExceeded, StatusCanceled},
		{"wrapped canceled", errors.Join(errors.New("wrap"), context.Canceled), StatusCanceled},
		{"plain error", errors.New("boom"), StatusError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyErr(tc.err); got != tc.want {
				t.Fatalf("ClassifyErr(%v)=%q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestSanitizeProvider(t *testing.T) {
	cases := map[string]string{
		"":          "unknown",
		"ANTHROPIC": "anthropic",
		"openai":    "openai",
		"DashScope": "dashscope",
		"unknown-x": "other",
		"  openai ": "openai",
	}
	for in, want := range cases {
		if got := SanitizeProvider(in); got != want {
			t.Fatalf("SanitizeProvider(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestSanitizeModel(t *testing.T) {
	cases := map[string]string{
		"":                              "unknown",
		"claude-sonnet-4-20250514":      "claude-sonnet",
		"claude-opus-4.7":               "claude-opus",
		"gpt-4o-mini":                   "gpt-4o",
		"gpt-4.1":                       "gpt-4.1",
		"o3-mini-high":                  "o3-mini",
		"qwen2.5-coder-32b":             "qwen",
		"some-future-model":             "other",
		"DEEPSEEK-CHAT":                 "deepseek",
		"  Claude-Haiku-4.5-20251001 ": "claude-haiku",
	}
	for in, want := range cases {
		if got := SanitizeModel(in); got != want {
			t.Fatalf("SanitizeModel(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestObserveSince(t *testing.T) {
	h := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "test_observe_since_seconds",
		Help:    "test",
		Buckets: prometheus.DefBuckets,
	})
	start := time.Now().Add(-time.Millisecond * 10)
	ObserveSince(h, start)

	var m dto.Metric
	if err := h.Write(&m); err != nil {
		t.Fatalf("histogram write: %v", err)
	}
	if m.Histogram == nil || m.Histogram.GetSampleCount() != 1 {
		t.Fatalf("expected 1 sample, got %+v", m.Histogram)
	}
}

func TestRegistrationIdempotent(t *testing.T) {
	// init() must have registered all vecs already; re-registering must
	// return prometheus.AlreadyRegisteredError, never panic.
	err := prometheus.Register(SessionsActive)
	if err == nil {
		t.Fatal("expected AlreadyRegisteredError")
	}
	var dup prometheus.AlreadyRegisteredError
	if !errors.As(err, &dup) {
		t.Fatalf("expected AlreadyRegisteredError, got %T: %v", err, err)
	}
}
