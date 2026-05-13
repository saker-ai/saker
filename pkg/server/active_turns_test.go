package server

import (
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/api"
	"github.com/stretchr/testify/require"
)

func TestActiveTurnTracker_RegisterAndCount(t *testing.T) {
	tr := NewActiveTurnTracker()
	require.Equal(t, 0, tr.Count())

	tr.Register("t1", "thread-1", "Title One", "hello world", "user")
	tr.Register("t2", "thread-2", "Title Two", "another prompt", "user")

	require.Equal(t, 2, tr.Count())

	tr.Unregister("t1")
	require.Equal(t, 1, tr.Count())

	// Unregister of unknown id is a no-op.
	tr.Unregister("missing")
	require.Equal(t, 1, tr.Count())
}

func TestActiveTurnTracker_RegisterCronAndList(t *testing.T) {
	tr := NewActiveTurnTracker()
	tr.RegisterCron("ct1", "thread-1", "Daily Job", "scan logs", "cron-42")
	turns := tr.List()
	require.Len(t, turns, 1)
	require.Equal(t, "cron", turns[0].Source)
	require.Equal(t, "cron-42", turns[0].CronJobID)
	require.Equal(t, "running", turns[0].Status)
	require.Equal(t, "scan logs", turns[0].Prompt)
	require.False(t, turns[0].StartedAt.IsZero())
}

func TestActiveTurnTracker_TruncateLongPrompt(t *testing.T) {
	tr := NewActiveTurnTracker()
	long := strings.Repeat("a", 250)
	tr.Register("t1", "thread", "Title", long, "user")
	turns := tr.List()
	require.Len(t, turns, 1)
	require.Equal(t, 200+len("..."), len(turns[0].Prompt))
	require.True(t, strings.HasSuffix(turns[0].Prompt, "..."))
}

func TestActiveTurnTracker_AppendStreamText(t *testing.T) {
	tr := NewActiveTurnTracker()
	tr.Register("t1", "thread", "Title", "p", "user")

	tr.AppendStreamText("t1", "hello ")
	tr.AppendStreamText("t1", "world")

	turns := tr.List()
	require.Len(t, turns, 1)
	require.Equal(t, "hello world", turns[0].StreamText)

	// Append for unknown id is a no-op.
	tr.AppendStreamText("missing", "ignored")
	turns = tr.List()
	require.Equal(t, "hello world", turns[0].StreamText)
}

func TestActiveTurnTracker_AppendStreamTextCapped(t *testing.T) {
	tr := NewActiveTurnTracker()
	tr.Register("t1", "thread", "Title", "p", "user")

	// Push past the maxStreamBufLen cap.
	chunk := strings.Repeat("x", maxStreamBufLen)
	tail := strings.Repeat("y", 100)
	tr.AppendStreamText("t1", chunk)
	tr.AppendStreamText("t1", tail)

	turns := tr.List()
	require.Len(t, turns, 1)
	require.Equal(t, maxStreamBufLen, len(turns[0].StreamText))
	require.True(t, strings.HasSuffix(turns[0].StreamText, tail))
}

func TestActiveTurnTracker_SetStatusAndToolName(t *testing.T) {
	tr := NewActiveTurnTracker()
	tr.Register("t1", "thread", "Title", "p", "user")

	tr.SetStatus("t1", "waiting")
	tr.SetToolName("t1", "bash")

	turns := tr.List()
	require.Len(t, turns, 1)
	require.Equal(t, "waiting", turns[0].Status)
	require.Equal(t, "bash", turns[0].ToolName)

	// Unknown id no-ops.
	tr.SetStatus("missing", "nope")
	tr.SetToolName("missing", "nope")
	require.Equal(t, 1, tr.Count())
}

func TestActiveTurnTracker_UpdateFromEvent(t *testing.T) {
	tr := NewActiveTurnTracker()
	tr.Register("t1", "thread", "Title", "p", "user")

	tr.UpdateFromEvent("t1", api.StreamEvent{
		Delta: &api.Delta{Text: "chunk-1 "},
	})
	tr.UpdateFromEvent("t1", api.StreamEvent{
		Delta: &api.Delta{Text: "chunk-2"},
	})
	tr.UpdateFromEvent("t1", api.StreamEvent{
		Type: "tool_execution_start",
		Name: "edit",
	})

	turns := tr.List()
	require.Len(t, turns, 1)
	require.Equal(t, "chunk-1 chunk-2", turns[0].StreamText)
	require.Equal(t, "edit", turns[0].ToolName)

	tr.UpdateFromEvent("t1", api.StreamEvent{Type: "tool_execution_result"})
	turns = tr.List()
	require.Equal(t, "", turns[0].ToolName)

	// Unknown id no-ops.
	tr.UpdateFromEvent("missing", api.StreamEvent{Delta: &api.Delta{Text: "x"}})
}

func TestActiveTurnTracker_UpdateFromEventCapped(t *testing.T) {
	tr := NewActiveTurnTracker()
	tr.Register("t1", "thread", "Title", "p", "user")

	bigText := strings.Repeat("z", maxStreamBufLen+100)
	tr.UpdateFromEvent("t1", api.StreamEvent{Delta: &api.Delta{Text: bigText}})

	turns := tr.List()
	require.Equal(t, maxStreamBufLen, len(turns[0].StreamText))
}

func TestTruncateStr(t *testing.T) {
	require.Equal(t, "abc", truncateStr("abc", 5))
	require.Equal(t, "abcde", truncateStr("abcde", 5))
	require.Equal(t, "abcde...", truncateStr("abcdefgh", 5))
}
