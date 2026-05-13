package conversation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Tests for the messages projection (P1). The projection runs inside
// the AppendEvent transaction (see store_message.go:projectEventTx),
// so every test here issues AppendEvent calls and then asserts what
// GetMessages observes.

func TestProjection_UserMessage_OneRowPerEvent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	for _, body := range []string{"hi", "are you there", "?"} {
		_, err := s.AppendEvent(ctx, AppendEventInput{
			ThreadID:    th.ID,
			ProjectID:   "proj",
			TurnID:      turnID,
			Kind:        EventKindUserMessage,
			ContentText: body,
		})
		require.NoError(t, err)
	}

	msgs, err := s.GetMessages(ctx, th.ID, GetMessagesOpts{})
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	for i, m := range msgs {
		require.Equal(t, "user", m.Role)
		require.EqualValues(t, i+1, m.Pos, "pos must be thread-scoped monotonic")
		require.Equal(t, turnID, m.TurnID)
	}
	require.Equal(t, "hi", msgs[0].Content)
	require.Equal(t, "?", msgs[2].Content)
}

func TestProjection_AssistantStreaming_ConcatenatesIntoOneRow(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	chunks := []string{"Hello, ", "world", "! ", "How are ", "you?"}
	for _, c := range chunks {
		_, err := s.AppendEvent(ctx, AppendEventInput{
			ThreadID:    th.ID,
			ProjectID:   "proj",
			TurnID:      turnID,
			Kind:        EventKindAssistantText,
			ContentText: c,
		})
		require.NoError(t, err)
	}

	msgs, err := s.GetMessages(ctx, th.ID, GetMessagesOpts{})
	require.NoError(t, err)
	require.Len(t, msgs, 1, "all chunks must collapse into one assistant row")
	require.Equal(t, "assistant", msgs[0].Role)
	require.Equal(t, "Hello, world! How are you?", msgs[0].Content)
	require.EqualValues(t, 1, msgs[0].Pos)
}

func TestProjection_MultipleTurns_GetTheirOwnAssistantRow(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turn1, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)
	turn2, err := s.OpenTurn(ctx, th.ID, turn1)
	require.NoError(t, err)

	for _, c := range []string{"foo", "bar"} {
		_, err := s.AppendEvent(ctx, AppendEventInput{
			ThreadID:    th.ID,
			ProjectID:   "proj",
			TurnID:      turn1,
			Kind:        EventKindAssistantText,
			ContentText: c,
		})
		require.NoError(t, err)
	}
	for _, c := range []string{"baz", "qux"} {
		_, err := s.AppendEvent(ctx, AppendEventInput{
			ThreadID:    th.ID,
			ProjectID:   "proj",
			TurnID:      turn2,
			Kind:        EventKindAssistantText,
			ContentText: c,
		})
		require.NoError(t, err)
	}

	msgs, err := s.GetMessages(ctx, th.ID, GetMessagesOpts{})
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, "foobar", msgs[0].Content)
	require.Equal(t, turn1, msgs[0].TurnID)
	require.Equal(t, "bazqux", msgs[1].Content)
	require.Equal(t, turn2, msgs[1].TurnID)
}

func TestProjection_AssistantToolCall_AppendsToToolCalls(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	// Streamed text first.
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:    th.ID,
		ProjectID:   "proj",
		TurnID:      turnID,
		Kind:        EventKindAssistantText,
		ContentText: "I'll look that up.",
	})
	require.NoError(t, err)

	// Then two tool calls — both should land in the same assistant row.
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:     th.ID,
		ProjectID:    "proj",
		TurnID:       turnID,
		Kind:         EventKindAssistantToolCall,
		ToolCallID:   "call_1",
		ToolCallName: "search",
		ContentJSON:  map[string]any{"q": "weather"},
	})
	require.NoError(t, err)
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:     th.ID,
		ProjectID:    "proj",
		TurnID:       turnID,
		Kind:         EventKindAssistantToolCall,
		ToolCallID:   "call_2",
		ToolCallName: "calculator",
		ContentJSON:  map[string]any{"expr": "2+2"},
	})
	require.NoError(t, err)

	msgs, err := s.GetMessages(ctx, th.ID, GetMessagesOpts{})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "assistant", msgs[0].Role)
	require.Equal(t, "I'll look that up.", msgs[0].Content)

	var calls []map[string]any
	require.NoError(t, json.Unmarshal(msgs[0].ToolCalls, &calls))
	require.Len(t, calls, 2)
	require.Equal(t, "call_1", calls[0]["id"])
	require.Equal(t, "search", calls[0]["name"])
	require.Equal(t, "call_2", calls[1]["id"])
	require.Equal(t, "calculator", calls[1]["name"])
}

func TestProjection_ToolResult_NewRowPerCall(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	// Assistant message with a tool call.
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:     th.ID,
		ProjectID:    "proj",
		TurnID:       turnID,
		Kind:         EventKindAssistantToolCall,
		ToolCallID:   "call_x",
		ToolCallName: "search",
	})
	require.NoError(t, err)

	// Tool result.
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:    th.ID,
		ProjectID:   "proj",
		TurnID:      turnID,
		Kind:        EventKindToolResult,
		ToolCallID:  "call_x",
		ContentText: "weather: sunny",
	})
	require.NoError(t, err)

	msgs, err := s.GetMessages(ctx, th.ID, GetMessagesOpts{})
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	require.Equal(t, "assistant", msgs[0].Role)
	require.Equal(t, "tool", msgs[1].Role)
	require.Equal(t, "call_x", msgs[1].ToolCallID)
	require.Equal(t, "weather: sunny", msgs[1].Content)
}

func TestProjection_ToolResult_RequiresToolCallID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	// Missing ToolCallID on a tool_result must surface an error so the
	// caller learns at append time, not when a UI renders an orphan.
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:    th.ID,
		ProjectID:   "proj",
		TurnID:      turnID,
		Kind:        EventKindToolResult,
		ContentText: "result",
	})
	require.Error(t, err)
}

func TestProjection_UsageAndError_SkippedFromMessages(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	// One real message + one usage + one error.
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:    th.ID,
		ProjectID:   "proj",
		TurnID:      turnID,
		Kind:        EventKindUserMessage,
		ContentText: "hi",
	})
	require.NoError(t, err)
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:    th.ID,
		ProjectID:   "proj",
		TurnID:      turnID,
		Kind:        EventKindUsage,
		ContentJSON: map[string]int{"input": 10, "output": 20},
	})
	require.NoError(t, err)
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:    th.ID,
		ProjectID:   "proj",
		TurnID:      turnID,
		Kind:        EventKindError,
		ContentText: "rate limited",
	})
	require.NoError(t, err)

	// Events log has all three.
	evts, err := s.GetEvents(ctx, th.ID, GetEventsOpts{})
	require.NoError(t, err)
	require.Len(t, evts, 3)

	// Messages projection only has the user message.
	msgs, err := s.GetMessages(ctx, th.ID, GetMessagesOpts{})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "user", msgs[0].Role)
}

func TestGetMessages_AfterPos_Cursor(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		_, err := s.AppendEvent(ctx, AppendEventInput{
			ThreadID:    th.ID,
			ProjectID:   "proj",
			TurnID:      turnID,
			Kind:        EventKindUserMessage,
			ContentText: "msg",
		})
		require.NoError(t, err)
	}

	tail, err := s.GetMessages(ctx, th.ID, GetMessagesOpts{AfterPos: 5})
	require.NoError(t, err)
	require.Len(t, tail, 5)
	require.EqualValues(t, 6, tail[0].Pos)
	require.EqualValues(t, 10, tail[4].Pos)
}

func TestGetMessages_RequiresThreadID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.GetMessages(ctx, "", GetMessagesOpts{})
	require.Error(t, err)
}

func TestProjection_SystemMessage_OneRowPerEvent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:    th.ID,
		ProjectID:   "proj",
		TurnID:      turnID,
		Kind:        EventKindSystem,
		ContentText: "you are a helpful assistant",
	})
	require.NoError(t, err)

	msgs, err := s.GetMessages(ctx, th.ID, GetMessagesOpts{})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "system", msgs[0].Role)
	require.Equal(t, "you are a helpful assistant", msgs[0].Content)
}
