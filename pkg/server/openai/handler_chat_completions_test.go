package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/runhub"
	"github.com/gin-gonic/gin"
)

// fakeRunner is a scripted Runner used by handler tests. It returns the
// channel the test pre-loaded; closing the channel signals end of stream.
type fakeRunner struct {
	events  chan api.StreamEvent
	startCh chan struct{} // closed once RunStream is called (so tests can sync)
	startCt int
	failOn  func(req api.Request) error // optional: when set, RunStream returns this error
	gotReq  api.Request                 // captured request (post-MessagesToRequest)
}

func (f *fakeRunner) RunStream(ctx context.Context, req api.Request) (<-chan api.StreamEvent, error) {
	f.startCt++
	f.gotReq = req
	if f.startCh != nil {
		close(f.startCh)
		f.startCh = nil
	}
	if f.failOn != nil {
		if err := f.failOn(req); err != nil {
			return nil, err
		}
	}
	return f.events, nil
}

func newFakeRunnerStream(events ...api.StreamEvent) *fakeRunner {
	ch := make(chan api.StreamEvent, len(events)+1)
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return &fakeRunner{events: ch}
}

// newTestHandlerGateway wires a Gateway with a fake runner and no project
// store (anonymous auth). Only the chat-completions route is mounted.
func newTestHandlerGateway(t *testing.T, runner Runner) (*Gateway, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	deps := Deps{
		Runtime: runner,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options: Options{
			Enabled:              true,
			MaxRuns:              10,
			MaxRunsPerTenant:     5,
			RingSize:             64,
			ExpiresAfterSeconds:  60,
			MaxRequestBodyBytes:  1024 * 1024,
			ErrorDetailMode:      "dev",
			DevBypassAuth:        true, // gives a localhost identity to every request
		},
	}
	if err := deps.Options.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	hub := runhub.NewHub(runhub.Config{
		MaxRuns:          deps.Options.MaxRuns,
		MaxRunsPerTenant: deps.Options.MaxRunsPerTenant,
		RingSize:         deps.Options.RingSize,
		Logger:           deps.Logger,
	})
	t.Cleanup(hub.Shutdown)

	gw := &Gateway{deps: deps, hub: hub}

	eng := gin.New()
	v1 := eng.Group("/v1")
	v1.Use(gw.authMiddleware())
	v1.POST("/chat/completions", gw.handleChatCompletions)
	return gw, eng
}

// chatBody is a small helper to JSON-marshal a request body.
func chatBody(t *testing.T, m map[string]any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return bytes.NewReader(b)
}

func TestHandleChatCompletions_MissingModel(t *testing.T) {
	t.Parallel()
	_, eng := newTestHandlerGateway(t, newFakeRunnerStream())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model") {
		t.Errorf("error body should mention model, got: %s", rec.Body.String())
	}
}

func TestHandleChatCompletions_EmptyMessages(t *testing.T) {
	t.Parallel()
	_, eng := newTestHandlerGateway(t, newFakeRunnerStream())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"messages": []any{},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleChatCompletions_BadJSON(t *testing.T) {
	t.Parallel()
	_, eng := newTestHandlerGateway(t, newFakeRunnerStream())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte("not json {")))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleChatCompletions_InvalidExtraBody(t *testing.T) {
	t.Parallel()
	_, eng := newTestHandlerGateway(t, newFakeRunnerStream())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"extra_body": map[string]any{
			"human_input_mode": "bogus",
		},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleChatCompletions_ToolCallModeAccepted(t *testing.T) {
	t.Parallel()
	_, eng := newTestHandlerGateway(t, newFakeRunnerStream())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"extra_body": map[string]any{
			"ask_user_question_mode": "tool_call",
		},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleChatCompletions_BodyTooLarge(t *testing.T) {
	t.Parallel()
	_, eng := newTestHandlerGateway(t, newFakeRunnerStream())
	// Override MaxRequestBodyBytes after construction to make oversize easy.
	// Simpler: build a body bigger than the configured 1 MiB by repeating
	// a string. Using 2 MiB.
	big := strings.Repeat("a", 2*1024*1024)
	body, _ := json.Marshal(map[string]any{
		"model":    "saker-default",
		"messages": []map[string]any{{"role": "user", "content": big}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "exceeds") {
		t.Errorf("error body should mention 'exceeds', got: %s", rec.Body.String())
	}
}

func TestHandleChatCompletions_StreamSuccess(t *testing.T) {
	t.Parallel()
	role := "assistant"
	idx := 0
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventMessageStart, Message: &api.Message{Role: role}},
		api.StreamEvent{Type: api.EventContentBlockDelta, Index: &idx, Delta: &api.Delta{Type: "text_delta", Text: "hello "}},
		api.StreamEvent{Type: api.EventContentBlockDelta, Index: &idx, Delta: &api.Delta{Type: "text_delta", Text: "world"}},
		api.StreamEvent{Type: api.EventMessageDelta, Delta: &api.Delta{StopReason: "end_turn"}, Usage: &api.Usage{InputTokens: 5, OutputTokens: 7}},
		api.StreamEvent{Type: api.EventMessageStop},
	)
	_, eng := newTestHandlerGateway(t, runner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Saker-Run-Id"); got == "" || !strings.HasPrefix(got, "run_") {
		t.Errorf("X-Saker-Run-Id missing/malformed, got %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data: ") {
		t.Errorf("expected SSE data frames, got: %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("expected [DONE] sentinel, got: %s", body)
	}
	// hello world should appear in delta content (split across chunks)
	if !strings.Contains(body, "hello") || !strings.Contains(body, "world") {
		t.Errorf("expected text deltas in body, got: %s", body)
	}
	// Without include_usage, no trailing usage frame should appear.
	if strings.Contains(body, `"usage"`) {
		t.Errorf("usage chunk leaked without stream_options.include_usage: %s", body)
	}
}

func TestHandleChatCompletions_StreamWithIncludeUsage(t *testing.T) {
	t.Parallel()
	idx := 0
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventMessageStart, Message: &api.Message{Role: "assistant"}},
		api.StreamEvent{Type: api.EventContentBlockDelta, Index: &idx, Delta: &api.Delta{Type: "text_delta", Text: "ok"}},
		api.StreamEvent{Type: api.EventMessageDelta, Delta: &api.Delta{StopReason: "end_turn"}, Usage: &api.Usage{InputTokens: 3, OutputTokens: 4}},
		api.StreamEvent{Type: api.EventMessageStop},
	)
	_, eng := newTestHandlerGateway(t, runner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"usage"`) {
		t.Errorf("expected usage chunk with include_usage=true, got: %s", body)
	}
	if !strings.Contains(body, `"prompt_tokens":3`) || !strings.Contains(body, `"completion_tokens":4`) {
		t.Errorf("expected mapped usage values, got: %s", body)
	}
}

func TestHandleChatCompletions_SyncSuccess(t *testing.T) {
	t.Parallel()
	idx := 0
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventMessageStart, Message: &api.Message{Role: "assistant"}},
		api.StreamEvent{Type: api.EventContentBlockDelta, Index: &idx, Delta: &api.Delta{Type: "text_delta", Text: "syncreply"}},
		api.StreamEvent{Type: api.EventMessageDelta, Delta: &api.Delta{StopReason: "end_turn"}, Usage: &api.Usage{InputTokens: 2, OutputTokens: 6}},
		api.StreamEvent{Type: api.EventMessageStop},
	)
	_, eng := newTestHandlerGateway(t, runner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Saker-Run-Id"); !strings.HasPrefix(got, "run_") {
		t.Errorf("X-Saker-Run-Id malformed, got %q", got)
	}

	var resp ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nbody=%s", err, rec.Body.String())
	}
	if resp.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", resp.Object)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message == nil || resp.Choices[0].Message.Content != "syncreply" {
		t.Errorf("expected content 'syncreply', got %+v", resp.Choices[0].Message)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.Choices[0].FinishReason)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 2 || resp.Usage.CompletionTokens != 6 {
		t.Errorf("usage mapping wrong: %+v", resp.Usage)
	}
}

func TestHandleChatCompletions_SyncRunnerError(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		events: nil,
		failOn: func(api.Request) error { return io.ErrClosedPipe },
	}
	_, eng := newTestHandlerGateway(t, runner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (runtime errors surface as InvalidRequest); body=%s", rec.Code, rec.Body.String())
	}
}

// Ensures session_id from extra_body propagates to the runner request.
func TestHandleChatCompletions_SessionIDPropagates(t *testing.T) {
	t.Parallel()
	idx := 0
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventContentBlockDelta, Index: &idx, Delta: &api.Delta{Type: "text_delta", Text: "x"}},
	)
	_, eng := newTestHandlerGateway(t, runner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"extra_body": map[string]any{
			"session_id": "sess-123",
		},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if runner.gotReq.SessionID != "sess-123" {
		t.Errorf("session_id not propagated, got %q", runner.gotReq.SessionID)
	}
}

// Sanity check on the time clamp: ExpiresAfter is bounded by the turn
// timeout. We can only assert the handler doesn't panic on an absurd
// extra_body.expires_after_seconds value.
func TestHandleChatCompletions_AbsurdExpires(t *testing.T) {
	t.Parallel()
	idx := 0
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventContentBlockDelta, Index: &idx, Delta: &api.Delta{Type: "text_delta", Text: "x"}},
	)
	_, eng := newTestHandlerGateway(t, runner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"extra_body": map[string]any{
			"expires_after_seconds": 86400,
		},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// Verifies the streaming consumer ends gracefully when the runner emits an
// error event (the producer marks finalStatus=Failed but still emits a
// final synthetic chunk).
func TestHandleChatCompletions_StreamPropagatesErrorEvent(t *testing.T) {
	t.Parallel()
	idx := 0
	isErr := true
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventContentBlockDelta, Index: &idx, Delta: &api.Delta{Type: "text_delta", Text: "partial"}},
		api.StreamEvent{Type: api.EventError, Output: "boom", IsError: &isErr},
	)
	_, eng := newTestHandlerGateway(t, runner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (stream error is in-band)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "[saker error]") {
		t.Errorf("expected [saker error] payload, got: %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("expected [DONE] even after error, got: %s", body)
	}
}

// Drives streamChatSync's "no usage / no finish reason" branch using a
// runner that emits only a single text delta then closes.
func TestHandleChatCompletions_SyncSyntheticStop(t *testing.T) {
	t.Parallel()
	idx := 0
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventContentBlockDelta, Index: &idx, Delta: &api.Delta{Type: "text_delta", Text: "abc"}},
	)
	_, eng := newTestHandlerGateway(t, runner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// finish_reason should be the synthesized "stop" since runner emitted
	// neither MessageDelta nor explicit finish_reason.
	if len(resp.Choices) != 1 || resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.Choices[0].FinishReason)
	}
}

// Confirms the Runner's RunStream sees the user prompt actually folded
// from messages[].
func TestHandleChatCompletions_RunnerSeesUserPrompt(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{events: makeClosedChannel()}
	_, eng := newTestHandlerGateway(t, runner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody(t, map[string]any{
		"model":    "saker-default",
		"messages": []map[string]any{{"role": "user", "content": "what time is it?"}},
	}))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(runner.gotReq.Prompt, "what time is it?") {
		t.Errorf("user prompt not folded into Request.Prompt, got %q", runner.gotReq.Prompt)
	}
}

// TestHandleChatCompletions_ModelOverridesPropagate verifies the OpenAI
// standard sampler fields land on api.Request.ModelOverrides exactly as
// declared. Failure here means a future refactor severed the gateway →
// runtime plumbing for temperature/top_p/etc.
func TestHandleChatCompletions_ModelOverridesPropagate(t *testing.T) {
	t.Parallel()
	runner := newFakeRunnerStream()
	_, eng := newTestHandlerGateway(t, runner)

	body := chatBody(t, map[string]any{
		"model": "saker-mid",
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
		"temperature":         0.42,
		"top_p":               0.7,
		"max_tokens":          321,
		"stop":                []any{"END", "DONE"},
		"seed":                123,
		"tool_choice":         "none",
		"parallel_tool_calls": false,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	o := runner.gotReq.ModelOverrides
	if o == nil {
		t.Fatal("ModelOverrides should be non-nil after sampler-bearing request")
	}
	if o.Temperature == nil || *o.Temperature != 0.42 {
		t.Errorf("Temperature = %v, want 0.42", o.Temperature)
	}
	if o.TopP == nil || *o.TopP != 0.7 {
		t.Errorf("TopP = %v, want 0.7", o.TopP)
	}
	if o.MaxTokens == nil || *o.MaxTokens != 321 {
		t.Errorf("MaxTokens = %v, want 321", o.MaxTokens)
	}
	if !(len(o.Stop) == 2 && o.Stop[0] == "END" && o.Stop[1] == "DONE") {
		t.Errorf("Stop = %v, want [END DONE]", o.Stop)
	}
	if o.Seed == nil || *o.Seed != 123 {
		t.Errorf("Seed = %v, want 123", o.Seed)
	}
	if o.ToolChoice != "none" {
		t.Errorf("ToolChoice = %q, want none", o.ToolChoice)
	}
	if o.ParallelToolCalls == nil || *o.ParallelToolCalls {
		t.Errorf("ParallelToolCalls = %v, want *false", o.ParallelToolCalls)
	}
}

// TestHandleChatCompletions_NoOverridesLeavesNil verifies a request that
// carries none of the sampler fields leaves Request.ModelOverrides nil so
// the runtime falls back to its configured defaults.
func TestHandleChatCompletions_NoOverridesLeavesNil(t *testing.T) {
	t.Parallel()
	runner := newFakeRunnerStream()
	_, eng := newTestHandlerGateway(t, runner)

	body := chatBody(t, map[string]any{
		"model":    "saker-mid",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if runner.gotReq.ModelOverrides != nil {
		t.Errorf("ModelOverrides should be nil for sampler-free request, got %+v", runner.gotReq.ModelOverrides)
	}
}

func makeClosedChannel() chan api.StreamEvent {
	ch := make(chan api.StreamEvent)
	close(ch)
	return ch
}

// silence ctx import (used by Runner interface in production handler).
var _ = context.Background
