package server

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// GenTask represents a background tool execution task.
type GenTask struct {
	ID        string         `json:"id"`
	NodeID    string         `json:"nodeId,omitempty"`
	ToolName  string         `json:"toolName"`
	Status    string         `json:"status"` // "running", "done", "error"
	Progress  int            `json:"progress,omitempty"`
	Message   string         `json:"message,omitempty"`
	Logs      []string       `json:"logs,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
	DoneAt    time.Time      `json:"doneAt,omitempty"`
}

// TaskTracker tracks background tool execution tasks in memory.
type TaskTracker struct {
	mu    sync.RWMutex
	tasks map[string]*GenTask
}

// NewTaskTracker creates a new TaskTracker.
func NewTaskTracker() *TaskTracker {
	tt := &TaskTracker{tasks: make(map[string]*GenTask)}
	go tt.cleanup()
	return tt
}

// Create registers a new running task and returns its ID.
func (tt *TaskTracker) Create(toolName, nodeID string) string {
	id := uuid.New().String()
	tt.mu.Lock()
	tt.tasks[id] = &GenTask{
		ID:        id,
		NodeID:    nodeID,
		ToolName:  toolName,
		Status:    "running",
		Progress:  0,
		CreatedAt: time.Now(),
	}
	tt.mu.Unlock()
	return id
}

// Complete marks a task as done with the given result.
func (tt *TaskTracker) Complete(id string, result map[string]any) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if t, ok := tt.tasks[id]; ok {
		t.Status = "done"
		t.Progress = 100
		t.Result = result
		t.DoneAt = time.Now()
	}
}

// Fail marks a task as failed with the given error message.
func (tt *TaskTracker) Fail(id string, errMsg string) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if t, ok := tt.tasks[id]; ok {
		t.Status = "error"
		t.Progress = 100
		t.Error = errMsg
		t.DoneAt = time.Now()
	}
}

// UpdateProgress updates a running task with progress and a human-readable message.
func (tt *TaskTracker) UpdateProgress(id string, progress int, message string) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if t, ok := tt.tasks[id]; ok {
		if progress < 0 {
			progress = 0
		}
		if progress > 100 {
			progress = 100
		}
		t.Progress = progress
		t.Message = message
	}
}

// AppendLog records a task log line for UI progress displays.
func (tt *TaskTracker) AppendLog(id string, line string) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if t, ok := tt.tasks[id]; ok {
		t.Logs = append(t.Logs, line)
	}
}

// Get returns a task by ID.
func (tt *TaskTracker) Get(id string) (*GenTask, bool) {
	tt.mu.RLock()
	defer tt.mu.RUnlock()
	t, ok := tt.tasks[id]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

// ActiveTasks returns all tasks that are running or recently completed (within 10 minutes).
func (tt *TaskTracker) ActiveTasks() []*GenTask {
	tt.mu.RLock()
	defer tt.mu.RUnlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	result := make([]*GenTask, 0)
	for _, t := range tt.tasks {
		if t.Status == "running" || t.DoneAt.After(cutoff) {
			cp := *t
			result = append(result, &cp)
		}
	}
	return result
}

// cleanup periodically removes completed tasks older than 10 minutes.
func (tt *TaskTracker) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		tt.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for id, t := range tt.tasks {
			if t.Status != "running" && t.DoneAt.Before(cutoff) {
				delete(tt.tasks, id)
			}
		}
		tt.mu.Unlock()
	}
}
