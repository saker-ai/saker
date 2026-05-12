package openai

import (
	"encoding/json"
	"reflect"
	"testing"
)

func float64Ptr(v float64) *float64 { return &v }
func intPtr(v int) *int             { return &v }
func boolPtr(v bool) *bool          { return &v }

func TestBuildModelOverrides_NoOverridesReturnsNil(t *testing.T) {
	t.Parallel()
	if got := buildModelOverrides(ChatRequest{Model: "saker-mid"}); got != nil {
		t.Errorf("expected nil overrides for empty request, got %+v", got)
	}
}

func TestBuildModelOverrides_AllFields(t *testing.T) {
	t.Parallel()
	req := ChatRequest{
		Temperature:       float64Ptr(0.25),
		TopP:              float64Ptr(0.9),
		MaxTokens:         512,
		Stop:              []any{"END", "STOP"},
		Seed:              intPtr(7),
		ToolChoice:        "auto",
		ParallelToolCalls: boolPtr(false),
	}
	got := buildModelOverrides(req)
	if got == nil {
		t.Fatal("expected non-nil overrides")
	}
	if got.Temperature == nil || *got.Temperature != 0.25 {
		t.Errorf("Temperature = %v, want 0.25", got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.9 {
		t.Errorf("TopP = %v, want 0.9", got.TopP)
	}
	if got.MaxTokens == nil || *got.MaxTokens != 512 {
		t.Errorf("MaxTokens = %v, want 512", got.MaxTokens)
	}
	if !reflect.DeepEqual(got.Stop, []string{"END", "STOP"}) {
		t.Errorf("Stop = %v, want [END STOP]", got.Stop)
	}
	if got.Seed == nil || *got.Seed != 7 {
		t.Errorf("Seed = %v, want 7", got.Seed)
	}
	if got.ToolChoice != "auto" {
		t.Errorf("ToolChoice = %q, want auto", got.ToolChoice)
	}
	if got.ParallelToolCalls == nil || *got.ParallelToolCalls {
		t.Errorf("ParallelToolCalls = %v, want false ptr", got.ParallelToolCalls)
	}
}

func TestBuildModelOverrides_MaxCompletionTokensWins(t *testing.T) {
	t.Parallel()
	got := buildModelOverrides(ChatRequest{MaxTokens: 100, MaxCompletionT: 200})
	if got == nil || got.MaxTokens == nil || *got.MaxTokens != 200 {
		t.Errorf("max_completion_tokens should override max_tokens; got %+v", got)
	}
	got = buildModelOverrides(ChatRequest{MaxTokens: 100})
	if got == nil || got.MaxTokens == nil || *got.MaxTokens != 100 {
		t.Errorf("max_tokens fallback failed; got %+v", got)
	}
	if got := buildModelOverrides(ChatRequest{MaxTokens: 0, MaxCompletionT: -3}); got != nil {
		t.Errorf("non-positive max tokens should be ignored, got %+v", got)
	}
}

func TestBuildModelOverrides_StopAsString(t *testing.T) {
	t.Parallel()
	got := buildModelOverrides(ChatRequest{Stop: "FIN"})
	if got == nil || !reflect.DeepEqual(got.Stop, []string{"FIN"}) {
		t.Errorf("Stop scalar string should produce [FIN]; got %+v", got)
	}
}

func TestBuildModelOverrides_StopEmptyDropped(t *testing.T) {
	t.Parallel()
	if got := buildModelOverrides(ChatRequest{Stop: ""}); got != nil {
		t.Errorf("empty stop string should yield no overrides, got %+v", got)
	}
	if got := buildModelOverrides(ChatRequest{Stop: []any{}}); got != nil {
		t.Errorf("empty stop array should yield no overrides, got %+v", got)
	}
}

func TestBuildModelOverrides_StopRawJSON(t *testing.T) {
	t.Parallel()
	got := buildModelOverrides(ChatRequest{Stop: json.RawMessage(`["A","B"]`)})
	if got == nil || !reflect.DeepEqual(got.Stop, []string{"A", "B"}) {
		t.Errorf("RawMessage stop array failed; got %+v", got)
	}
	got = buildModelOverrides(ChatRequest{Stop: json.RawMessage(`"X"`)})
	if got == nil || !reflect.DeepEqual(got.Stop, []string{"X"}) {
		t.Errorf("RawMessage stop string failed; got %+v", got)
	}
}

func TestBuildModelOverrides_ToolChoiceStructIgnored(t *testing.T) {
	t.Parallel()
	// Struct form is forward-compat work — must NOT crash and MUST NOT
	// pretend to forward what we don't yet support.
	got := buildModelOverrides(ChatRequest{ToolChoice: map[string]any{
		"type":     "function",
		"function": map[string]any{"name": "do_thing"},
	}})
	if got != nil {
		t.Errorf("struct tool_choice must yield nil overrides today, got %+v", got)
	}
}

func TestCoerceStop_BogusShapeReturnsNil(t *testing.T) {
	t.Parallel()
	if got := coerceStop(42); got != nil {
		t.Errorf("integer stop should be nil, got %v", got)
	}
	if got := coerceStop(map[string]any{"k": "v"}); got != nil {
		t.Errorf("map stop should be nil, got %v", got)
	}
	if got := coerceStop(nil); got != nil {
		t.Errorf("nil stop should be nil, got %v", got)
	}
}

func TestStringFromAny(t *testing.T) {
	t.Parallel()
	if s, ok := stringFromAny("hi"); !ok || s != "hi" {
		t.Errorf("plain string failed: %q ok=%v", s, ok)
	}
	if s, ok := stringFromAny(json.RawMessage(`"hi"`)); !ok || s != "hi" {
		t.Errorf("raw json string failed: %q ok=%v", s, ok)
	}
	if _, ok := stringFromAny(42); ok {
		t.Errorf("non-string should not coerce")
	}
	if _, ok := stringFromAny(nil); ok {
		t.Errorf("nil should not coerce")
	}
}

func TestFilterNonEmpty(t *testing.T) {
	t.Parallel()
	if got := filterNonEmpty([]string{"", "a", "", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("filterNonEmpty = %v, want [a b]", got)
	}
	if got := filterNonEmpty([]string{"", ""}); got != nil {
		t.Errorf("all-empty should yield nil, got %v", got)
	}
}
