package agui_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/conversation"
	"github.com/saker-ai/saker/pkg/server/agui"
)

func init() { gin.SetMode(gin.ReleaseMode) }

// setupTestGateway creates a real HTTP test server with an in-memory
// conversation store — no mocks, no stubs. Every request exercises the
// full Gin middleware stack + AG-UI gateway.
func setupTestGateway(t *testing.T) (*httptest.Server, *conversation.Store) {
	t.Helper()

	cs, err := conversation.Open(conversation.Config{DSN: ":memory:"})
	if err != nil {
		t.Fatalf("open conversation store: %v", err)
	}
	t.Cleanup(func() { cs.Close() })

	engine := gin.New()
	_, err = agui.RegisterAGUIGateway(engine, agui.Deps{
		Runtime:           &stubRunner{},
		ConversationStore: cs,
		Logger:            slog.Default(),
		Options:           agui.Options{Enabled: true, DevBypassAuth: true},
	})
	if err != nil {
		t.Fatalf("register gateway: %v", err)
	}

	ts := httptest.NewServer(engine)
	t.Cleanup(ts.Close)
	return ts, cs
}

// stubRunner satisfies the Runner interface. We only need it for
// handleRun — the connect/threads/info tests never call RunStream.
type stubRunner struct{}

func (s *stubRunner) RunStream(_ context.Context, _ api.Request) (<-chan api.StreamEvent, error) {
	return nil, fmt.Errorf("stub: not implemented")
}

// --- helpers ---

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func doRequest(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return out
}

// parseSSEEvents reads an SSE response and returns the parsed event types
// and raw data payloads.
func parseSSEEvents(t *testing.T, resp *http.Response) []sseEvent {
	t.Helper()
	defer resp.Body.Close()
	var events []sseEvent
	scanner := bufio.NewScanner(resp.Body)
	var currentEvent, currentData string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			currentData = strings.TrimPrefix(line, "data: ")
		} else if line == "" && currentEvent != "" {
			events = append(events, sseEvent{Type: currentEvent, Data: currentData})
			currentEvent = ""
			currentData = ""
		}
	}
	if currentEvent != "" {
		events = append(events, sseEvent{Type: currentEvent, Data: currentData})
	}
	return events
}

type sseEvent struct {
	Type string
	Data string
}

// --- Tests ---

// 1. GET /info — agent discovery
func TestCopilotKitAPI_Info(t *testing.T) {
	ts, _ := setupTestGateway(t)

	resp, err := http.Get(ts.URL + "/v1/agents/run/info")
	if err != nil {
		t.Fatalf("GET /info: %v", err)
	}
	data := readJSON(t, resp)

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	agents, ok := data["agents"].(map[string]any)
	if !ok {
		t.Fatalf("missing 'agents' in response: %v", data)
	}
	if _, ok := agents["default"]; !ok {
		t.Error("missing 'default' agent")
	}
	t.Logf("PASS: GET /info → agents=%v", agents)
}

// 2. POST envelope method=info
func TestCopilotKitAPI_EnvelopeInfo(t *testing.T) {
	ts, _ := setupTestGateway(t)

	resp := postJSON(t, ts.URL+"/v1/agents/run", map[string]any{
		"method": "info",
	})
	data := readJSON(t, resp)

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if _, ok := data["agents"]; !ok {
		t.Error("envelope info should return agents")
	}
	t.Logf("PASS: envelope method=info → %v", data)
}

// 3. GET /threads — initially empty
func TestCopilotKitAPI_ThreadsEmpty(t *testing.T) {
	ts, _ := setupTestGateway(t)

	resp, err := http.Get(ts.URL + "/v1/agents/run/threads")
	if err != nil {
		t.Fatalf("GET /threads: %v", err)
	}
	data := readJSON(t, resp)

	threads, ok := data["threads"].([]any)
	if !ok {
		t.Fatalf("missing 'threads' array: %v", data)
	}
	if len(threads) != 0 {
		t.Errorf("expected 0 threads, got %d", len(threads))
	}
	t.Logf("PASS: GET /threads (empty) → %d threads", len(threads))
}

// 4. POST envelope method=threads — same via envelope
func TestCopilotKitAPI_EnvelopeThreads(t *testing.T) {
	ts, _ := setupTestGateway(t)

	resp := postJSON(t, ts.URL+"/v1/agents/run", map[string]any{
		"method": "threads",
	})
	data := readJSON(t, resp)

	if _, ok := data["threads"]; !ok {
		t.Error("envelope threads should return threads array")
	}
	t.Logf("PASS: envelope method=threads → %v", data)
}

// 5. GET /threads — after creating threads, list returns them
func TestCopilotKitAPI_ThreadsList(t *testing.T) {
	ts, cs := setupTestGateway(t)

	// Seed threads directly via conversation store.
	_, err := cs.CreateThreadWithID(context.Background(),
		"thread_aaa", "default", "localhost", "Test Chat 1", "agui")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	_, err = cs.CreateThreadWithID(context.Background(),
		"thread_bbb", "default", "localhost", "Test Chat 2", "agui")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	// Non-agui client thread should not appear.
	_, err = cs.CreateThreadWithID(context.Background(),
		"thread_ccc", "default", "localhost", "WS Thread", "web")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	resp, _ := http.Get(ts.URL + "/v1/agents/run/threads")
	data := readJSON(t, resp)
	threads := data["threads"].([]any)

	if len(threads) != 2 {
		t.Fatalf("expected 2 agui threads, got %d: %v", len(threads), threads)
	}
	t.Logf("PASS: GET /threads (filtered) → %d agui threads", len(threads))
}

// 6. agent/connect — empty thread returns empty snapshot
func TestCopilotKitAPI_ConnectEmptyThread(t *testing.T) {
	ts, cs := setupTestGateway(t)

	_, _ = cs.CreateThreadWithID(context.Background(),
		"thread_empty", "default", "localhost", "Empty", "agui")

	resp := postJSON(t, ts.URL+"/v1/agents/run", map[string]any{
		"method": "agent/connect",
		"body": map[string]any{
			"threadId": "thread_empty",
			"runId":    "run_001",
		},
	})

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	events := parseSSEEvents(t, resp)
	if len(events) < 3 {
		t.Fatalf("expected >= 3 SSE events, got %d: %v", len(events), events)
	}

	if events[0].Type != "RUN_STARTED" {
		t.Errorf("event[0] = %q, want RUN_STARTED", events[0].Type)
	}
	if events[1].Type != "MESSAGES_SNAPSHOT" {
		t.Errorf("event[1] = %q, want MESSAGES_SNAPSHOT", events[1].Type)
	}
	if events[2].Type != "RUN_FINISHED" {
		t.Errorf("event[2] = %q, want RUN_FINISHED", events[2].Type)
	}

	// Parse MESSAGES_SNAPSHOT — should have empty messages array.
	var snapshot struct {
		Messages []any `json:"messages"`
	}
	if err := json.Unmarshal([]byte(events[1].Data), &snapshot); err != nil {
		t.Fatalf("parse snapshot: %v", err)
	}
	if len(snapshot.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(snapshot.Messages))
	}
	t.Logf("PASS: agent/connect (empty) → %d events, %d messages", len(events), len(snapshot.Messages))
}

// 7. agent/connect — thread with history returns messages
func TestCopilotKitAPI_ConnectWithHistory(t *testing.T) {
	ts, cs := setupTestGateway(t)

	threadID := "thread_hist"
	_, _ = cs.CreateThreadWithID(context.Background(),
		threadID, "default", "localhost", "History Test", "agui")

	turnID, _ := cs.OpenTurn(context.Background(), threadID, "")

	// Seed user message.
	cs.AppendEvent(context.Background(), conversation.AppendEventInput{
		ThreadID: threadID, ProjectID: "default", TurnID: turnID,
		Kind: conversation.EventKindUserMessage, ContentText: "Hello, Saker!",
	})
	// Seed assistant response.
	cs.AppendEvent(context.Background(), conversation.AppendEventInput{
		ThreadID: threadID, ProjectID: "default", TurnID: turnID,
		Kind: conversation.EventKindAssistantText, ContentText: "Hi! How can I help?",
	})

	resp := postJSON(t, ts.URL+"/v1/agents/run", map[string]any{
		"method": "agent/connect",
		"body": map[string]any{
			"threadId": threadID,
			"runId":    "run_hist",
		},
	})

	events := parseSSEEvents(t, resp)
	if len(events) < 3 {
		t.Fatalf("expected >= 3 events, got %d", len(events))
	}

	var snapshot struct {
		Messages []struct {
			ID      string `json:"id"`
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(events[1].Data), &snapshot); err != nil {
		t.Fatalf("parse snapshot: %v", err)
	}

	if len(snapshot.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d: %v", len(snapshot.Messages), snapshot.Messages)
	}
	if snapshot.Messages[0].Role != "user" {
		t.Errorf("msg[0].role = %q, want user", snapshot.Messages[0].Role)
	}
	if snapshot.Messages[1].Role != "assistant" {
		t.Errorf("msg[1].role = %q, want assistant", snapshot.Messages[1].Role)
	}
	t.Logf("PASS: agent/connect (history) → %d messages: user=%v, assistant=%v",
		len(snapshot.Messages), snapshot.Messages[0].Content, snapshot.Messages[1].Content)
}

// 8. agent/connect — thread with tool calls
func TestCopilotKitAPI_ConnectWithToolCalls(t *testing.T) {
	ts, cs := setupTestGateway(t)

	threadID := "thread_tools"
	_, _ = cs.CreateThreadWithID(context.Background(),
		threadID, "default", "localhost", "Tool Test", "agui")

	turnID, _ := cs.OpenTurn(context.Background(), threadID, "")

	cs.AppendEvent(context.Background(), conversation.AppendEventInput{
		ThreadID: threadID, ProjectID: "default", TurnID: turnID,
		Kind: conversation.EventKindUserMessage, ContentText: "Search for saker",
	})
	cs.AppendEvent(context.Background(), conversation.AppendEventInput{
		ThreadID: threadID, ProjectID: "default", TurnID: turnID,
		Kind:         conversation.EventKindAssistantToolCall,
		ToolCallID:   "call_001",
		ToolCallName: "web_search",
		ContentJSON:  json.RawMessage(`{"query":"saker falcon"}`),
	})
	cs.AppendEvent(context.Background(), conversation.AppendEventInput{
		ThreadID: threadID, ProjectID: "default", TurnID: turnID,
		Kind: conversation.EventKindToolResult, ToolCallID: "call_001",
		ContentText: "Saker falcon is a bird of prey.",
	})
	cs.AppendEvent(context.Background(), conversation.AppendEventInput{
		ThreadID: threadID, ProjectID: "default", TurnID: turnID,
		Kind: conversation.EventKindAssistantText, ContentText: "The saker falcon is a large bird of prey.",
	})

	resp := postJSON(t, ts.URL+"/v1/agents/run", map[string]any{
		"method": "agent/connect",
		"body":   map[string]any{"threadId": threadID, "runId": "run_tc"},
	})

	events := parseSSEEvents(t, resp)
	var snapshot struct {
		Messages []struct {
			ID         string `json:"id"`
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCalls  []any  `json:"toolCalls"`
			ToolCallID string `json:"toolCallId"`
		} `json:"messages"`
	}
	json.Unmarshal([]byte(events[1].Data), &snapshot)

	if len(snapshot.Messages) != 3 {
		t.Fatalf("expected 3 messages (user, assistant, tool), got %d", len(snapshot.Messages))
	}

	assistantMsg := snapshot.Messages[1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("msg[1].role = %q, want assistant", assistantMsg.Role)
	}
	if len(assistantMsg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistantMsg.ToolCalls))
	}

	toolMsg := snapshot.Messages[2]
	if toolMsg.Role != "tool" {
		t.Errorf("msg[2].role = %q, want tool", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call_001" {
		t.Errorf("msg[2].toolCallId = %q, want call_001", toolMsg.ToolCallID)
	}

	t.Logf("PASS: agent/connect (tools) → %d messages, assistant has %d tool_calls",
		len(snapshot.Messages), len(assistantMsg.ToolCalls))
}

// 9. agent/connect — missing threadId returns 400
func TestCopilotKitAPI_ConnectNoThreadId(t *testing.T) {
	ts, _ := setupTestGateway(t)

	resp := postJSON(t, ts.URL+"/v1/agents/run", map[string]any{
		"method": "agent/connect",
		"body":   map[string]any{"runId": "run_no_thread"},
	})
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	t.Logf("PASS: agent/connect (no threadId) → %d", resp.StatusCode)
}

// 10. PATCH /threads/:threadId — rename thread
func TestCopilotKitAPI_ThreadUpdate(t *testing.T) {
	ts, cs := setupTestGateway(t)

	_, _ = cs.CreateThreadWithID(context.Background(),
		"thread_rename", "default", "localhost", "Old Title", "agui")

	resp := doRequest(t, "PATCH", ts.URL+"/v1/agents/run/threads/thread_rename",
		map[string]any{"title": "New Title"})
	data := readJSON(t, resp)

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if data["title"] != "New Title" {
		t.Errorf("title = %v, want 'New Title'", data["title"])
	}

	// Verify via store.
	th, _ := cs.GetThread(context.Background(), "thread_rename")
	if th.Title != "New Title" {
		t.Errorf("stored title = %q, want 'New Title'", th.Title)
	}
	t.Logf("PASS: PATCH /threads/:id → title=%v", data["title"])
}

// 11. PATCH /threads/:threadId — not found
func TestCopilotKitAPI_ThreadUpdateNotFound(t *testing.T) {
	ts, _ := setupTestGateway(t)

	resp := doRequest(t, "PATCH", ts.URL+"/v1/agents/run/threads/nonexistent",
		map[string]any{"title": "X"})
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	t.Logf("PASS: PATCH /threads/nonexistent → %d", resp.StatusCode)
}

// 12. DELETE /threads/:threadId — soft delete
func TestCopilotKitAPI_ThreadDelete(t *testing.T) {
	ts, cs := setupTestGateway(t)

	_, _ = cs.CreateThreadWithID(context.Background(),
		"thread_del", "default", "localhost", "To Delete", "agui")

	resp := doRequest(t, "DELETE", ts.URL+"/v1/agents/run/threads/thread_del", nil)
	data := readJSON(t, resp)

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if data["status"] != "deleted" {
		t.Errorf("status = %v, want 'deleted'", data["status"])
	}

	// Verify thread is gone from list.
	_, err := cs.GetThread(context.Background(), "thread_del")
	if err == nil {
		t.Error("expected ErrThreadNotFound after delete")
	}
	t.Logf("PASS: DELETE /threads/:id → %v", data)
}

// 13. DELETE /threads/:threadId — not found
func TestCopilotKitAPI_ThreadDeleteNotFound(t *testing.T) {
	ts, _ := setupTestGateway(t)

	resp := doRequest(t, "DELETE", ts.URL+"/v1/agents/run/threads/nonexistent", nil)
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	t.Logf("PASS: DELETE /threads/nonexistent → %d", resp.StatusCode)
}

// 14. Full lifecycle: create → send → connect → rename → delete → list
func TestCopilotKitAPI_FullLifecycle(t *testing.T) {
	ts, cs := setupTestGateway(t)

	// Step 1: Seed a thread with messages (simulating what handleRun does).
	threadID := "thread_lifecycle"
	_, _ = cs.CreateThreadWithID(context.Background(),
		threadID, "default", "localhost", "Lifecycle Test", "agui")

	turnID, _ := cs.OpenTurn(context.Background(), threadID, "")
	cs.AppendEvent(context.Background(), conversation.AppendEventInput{
		ThreadID: threadID, ProjectID: "default", TurnID: turnID,
		Kind: conversation.EventKindUserMessage, ContentText: "What is Saker?",
	})
	cs.AppendEvent(context.Background(), conversation.AppendEventInput{
		ThreadID: threadID, ProjectID: "default", TurnID: turnID,
		Kind: conversation.EventKindAssistantText, ContentText: "Saker is an AI platform.",
	})
	cs.CloseTurn(context.Background(), turnID, "completed")

	// Step 2: List threads — should see our thread.
	resp, _ := http.Get(ts.URL + "/v1/agents/run/threads")
	data := readJSON(t, resp)
	threads := data["threads"].([]any)
	if len(threads) != 1 {
		t.Fatalf("step 2: expected 1 thread, got %d", len(threads))
	}
	t.Log("  Step 2 PASS: 1 thread listed")

	// Step 3: Connect — should get messages snapshot.
	resp = postJSON(t, ts.URL+"/v1/agents/run", map[string]any{
		"method": "agent/connect",
		"body":   map[string]any{"threadId": threadID, "runId": "run_lc"},
	})
	events := parseSSEEvents(t, resp)
	if events[1].Type != "MESSAGES_SNAPSHOT" {
		t.Fatalf("step 3: expected MESSAGES_SNAPSHOT, got %q", events[1].Type)
	}
	var snapshot struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	json.Unmarshal([]byte(events[1].Data), &snapshot)
	if len(snapshot.Messages) != 2 {
		t.Fatalf("step 3: expected 2 messages, got %d", len(snapshot.Messages))
	}
	t.Logf("  Step 3 PASS: connect → %d messages", len(snapshot.Messages))

	// Step 4: Rename thread.
	resp = doRequest(t, "PATCH", ts.URL+"/v1/agents/run/threads/"+threadID,
		map[string]any{"title": "Renamed Chat"})
	rData := readJSON(t, resp)
	if rData["title"] != "Renamed Chat" {
		t.Fatalf("step 4: title = %v", rData["title"])
	}
	t.Log("  Step 4 PASS: renamed to 'Renamed Chat'")

	// Step 5: Delete thread.
	resp = doRequest(t, "DELETE", ts.URL+"/v1/agents/run/threads/"+threadID, nil)
	dData := readJSON(t, resp)
	if dData["status"] != "deleted" {
		t.Fatalf("step 5: status = %v", dData["status"])
	}
	t.Log("  Step 5 PASS: thread deleted")

	// Step 6: List threads — should be empty now.
	resp, _ = http.Get(ts.URL + "/v1/agents/run/threads")
	data = readJSON(t, resp)
	threads = data["threads"].([]any)
	if len(threads) != 0 {
		t.Fatalf("step 6: expected 0 threads, got %d", len(threads))
	}
	t.Log("  Step 6 PASS: thread list empty after delete")

	t.Log("PASS: full lifecycle completed")
}

// 15. POST /agent/:agentId/stop/:threadId — should return 200
func TestCopilotKitAPI_Stop(t *testing.T) {
	ts, _ := setupTestGateway(t)

	resp := postJSON(t, ts.URL+"/v1/agents/run/agent/default/stop/thread_x", nil)
	data := readJSON(t, resp)

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if data["status"] != "stopped" {
		t.Errorf("status = %v, want 'stopped'", data["status"])
	}
	t.Logf("PASS: POST /stop → %v", data)
}
