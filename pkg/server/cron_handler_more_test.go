package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// handleTurnsActive: tracker present + populated → returns the snapshot.
func TestHandleTurnsActive_WithTracker(t *testing.T) {
	t.Parallel()
	tracker := NewActiveTurnTracker()
	tracker.Register("turn-1", "thread-1", "Title", "hi", "user")

	h := &Handler{tracker: tracker}
	resp := h.handleTurnsActive(rpcRequest("turns/active", 1, nil))
	require.Nil(t, resp.Error)

	out := resp.Result.(map[string]any)
	turns := out["turns"].([]ActiveTurn)
	require.Len(t, turns, 1)
	require.Equal(t, "turn-1", turns[0].TurnID)
}

func TestHandleToolTaskStatus_MissingTaskID(t *testing.T) {
	t.Parallel()
	h := &Handler{taskTracker: &TaskTracker{tasks: make(map[string]*GenTask)}}
	resp := h.handleToolTaskStatus(rpcRequest("tool/task/status", 1, nil))
	require.NotNil(t, resp.Error)
	require.Contains(t, resp.Error.Message, "taskId is required")
}

func TestHandleToolTaskStatus_NotFound(t *testing.T) {
	t.Parallel()
	h := &Handler{taskTracker: &TaskTracker{tasks: make(map[string]*GenTask)}}
	resp := h.handleToolTaskStatus(rpcRequest("tool/task/status", 1, map[string]any{
		"taskId": "missing",
	}))
	require.NotNil(t, resp.Error)
	require.Contains(t, resp.Error.Message, "task not found")
}

func TestHandleToolTaskStatus_Found(t *testing.T) {
	t.Parallel()
	tt := &TaskTracker{tasks: make(map[string]*GenTask)}
	id := tt.Create("genImage", "node-x")
	h := &Handler{taskTracker: tt}
	resp := h.handleToolTaskStatus(rpcRequest("tool/task/status", 1, map[string]any{
		"taskId": id,
	}))
	require.Nil(t, resp.Error)
	got := resp.Result.(*GenTask)
	require.Equal(t, id, got.ID)
	require.Equal(t, "genImage", got.ToolName)
	require.Equal(t, "running", got.Status)
}

func TestHandleToolActiveTasks(t *testing.T) {
	t.Parallel()
	tt := &TaskTracker{tasks: make(map[string]*GenTask)}
	id := tt.Create("genImage", "")
	h := &Handler{taskTracker: tt}
	resp := h.handleToolActiveTasks(rpcRequest("tool/active", 1, nil))
	require.Nil(t, resp.Error)
	out := resp.Result.(map[string]any)
	tasks := out["tasks"].([]*GenTask)
	require.Len(t, tasks, 1)
	require.Equal(t, id, tasks[0].ID)
}
