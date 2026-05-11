package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewTaskTracker_BackgroundLoop(t *testing.T) {
	// Smoke test that NewTaskTracker returns a usable tracker. The background
	// cleanup goroutine has no Close API and is allow-listed in main_test.go.
	tt := NewTaskTracker()
	require.NotNil(t, tt)
	id := tt.Create("genImage", "")
	require.NotEmpty(t, id)
}

func TestTaskTracker_CreateAndGet(t *testing.T) {
	tt := &TaskTracker{tasks: make(map[string]*GenTask)}
	id := tt.Create("genImage", "node-1")
	require.NotEmpty(t, id)

	task, ok := tt.Get(id)
	require.True(t, ok)
	require.Equal(t, "genImage", task.ToolName)
	require.Equal(t, "node-1", task.NodeID)
	require.Equal(t, "running", task.Status)
	require.Equal(t, 0, task.Progress)
	require.False(t, task.CreatedAt.IsZero())
	require.True(t, task.DoneAt.IsZero())

	// Get returns a copy: mutating the returned value should not affect store.
	task.Status = "tampered"
	again, ok := tt.Get(id)
	require.True(t, ok)
	require.Equal(t, "running", again.Status)

	_, ok = tt.Get("missing")
	require.False(t, ok)
}

func TestTaskTracker_Complete(t *testing.T) {
	tt := &TaskTracker{tasks: make(map[string]*GenTask)}
	id := tt.Create("genImage", "")
	tt.Complete(id, map[string]any{"url": "http://x"})

	got, ok := tt.Get(id)
	require.True(t, ok)
	require.Equal(t, "done", got.Status)
	require.Equal(t, 100, got.Progress)
	require.Equal(t, "http://x", got.Result["url"])
	require.False(t, got.DoneAt.IsZero())

	// Complete on missing id is a no-op.
	tt.Complete("missing", map[string]any{})
}

func TestTaskTracker_Fail(t *testing.T) {
	tt := &TaskTracker{tasks: make(map[string]*GenTask)}
	id := tt.Create("genVideo", "")
	tt.Fail(id, "boom")

	got, ok := tt.Get(id)
	require.True(t, ok)
	require.Equal(t, "error", got.Status)
	require.Equal(t, "boom", got.Error)
	require.Equal(t, 100, got.Progress)
	require.False(t, got.DoneAt.IsZero())

	tt.Fail("missing", "ignored")
}

func TestTaskTracker_UpdateProgress(t *testing.T) {
	tt := &TaskTracker{tasks: make(map[string]*GenTask)}
	id := tt.Create("genImage", "")

	tt.UpdateProgress(id, 50, "halfway")
	got, _ := tt.Get(id)
	require.Equal(t, 50, got.Progress)
	require.Equal(t, "halfway", got.Message)

	// Negative clamps to 0.
	tt.UpdateProgress(id, -5, "neg")
	got, _ = tt.Get(id)
	require.Equal(t, 0, got.Progress)

	// Over-100 clamps to 100.
	tt.UpdateProgress(id, 150, "over")
	got, _ = tt.Get(id)
	require.Equal(t, 100, got.Progress)

	// Missing id no-op.
	tt.UpdateProgress("missing", 10, "x")
}

func TestTaskTracker_AppendLog(t *testing.T) {
	tt := &TaskTracker{tasks: make(map[string]*GenTask)}
	id := tt.Create("genImage", "")
	tt.AppendLog(id, "line 1")
	tt.AppendLog(id, "line 2")

	got, _ := tt.Get(id)
	require.Equal(t, []string{"line 1", "line 2"}, got.Logs)

	// Missing id no-op.
	tt.AppendLog("missing", "x")
}

func TestTaskTracker_ActiveTasks(t *testing.T) {
	tt := &TaskTracker{tasks: make(map[string]*GenTask)}

	runningID := tt.Create("genImage", "")
	doneID := tt.Create("genVideo", "")
	tt.Complete(doneID, map[string]any{})

	// Manually back-date a completed task to be just-completed (within window).
	tt.mu.Lock()
	tt.tasks[doneID].DoneAt = time.Now()
	tt.mu.Unlock()

	staleID := tt.Create("genImage", "")
	tt.Complete(staleID, map[string]any{})

	// Make stale task look completed >10 minutes ago.
	tt.mu.Lock()
	tt.tasks[staleID].DoneAt = time.Now().Add(-15 * time.Minute)
	tt.mu.Unlock()

	active := tt.ActiveTasks()
	ids := map[string]bool{}
	for _, a := range active {
		ids[a.ID] = true
	}
	require.True(t, ids[runningID], "running task should be active")
	require.True(t, ids[doneID], "recently completed task should be active")
	require.False(t, ids[staleID], "stale completed task should be filtered")
}
