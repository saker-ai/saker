package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cinience/saker/pkg/conversation"
	"github.com/cinience/saker/pkg/message"
	"github.com/stretchr/testify/require"
)

// openConvStoreForTest opens a fresh SQLite-backed conversation.Store
// inside the test's TempDir. Cleanup is registered with t.Cleanup.
func openConvStoreForTest(t *testing.T) *conversation.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := conversation.Open(conversation.Config{FallbackPath: filepath.Join(dir, "conv.db")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// newRuntimeWithConvStore returns a minimal *Runtime literal wired up
// just enough to exercise persistToConversation. We bypass api.New so
// the test stays focused on the persistence diff/cursor logic instead
// of dragging in model factories, sandboxes, etc.
func newRuntimeWithConvStore(t *testing.T) *Runtime {
	t.Helper()
	return &Runtime{
		conversationStore: openConvStoreForTest(t),
		convCursor:        map[string]int{},
	}
}

func TestPersistToConversation_NilSafe(t *testing.T) {
	// nil receiver
	var nilRT *Runtime
	require.NotPanics(t, func() { nilRT.persistToConversation("s", message.NewHistory()) })

	// nil store
	rt := &Runtime{convCursor: map[string]int{}}
	require.NotPanics(t, func() { rt.persistToConversation("s", message.NewHistory()) })

	// nil history
	rt2 := newRuntimeWithConvStore(t)
	require.NotPanics(t, func() { rt2.persistToConversation("s", nil) })

	// blank session
	rt3 := newRuntimeWithConvStore(t)
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "hi"})
	rt3.persistToConversation("   ", hist)
	// thread must NOT have been created — blank id was rejected.
	_, err := rt3.conversationStore.GetThread(context.Background(), "")
	require.Error(t, err)

	// empty snapshot
	rt4 := newRuntimeWithConvStore(t)
	rt4.persistToConversation("session-empty", message.NewHistory())
	_, err = rt4.conversationStore.GetThread(context.Background(), "session-empty")
	require.Error(t, err, "no thread should be created when there are no messages")
}

func TestPersistToConversation_ThreadAndTitleSeed(t *testing.T) {
	rt := newRuntimeWithConvStore(t)
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "What is the meaning of life?"})

	rt.persistToConversation("sess-1", hist)

	// Thread row created with sessionID as the id and title from first user msg.
	thread, err := rt.conversationStore.GetThread(context.Background(), "sess-1")
	require.NoError(t, err)
	require.Equal(t, "sess-1", thread.ID)
	require.Equal(t, cliConversationProjectID, thread.ProjectID)
	require.Equal(t, cliConversationOwnerUserID, thread.OwnerUserID)
	require.Equal(t, cliConversationClient, thread.Client)
	require.Equal(t, "What is the meaning of life?", thread.Title)
}

func TestPersistToConversation_TitleFallback(t *testing.T) {
	rt := newRuntimeWithConvStore(t)
	hist := message.NewHistory()
	// No user message in the snapshot — only assistant.
	hist.Append(message.Message{Role: "assistant", Content: "Hello!"})
	rt.persistToConversation("sess-fallback", hist)

	thread, err := rt.conversationStore.GetThread(context.Background(), "sess-fallback")
	require.NoError(t, err)
	require.Equal(t, "CLI session", thread.Title)
}

func TestPersistToConversation_TitleTruncation(t *testing.T) {
	rt := newRuntimeWithConvStore(t)
	long := ""
	for i := 0; i < 200; i++ {
		long += "x"
	}
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: long})
	rt.persistToConversation("sess-long", hist)

	thread, err := rt.conversationStore.GetThread(context.Background(), "sess-long")
	require.NoError(t, err)
	require.Len(t, thread.Title, 80)
}

func TestPersistToConversation_CursorAdvances(t *testing.T) {
	rt := newRuntimeWithConvStore(t)
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "first"})
	hist.Append(message.Message{Role: "assistant", Content: "answer-1"})
	rt.persistToConversation("sess-cursor", hist)

	// One turn, two events so far.
	events, err := rt.conversationStore.GetEvents(context.Background(), "sess-cursor", conversation.GetEventsOpts{Limit: 100})
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, string(conversation.EventKindUserMessage), events[0].Kind)
	require.Equal(t, string(conversation.EventKindAssistantText), events[1].Kind)

	rt.convCursorMu.Lock()
	require.Equal(t, 2, rt.convCursor["sess-cursor"])
	rt.convCursorMu.Unlock()

	// Second persistHistory: only the new tail should land.
	hist.Append(message.Message{Role: "user", Content: "follow-up"})
	hist.Append(message.Message{Role: "assistant", Content: "answer-2"})
	rt.persistToConversation("sess-cursor", hist)

	events, err = rt.conversationStore.GetEvents(context.Background(), "sess-cursor", conversation.GetEventsOpts{Limit: 100})
	require.NoError(t, err)
	require.Len(t, events, 4)
	rt.convCursorMu.Lock()
	require.Equal(t, 4, rt.convCursor["sess-cursor"])
	rt.convCursorMu.Unlock()

	// The two calls should produce two distinct turns (different turn_ids).
	turnIDs := map[string]struct{}{}
	for _, e := range events {
		turnIDs[e.TurnID] = struct{}{}
	}
	require.Len(t, turnIDs, 2, "expected one turn per persistHistory call")
}

func TestPersistToConversation_NoOpWhenCursorAtEnd(t *testing.T) {
	rt := newRuntimeWithConvStore(t)
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "single"})

	rt.persistToConversation("sess-noop", hist)
	rt.persistToConversation("sess-noop", hist) // same snapshot, cursor at end

	events, err := rt.conversationStore.GetEvents(context.Background(), "sess-noop", conversation.GetEventsOpts{Limit: 100})
	require.NoError(t, err)
	require.Len(t, events, 1, "second call must be a no-op when nothing new")
}

func TestPersistToConversation_CompactionResetsCursor(t *testing.T) {
	rt := newRuntimeWithConvStore(t)
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "msg-1"})
	hist.Append(message.Message{Role: "assistant", Content: "reply-1"})
	hist.Append(message.Message{Role: "user", Content: "msg-2"})
	hist.Append(message.Message{Role: "assistant", Content: "reply-2"})
	rt.persistToConversation("sess-compact", hist)

	rt.convCursorMu.Lock()
	require.Equal(t, 4, rt.convCursor["sess-compact"])
	rt.convCursorMu.Unlock()

	// History.Replace simulates compaction: snapshot shrinks below cursor.
	hist.Replace([]message.Message{
		{Role: "system", Content: "compacted summary"},
		{Role: "user", Content: "msg-2"},
	})
	rt.persistToConversation("sess-compact", hist)

	// Cursor should have been reset and the new short snapshot re-emitted.
	rt.convCursorMu.Lock()
	require.Equal(t, 2, rt.convCursor["sess-compact"])
	rt.convCursorMu.Unlock()

	events, err := rt.conversationStore.GetEvents(context.Background(), "sess-compact", conversation.GetEventsOpts{Limit: 100})
	require.NoError(t, err)
	require.Len(t, events, 6, "4 pre-compaction + 2 post-compaction events")
}

func TestPersistToConversation_RoleMapping(t *testing.T) {
	rt := newRuntimeWithConvStore(t)
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "u"})
	hist.Append(message.Message{Role: "system", Content: "s"})
	hist.Append(message.Message{Role: "developer", Content: "d"})
	hist.Append(message.Message{Role: "assistant", Content: "a"})
	hist.Append(message.Message{Role: "weird", Content: "?"})

	rt.persistToConversation("sess-roles", hist)

	events, err := rt.conversationStore.GetEvents(context.Background(), "sess-roles", conversation.GetEventsOpts{Limit: 100})
	require.NoError(t, err)
	require.Len(t, events, 5)

	require.Equal(t, string(conversation.EventKindUserMessage), events[0].Kind)
	require.Equal(t, string(conversation.EventKindSystem), events[1].Kind)
	require.Equal(t, string(conversation.EventKindSystem), events[2].Kind) // developer → system
	require.Equal(t, string(conversation.EventKindAssistantText), events[3].Kind)
	require.Equal(t, string(conversation.EventKindSystem), events[4].Kind) // unknown role → system, role preserved
	require.Equal(t, "weird", events[4].Role)
}

// TestPersistToConversation_ToolResultPaired verifies the positional
// matching in pairToolResultsToCalls — a "tool" message immediately
// after an assistant with ToolCalls picks up the next call's id.
func TestPersistToConversation_ToolResultPaired(t *testing.T) {
	rt := newRuntimeWithConvStore(t)
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "search please"})
	hist.Append(message.Message{
		Role: "assistant",
		ToolCalls: []message.ToolCall{
			{ID: "call-A", Name: "search", Arguments: map[string]any{"q": "saker"}},
			{ID: "call-B", Name: "read", Arguments: map[string]any{}},
		},
	})
	hist.Append(message.Message{Role: "tool", Content: "result-A"})
	hist.Append(message.Message{Role: "function", Content: "result-B"})

	rt.persistToConversation("sess-tool-pair", hist)

	events, err := rt.conversationStore.GetEvents(context.Background(), "sess-tool-pair", conversation.GetEventsOpts{Limit: 100})
	require.NoError(t, err)
	// 1 user + 2 assistant_tool_call + 2 tool_result = 5 (no assistant_text:
	// Content is empty and ContentBlocks too).
	require.Len(t, events, 5)
	require.Equal(t, string(conversation.EventKindUserMessage), events[0].Kind)
	require.Equal(t, string(conversation.EventKindAssistantToolCall), events[1].Kind)
	require.Equal(t, string(conversation.EventKindAssistantToolCall), events[2].Kind)
	require.Equal(t, string(conversation.EventKindToolResult), events[3].Kind)
	require.Equal(t, string(conversation.EventKindToolResult), events[4].Kind)
}

// TestPersistToConversation_ToolResultUnpairedFallsBackToSystem covers
// the safety net: a "tool" message that can't be matched (no preceding
// assistant tool call) is demoted to System rather than failing the
// projection contract.
func TestPersistToConversation_ToolResultUnpairedFallsBackToSystem(t *testing.T) {
	rt := newRuntimeWithConvStore(t)
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "go"})
	hist.Append(message.Message{Role: "tool", Content: "stray result"})

	rt.persistToConversation("sess-tool-stray", hist)

	events, err := rt.conversationStore.GetEvents(context.Background(), "sess-tool-stray", conversation.GetEventsOpts{Limit: 100})
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, string(conversation.EventKindSystem), events[1].Kind)
	require.Equal(t, "tool", events[1].Role) // role preserved for forensics
}

func TestPersistToConversation_AssistantToolCallFanout(t *testing.T) {
	rt := newRuntimeWithConvStore(t)
	hist := message.NewHistory()
	hist.Append(message.Message{
		Role:    "assistant",
		Content: "I'll look that up.",
		ToolCalls: []message.ToolCall{
			{ID: "call-1", Name: "search", Arguments: map[string]any{"q": "saker"}},
			{ID: "call-2", Name: "read_file", Arguments: map[string]any{"path": "/tmp/x"}},
		},
	})

	rt.persistToConversation("sess-fanout", hist)

	events, err := rt.conversationStore.GetEvents(context.Background(), "sess-fanout", conversation.GetEventsOpts{Limit: 100})
	require.NoError(t, err)
	require.Len(t, events, 3, "assistant text + 2 tool calls = 3 events")

	require.Equal(t, string(conversation.EventKindAssistantText), events[0].Kind)
	require.Equal(t, "I'll look that up.", events[0].ContentText)

	require.Equal(t, string(conversation.EventKindAssistantToolCall), events[1].Kind)
	require.Equal(t, "call-1", toolCallIDFromEvent(t, events[1]))
	require.Equal(t, "search", toolCallNameFromEvent(t, events[1]))

	require.Equal(t, string(conversation.EventKindAssistantToolCall), events[2].Kind)
	require.Equal(t, "call-2", toolCallIDFromEvent(t, events[2]))
	require.Equal(t, "read_file", toolCallNameFromEvent(t, events[2]))

	// All three must share the same turn id.
	require.Equal(t, events[0].TurnID, events[1].TurnID)
	require.Equal(t, events[1].TurnID, events[2].TurnID)
}

func TestPersistToConversation_AssistantToolCallsOnlyNoText(t *testing.T) {
	rt := newRuntimeWithConvStore(t)
	hist := message.NewHistory()
	hist.Append(message.Message{
		Role: "assistant",
		// No content, just tools.
		ToolCalls: []message.ToolCall{
			{ID: "call-1", Name: "search", Arguments: map[string]any{}},
		},
	})
	rt.persistToConversation("sess-toolsonly", hist)

	events, err := rt.conversationStore.GetEvents(context.Background(), "sess-toolsonly", conversation.GetEventsOpts{Limit: 100})
	require.NoError(t, err)
	require.Len(t, events, 1, "must skip empty assistant_text and only emit tool_call")
	require.Equal(t, string(conversation.EventKindAssistantToolCall), events[0].Kind)
}

// TestPersistToConversation_MultiSessionCursorIsolation verifies the
// per-session convCursor map keeps each session's tail offset separate
// — interleaved persists for distinct session ids never cause one
// session's events to land under another's id, and each session's
// cursor advances independently.
//
// Serial on purpose: SQLite under simultaneous writes is exercised by
// pkg/conversation's own concurrency tests; this one targets the cursor
// map invariant.
func TestPersistToConversation_MultiSessionCursorIsolation(t *testing.T) {
	rt := newRuntimeWithConvStore(t)

	histA := message.NewHistory()
	histB := message.NewHistory()

	histA.Append(message.Message{Role: "user", Content: "A1"})
	rt.persistToConversation("sess-A", histA)

	histB.Append(message.Message{Role: "user", Content: "B1"})
	histB.Append(message.Message{Role: "assistant", Content: "B-reply"})
	rt.persistToConversation("sess-B", histB)

	histA.Append(message.Message{Role: "assistant", Content: "A-reply"})
	rt.persistToConversation("sess-A", histA)

	rt.convCursorMu.Lock()
	cursorA := rt.convCursor["sess-A"]
	cursorB := rt.convCursor["sess-B"]
	rt.convCursorMu.Unlock()
	require.Equal(t, 2, cursorA, "sess-A: 1 user + 1 assistant")
	require.Equal(t, 2, cursorB, "sess-B: 1 user + 1 assistant")

	eventsA, err := rt.conversationStore.GetEvents(context.Background(), "sess-A", conversation.GetEventsOpts{Limit: 100})
	require.NoError(t, err)
	require.Len(t, eventsA, 2)

	eventsB, err := rt.conversationStore.GetEvents(context.Background(), "sess-B", conversation.GetEventsOpts{Limit: 100})
	require.NoError(t, err)
	require.Len(t, eventsB, 2)
}

// toolCallIDFromEvent / toolCallNameFromEvent decode the tool-call payload
// embedded in events[].ContentJSON. The events table doesn't store
// tool_call_id / tool_call_name as separate columns (those land in the P1
// projected messages table); appendMessageEvents shoves them into a small
// JSON envelope so future queries can extract without a JOIN.
func toolCallIDFromEvent(t *testing.T, e conversation.Event) string {
	t.Helper()
	return decodeToolCallField(t, e.ContentJSON, "id")
}

func toolCallNameFromEvent(t *testing.T, e conversation.Event) string {
	t.Helper()
	return decodeToolCallField(t, e.ContentJSON, "name")
}

func decodeToolCallField(t *testing.T, raw json.RawMessage, key string) string {
	t.Helper()
	require.NotEmpty(t, raw, "expected ContentJSON to carry the tool call envelope")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	v, ok := payload[key].(string)
	require.True(t, ok, "field %q missing from tool-call envelope", key)
	return v
}
