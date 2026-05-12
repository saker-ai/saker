package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestObservationSinkFunc(t *testing.T) {
	var got ObservationEvent
	var sink ObservationSink = ObservationSinkFunc(func(ev ObservationEvent) { got = ev })
	sink.OnObservation(ObservationEvent{Provider: "openai", Model: "gpt-4"})
	if got.Provider != "openai" || got.Model != "gpt-4" {
		t.Errorf("ObservationSinkFunc did not forward event: %+v", got)
	}
}

func TestSetGlobalObservationSink_NilClears(t *testing.T) {
	t.Cleanup(func() { SetGlobalObservationSink(nil) })

	SetGlobalObservationSink(ObservationSinkFunc(func(ObservationEvent) {}))
	if currentObservationSink() == nil {
		t.Fatal("sink should be set")
	}
	SetGlobalObservationSink(nil)
	if currentObservationSink() != nil {
		t.Fatal("nil store should clear the sink")
	}
}

func TestObservationPlugin_PostLLMHook_Success(t *testing.T) {
	var mu sync.Mutex
	var events []ObservationEvent
	sink := ObservationSinkFunc(func(ev ObservationEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})

	p := newObservationPlugin(schemas.Anthropic, "claude-3", sink)

	resp := &schemas.BifrostResponse{
		ChatResponse: &schemas.BifrostChatResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{
				Provider:          schemas.Anthropic,
				ResolvedModelUsed: "claude-3",
			},
			Usage: &schemas.BifrostLLMUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
				PromptTokensDetails: &schemas.ChatPromptTokensDetails{
					CachedReadTokens:  20,
					CachedWriteTokens: 30,
				},
			},
		},
	}

	if _, _, err := p.PostLLMHook(nil, resp, nil); err != nil {
		t.Fatalf("PostLLMHook returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Provider != "anthropic" || ev.Model != "claude-3" {
		t.Errorf("provider/model: got %s/%s", ev.Provider, ev.Model)
	}
	if ev.RequestedProvider != "anthropic" || ev.RequestedModel != "claude-3" {
		t.Errorf("requested provider/model: got %s/%s", ev.RequestedProvider, ev.RequestedModel)
	}
	if ev.UsedFallback {
		t.Error("UsedFallback should be false when primary served")
	}
	if ev.InputTokens != 100 || ev.OutputTokens != 50 || ev.TotalTokens != 150 {
		t.Errorf("token counts: in=%d out=%d total=%d", ev.InputTokens, ev.OutputTokens, ev.TotalTokens)
	}
	if ev.CacheReadTokens != 20 || ev.CacheWriteTokens != 30 {
		t.Errorf("cache tokens: read=%d write=%d", ev.CacheReadTokens, ev.CacheWriteTokens)
	}
	if ev.StatusCode != 0 || ev.ErrorMessage != "" {
		t.Errorf("status/error should be empty on success: status=%d msg=%q", ev.StatusCode, ev.ErrorMessage)
	}
}

func TestObservationPlugin_PostLLMHook_Fallback(t *testing.T) {
	var ev ObservationEvent
	sink := ObservationSinkFunc(func(e ObservationEvent) { ev = e })
	p := newObservationPlugin(schemas.Anthropic, "claude-3", sink)

	resp := &schemas.BifrostResponse{
		ChatResponse: &schemas.BifrostChatResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{
				Provider:          schemas.OpenAI,
				ResolvedModelUsed: "gpt-4",
			},
		},
	}
	if _, _, err := p.PostLLMHook(nil, resp, nil); err != nil {
		t.Fatalf("PostLLMHook: %v", err)
	}
	if !ev.UsedFallback {
		t.Error("expected UsedFallback=true when resolved provider differs from primary")
	}
	if ev.Provider != "openai" || ev.Model != "gpt-4" {
		t.Errorf("expected resolved openai/gpt-4, got %s/%s", ev.Provider, ev.Model)
	}
}

func TestObservationPlugin_PostLLMHook_Error(t *testing.T) {
	var ev ObservationEvent
	sink := ObservationSinkFunc(func(e ObservationEvent) { ev = e })
	p := newObservationPlugin(schemas.OpenAI, "gpt-4", sink)

	statusCode := 429
	errType := "rate_limit_exceeded"
	bErr := &schemas.BifrostError{
		Type:       &errType,
		StatusCode: &statusCode,
		Error: &schemas.ErrorField{
			Message: "rate limit hit",
			Error:   errors.New("rate limit hit"),
		},
		ExtraFields: schemas.BifrostErrorExtraFields{
			Provider:          schemas.OpenAI,
			ResolvedModelUsed: "gpt-4",
		},
	}
	if _, _, err := p.PostLLMHook(nil, nil, bErr); err != nil {
		t.Fatalf("PostLLMHook: %v", err)
	}
	if ev.StatusCode != 429 {
		t.Errorf("status: got %d, want 429", ev.StatusCode)
	}
	if ev.ErrorType != "rate_limit_exceeded" {
		t.Errorf("errType: got %q", ev.ErrorType)
	}
	if ev.ErrorMessage != "rate limit hit" {
		t.Errorf("errMsg: got %q", ev.ErrorMessage)
	}
	if ev.Provider != "openai" || ev.Model != "gpt-4" {
		t.Errorf("provider/model on error path: got %s/%s", ev.Provider, ev.Model)
	}
}

func TestObservationPlugin_PostLLMHook_NilSink(t *testing.T) {
	p := newObservationPlugin(schemas.Anthropic, "claude-3", nil)
	if _, _, err := p.PostLLMHook(nil, nil, nil); err != nil {
		t.Fatalf("PostLLMHook with nil sink should be a no-op, got error: %v", err)
	}
}

func TestObservationPlugin_BasePlugin(t *testing.T) {
	p := newObservationPlugin(schemas.Anthropic, "claude-3", nil)
	if p.GetName() == "" {
		t.Error("GetName must be non-empty")
	}
	if err := p.Cleanup(); err != nil {
		t.Errorf("Cleanup returned error: %v", err)
	}
}
