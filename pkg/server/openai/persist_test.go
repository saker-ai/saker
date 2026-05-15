package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/conversation"
	"github.com/saker-ai/saker/pkg/runhub"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// openTestConvStore opens a fresh conversation.Store rooted in t.TempDir
// so each test gets full isolation. Cleanup is registered automatically.
func openTestConvStore(t *testing.T) *conversation.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := conversation.Open(conversation.Config{FallbackPath: filepath.Join(dir, "conv.db")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// newPersistTestGateway is the persistence-aware twin of
// newTestHandlerGateway. Returns the gateway, engine, and the
// conversation.Store the gateway was wired against (so tests can
// directly assert on the stored events).
func newPersistTestGateway(t *testing.T, runner Runner) (*Gateway, *gin.Engine, *conversation.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	convStore := openTestConvStore(t)
	deps := Deps{
		Runtime:           runner,
		ConversationStore: convStore,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options: Options{
			Enabled:             true,
			MaxRuns:             10,
			MaxRunsPerTenant:    5,
			RingSize:             64,
			ExpiresAfterSeconds:  60,
			MaxRequestBodyBytes:  1024 * 1024,
			ErrorDetailMode:      "dev",
			DevBypassAuth:        true,
		},
	}
	require.NoError(t, deps.Options.Validate())

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
	return gw, eng, convStore
}

// waitForKind polls the events table until it contains an event with
// the requested kind, or the deadline trips. The producer goroutine
// finalizes assistant_text writes after the sync HTTP response returns,
// so direct read-after-response can race.
func waitForKind(t *testing.T, ctx context.Context, store *conversation.Store, threadID string, kind conversation.EventKind) conversation.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, err := store.GetEvents(ctx, threadID, conversation.GetEventsOpts{})
		require.NoError(t, err)
		for _, e := range events {
			if conversation.EventKind(e.Kind) == kind {
				return e
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("event of kind %q never landed for thread %q within deadline", kind, threadID)
	return conversation.Event{}
}

func TestPersist_NonStreaming_RoundTrip(t *testing.T) {
	t.Parallel()
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Type: "text_delta", Text: "Hello "}},
		api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Type: "text_delta", Text: "world"}},
	)
	_, eng, store := newPersistTestGateway(t, runner)

	body, _ := json.Marshal(map[string]any{
		"model": "gpt-4",
		"messages": []map[string]any{
			{"role": "system", "content": "be terse"},
			{"role": "user", "content": "say hi"},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	threadID := rec.Header().Get("X-Saker-Thread-Id")
	require.NotEmpty(t, threadID, "X-Saker-Thread-Id header must be set")

	ctx := context.Background()
	// Block until the second assistant_text delta has been recorded.
	// Inputs are recorded synchronously before the response so they're
	// already on disk by the time we look.
	waitForKind(t, ctx, store, threadID, conversation.EventKindAssistantText)

	events, err := store.GetEvents(ctx, threadID, conversation.GetEventsOpts{})
	require.NoError(t, err)

	var (
		sawSystem, sawUser bool
		assistantText      string
		assistantCount     int
	)
	for _, e := range events {
		switch conversation.EventKind(e.Kind) {
		case conversation.EventKindSystem:
			sawSystem = true
		case conversation.EventKindUserMessage:
			sawUser = true
		case conversation.EventKindAssistantText:
			assistantText += e.ContentText
			assistantCount++
		}
	}
	require.True(t, sawSystem, "system input should be recorded")
	require.True(t, sawUser, "user input should be recorded")

	// Allow a brief catch-up so the second assistant chunk lands too.
	deadline := time.Now().Add(1 * time.Second)
	for assistantCount < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		events, err = store.GetEvents(ctx, threadID, conversation.GetEventsOpts{})
		require.NoError(t, err)
		assistantText, assistantCount = "", 0
		for _, e := range events {
			if conversation.EventKind(e.Kind) == conversation.EventKindAssistantText {
				assistantText += e.ContentText
				assistantCount++
			}
		}
	}
	require.Equal(t, "Hello world", assistantText, "both deltas must concatenate to the full reply")
}

func TestPersist_ReuseSessionID(t *testing.T) {
	t.Parallel()
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Type: "text_delta", Text: "ack"}},
	)
	_, eng, store := newPersistTestGateway(t, runner)

	const sessionID = "00000000-0000-4000-8000-000000000abc"
	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4",
		"messages": []map[string]any{{"role": "user", "content": "first"}},
		"extra_body": map[string]any{
			"session_id": sessionID,
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, sessionID, rec.Header().Get("X-Saker-Thread-Id"),
		"SDK-supplied session_id must be used verbatim as the thread id")

	ctx := context.Background()
	th, err := store.GetThread(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, sessionID, th.ID)
	require.Equal(t, "openai", th.Client)
}

func TestPersist_ErrorEventLandsInLog(t *testing.T) {
	t.Parallel()
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Type: "text_delta", Text: "partial "}},
		api.StreamEvent{Type: api.EventError, Output: "upstream blew up"},
	)
	_, eng, store := newPersistTestGateway(t, runner)

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4",
		"messages": []map[string]any{{"role": "user", "content": "go"}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "sync handler still flushes a chat.completion envelope")
	threadID := rec.Header().Get("X-Saker-Thread-Id")
	require.NotEmpty(t, threadID)

	ctx := context.Background()
	errEvt := waitForKind(t, ctx, store, threadID, conversation.EventKindError)
	require.Contains(t, errEvt.ContentText, "upstream blew up")
}

func TestPersist_ToolExecutionOutputNotPersisted(t *testing.T) {
	t.Parallel()
	isStderr := true
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventToolExecutionStart, ToolUseID: "tool-1", Name: "bash", Input: `{"cmd":"ls"}`},
		api.StreamEvent{Type: api.EventToolExecutionOutput, ToolUseID: "tool-1", Output: "file.txt", IsStderr: &isStderr},
	)
	_, eng, store := newPersistTestGateway(t, runner)

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4",
		"messages": []map[string]any{{"role": "user", "content": "list files"}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	threadID := rec.Header().Get("X-Saker-Thread-Id")
	require.NotEmpty(t, threadID)

	// Intermediate output events are no longer persisted; only the final
	// EventToolExecutionResult is recorded. Verify no tool_result event exists.
	time.Sleep(200 * time.Millisecond)
	ctx := context.Background()
	events, err := store.GetEvents(ctx, threadID, conversation.GetEventsOpts{})
	require.NoError(t, err)
	for _, e := range events {
		if conversation.EventKind(e.Kind) == conversation.EventKindToolResult {
			t.Fatalf("unexpected tool_result event persisted for intermediate output")
		}
	}
}

func TestPersist_ToolExecutionResultLandsInLog(t *testing.T) {
	t.Parallel()
	isErr := false
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventToolExecutionStart, ToolUseID: "tool-2", Name: "search", Input: `{"q":"test"}`},
		api.StreamEvent{Type: api.EventToolExecutionResult, ToolUseID: "tool-2", Name: "search", Output: `{"hits":3}`, IsError: &isErr},
	)
	_, eng, store := newPersistTestGateway(t, runner)

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4",
		"messages": []map[string]any{{"role": "user", "content": "search"}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	threadID := rec.Header().Get("X-Saker-Thread-Id")
	require.NotEmpty(t, threadID)

	ctx := context.Background()
	evt := waitForKind(t, ctx, store, threadID, conversation.EventKindToolResult)
	require.Equal(t, "tool", evt.Role)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(evt.ContentJSON, &payload))
	require.Equal(t, "tool-2", payload["tool_use_id"])
	require.Equal(t, "search", payload["name"])
}

func TestPersist_NilStore_Bypass(t *testing.T) {
	t.Parallel()
	// Validates back-compat: when ConversationStore is nil the gateway
	// must still serve normally and emit no X-Saker-Thread-Id header.
	runner := newFakeRunnerStream(
		api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Type: "text_delta", Text: "ok"}},
	)
	_, eng := newTestHandlerGateway(t, runner)

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Empty(t, rec.Header().Get("X-Saker-Thread-Id"),
		"no thread header when store is unconfigured")
}

func TestPersist_CrossTenantSessionRejected(t *testing.T) {
	t.Parallel()
	// Pre-seed a thread under a different project; the dev-bypass
	// identity supplies project="default", so the SDK must NOT be able
	// to point at a different project's session_id and silently
	// piggyback on it.
	runner := newFakeRunnerStream()
	_, eng, store := newPersistTestGateway(t, runner)

	const sessionID = "00000000-0000-4000-8000-deadbeefcafe"
	ctx := context.Background()
	_, err := store.CreateThreadWithID(ctx, sessionID, "other-project", "other-user", "x", "openai")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4",
		"messages": []map[string]any{{"role": "user", "content": "probe"}},
		"extra_body": map[string]any{
			"session_id": sessionID,
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	eng.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code,
		"cross-tenant session_id must be rejected, body=%s", rec.Body.String())
	require.Zero(t, runner.startCt, "runtime must not be invoked on a rejected request")
}
