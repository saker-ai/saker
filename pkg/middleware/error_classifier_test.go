package middleware

import (
	"errors"
	"testing"
)

func TestClassifyError_Nil(t *testing.T) {
	t.Parallel()
	if ClassifyError(nil) != nil {
		t.Error("expected nil for nil error")
	}
}

func TestClassifyErrorString_Empty(t *testing.T) {
	t.Parallel()
	if ClassifyErrorString("") != nil {
		t.Error("expected nil for empty string")
	}
}

func TestClassifyError_Categories(t *testing.T) {
	t.Parallel()
	tests := []struct {
		msg      string
		category ErrorCategory
		retry    bool
	}{
		{"invalid x-api-key header", ErrorCategoryAuth, false},
		{"401 Unauthorized", ErrorCategoryAuth, false},
		{"rate limit exceeded", ErrorCategoryRateLimit, true},
		{"429 Too Many Requests", ErrorCategoryRateLimit, true},
		{"context deadline exceeded", ErrorCategoryTimeout, true},
		{"connection refused", ErrorCategoryNetwork, true},
		{"no such host", ErrorCategoryNetwork, true},
		{"permission denied", ErrorCategoryPermission, false},
		{"403 Forbidden", ErrorCategoryPermission, false},
		{"file not found", ErrorCategoryNotFound, false},
		{"no such file or directory", ErrorCategoryNotFound, false},
		{"invalid parameter value", ErrorCategoryValidation, false},
		{"400 Bad Request", ErrorCategoryValidation, false},
		{"context window exceeded", ErrorCategoryModel, false},
		{"command failed with exit code 1", ErrorCategoryTool, false},
		{"blocked by sandbox policy", ErrorCategorySandbox, false},
		{"context canceled", ErrorCategoryTimeout, false},
	}

	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			t.Parallel()
			classified := ClassifyErrorString(tt.msg)
			if classified == nil {
				t.Fatal("expected non-nil classification")
			}
			if classified.Category != tt.category {
				t.Errorf("got category %q, want %q", classified.Category, tt.category)
			}
			if classified.Retryable != tt.retry {
				t.Errorf("got retryable %v, want %v", classified.Retryable, tt.retry)
			}
			if classified.Recovery == "" {
				t.Error("expected non-empty recovery suggestion")
			}
			if classified.Original != tt.msg {
				t.Errorf("got original %q, want %q", classified.Original, tt.msg)
			}
		})
	}
}

func TestClassifyError_Unknown(t *testing.T) {
	t.Parallel()
	classified := ClassifyErrorString("some random error that matches nothing specific")
	if classified == nil {
		t.Fatal("expected non-nil classification")
	}
	if classified.Category != ErrorCategoryUnknown {
		t.Errorf("got category %q, want %q", classified.Category, ErrorCategoryUnknown)
	}
}

func TestClassifyError_FromError(t *testing.T) {
	t.Parallel()
	err := errors.New("rate limit exceeded for model")
	classified := ClassifyError(err)
	if classified == nil {
		t.Fatal("expected non-nil classification")
	}
	if classified.Category != ErrorCategoryRateLimit {
		t.Errorf("got %q, want %q", classified.Category, ErrorCategoryRateLimit)
	}
}

func TestErrorClassifierMiddleware_Name(t *testing.T) {
	t.Parallel()
	m := NewErrorClassifier()
	if m.Name() != "error_classifier" {
		t.Errorf("got %q, want %q", m.Name(), "error_classifier")
	}
}

func TestErrorClassifierMiddleware_AfterTool_WithError(t *testing.T) {
	t.Parallel()
	m := NewErrorClassifier()
	st := &State{
		ToolResult: map[string]any{"error": "rate limit exceeded"},
		Values:     map[string]any{},
	}
	if err := m.AfterTool(nil, st); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classified, ok := st.Values["error_classification"].(*ClassifiedError)
	if !ok {
		t.Fatal("expected classified error in state")
	}
	if classified.Category != ErrorCategoryRateLimit {
		t.Errorf("got %q, want %q", classified.Category, ErrorCategoryRateLimit)
	}
}

func TestErrorClassifierMiddleware_AfterTool_NoError(t *testing.T) {
	t.Parallel()
	m := NewErrorClassifier()
	st := &State{
		ToolResult: map[string]any{"output": "success"},
		Values:     map[string]any{},
	}
	if err := m.AfterTool(nil, st); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := st.Values["error_classification"]; ok {
		t.Error("should not classify when no error in result")
	}
}

func TestErrorClassifierMiddleware_AfterModel_Nil(t *testing.T) {
	t.Parallel()
	m := NewErrorClassifier()
	st := &State{Values: map[string]any{}}
	if err := m.AfterModel(nil, st); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := st.Values["error_classification"]; ok {
		t.Error("should not classify when model output is nil")
	}
}
