package toolbuiltin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/saker-ai/saker/pkg/runtime/tasks"
)

func TestStreamMonitorTool_Name(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)
	if sm.Name() != "stream_monitor" {
		t.Fatalf("expected name stream_monitor, got %s", sm.Name())
	}
}

func TestStreamMonitorTool_InvalidAction(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)
	_, err := sm.Execute(context.Background(), map[string]any{"action": "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestStreamMonitorTool_StartRequiresURL(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)
	_, err := sm.Execute(context.Background(), map[string]any{"action": "start"})
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

func TestStreamMonitorTool_StartRejectsInvalidScheme(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)
	_, err := sm.Execute(context.Background(), map[string]any{
		"action": "start",
		"url":    "http://example.com/not-a-stream",
	})
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestStreamMonitorTool_StartCreatesTask(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)

	result, err := sm.Execute(context.Background(), map[string]any{
		"action":  "start",
		"url":     "rtsp://example.com/stream",
		"events":  "person,vehicle",
		"subject": "Test Monitor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}

	var output map[string]any
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	taskID, ok := output["task_id"].(string)
	if !ok || taskID == "" {
		t.Fatal("expected task_id in output")
	}
	if output["status"] != "started" {
		t.Errorf("expected status=started, got %v", output["status"])
	}

	// Verify task was created
	task, err := store.Get(taskID)
	if err != nil {
		t.Fatalf("task not found: %v", err)
	}
	if task.Status != tasks.TaskInProgress {
		t.Errorf("expected task in_progress, got %s", task.Status)
	}
	if task.Subject != "Test Monitor" {
		t.Errorf("expected subject 'Test Monitor', got %s", task.Subject)
	}

	// Stop the monitor to clean up
	stopResult, err := sm.Execute(context.Background(), map[string]any{
		"action":  "stop",
		"task_id": taskID,
	})
	if err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if !stopResult.Success {
		t.Error("expected stop success")
	}

	// Verify task completed
	task, _ = store.Get(taskID)
	if task.Status != tasks.TaskCompleted {
		t.Errorf("expected completed, got %s", task.Status)
	}
}

func TestStreamMonitorTool_StatusNoMonitor(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)
	result, err := sm.Execute(context.Background(), map[string]any{
		"action":  "status",
		"task_id": "nonexistent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected non-success for missing monitor")
	}
}

func TestStreamMonitorTool_StopNoMonitor(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)
	_, err := sm.Execute(context.Background(), map[string]any{
		"action":  "stop",
		"task_id": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for stopping nonexistent monitor")
	}
}

func TestStreamMonitorTool_StatusRequiresTaskID(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)
	_, err := sm.Execute(context.Background(), map[string]any{"action": "status"})
	if err == nil {
		t.Fatal("expected error for missing task_id")
	}
}

func TestStreamMonitorTool_StopRequiresTaskID(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)
	_, err := sm.Execute(context.Background(), map[string]any{"action": "stop"})
	if err == nil {
		t.Fatal("expected error for missing task_id")
	}
}

func TestStreamMonitorTool_ListMonitors(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)

	// Empty at start
	monitors := sm.ListMonitors()
	if len(monitors) != 0 {
		t.Fatalf("expected 0 monitors, got %d", len(monitors))
	}

	// Start a monitor
	result, err := sm.Execute(context.Background(), map[string]any{
		"action":  "start",
		"url":     "rtsp://example.com/stream",
		"subject": "List Test",
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	var output map[string]any
	json.Unmarshal([]byte(result.Output), &output)
	taskID := output["task_id"].(string)

	monitors = sm.ListMonitors()
	if len(monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(monitors))
	}
	if monitors[0].TaskID != taskID {
		t.Errorf("expected task_id %s, got %s", taskID, monitors[0].TaskID)
	}
	if monitors[0].Subject != "List Test" {
		t.Errorf("expected subject 'List Test', got %s", monitors[0].Subject)
	}
	if !monitors[0].Running {
		t.Error("expected running=true")
	}

	// Cleanup
	sm.Execute(context.Background(), map[string]any{"action": "stop", "task_id": taskID})
}

func TestStreamMonitorTool_Close(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)

	// Start two monitors
	var taskIDs []string
	for _, url := range []string{"rtsp://example.com/a", "rtsp://example.com/b"} {
		result, err := sm.Execute(context.Background(), map[string]any{
			"action": "start",
			"url":    url,
		})
		if err != nil {
			t.Fatalf("start failed: %v", err)
		}
		var output map[string]any
		json.Unmarshal([]byte(result.Output), &output)
		taskIDs = append(taskIDs, output["task_id"].(string))
	}

	if len(sm.ListMonitors()) != 2 {
		t.Fatalf("expected 2 monitors before Close")
	}

	// Close should stop all monitors
	sm.Close()

	if len(sm.ListMonitors()) != 0 {
		t.Fatalf("expected 0 monitors after Close, got %d", len(sm.ListMonitors()))
	}

	// Tasks should be completed
	for _, id := range taskIDs {
		task, err := store.Get(id)
		if err != nil {
			t.Fatalf("task %s not found: %v", id, err)
		}
		if task.Status != tasks.TaskCompleted {
			t.Errorf("task %s: expected completed, got %s", id, task.Status)
		}
	}
}

func TestStreamMonitorTool_StartAndStatus(t *testing.T) {
	store := tasks.NewTaskStore()
	sm := NewStreamMonitorTool(store)

	// Start
	result, err := sm.Execute(context.Background(), map[string]any{
		"action": "start",
		"url":    "rtsp://example.com/stream",
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	var output map[string]any
	json.Unmarshal([]byte(result.Output), &output)
	taskID := output["task_id"].(string)

	// Status
	statusResult, err := sm.Execute(context.Background(), map[string]any{
		"action":  "status",
		"task_id": taskID,
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !statusResult.Success {
		t.Error("expected status success")
	}

	var statusOutput map[string]any
	json.Unmarshal([]byte(statusResult.Output), &statusOutput)
	if statusOutput["running"] != true {
		t.Error("expected running=true")
	}

	// Cleanup
	sm.Execute(context.Background(), map[string]any{"action": "stop", "task_id": taskID})
}
