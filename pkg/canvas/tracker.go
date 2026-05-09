package canvas

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Run lifecycle constants surfaced through the JSON-RPC / REST API.
const (
	RunStatusRunning   = "running"
	RunStatusDone      = "done"
	RunStatusError     = "error"
	RunStatusCancelled = "cancelled"

	NodeStatusPending = "pending"
	NodeStatusRunning = "running"
	NodeStatusDone    = "done"
	NodeStatusError   = "error"
	NodeStatusSkipped = "skipped"
)

// RunSummary is the public, serialisable record for a single canvas run.
// The shape is what callers see via canvas/run-status and the REST endpoint.
type RunSummary struct {
	RunID      string          `json:"runId"`
	ThreadID   string          `json:"threadId"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt time.Time       `json:"finishedAt,omitempty"`
	Status     string          `json:"status"`
	Total      int             `json:"total"`
	Succeeded  int             `json:"succeeded"`
	Failed     int             `json:"failed"`
	Skipped    int             `json:"skipped"`
	Nodes      []NodeRunResult `json:"nodes"`
	Error      string          `json:"error,omitempty"`
}

// NodeRunResult records the per-node outcome inside a RunSummary.
type NodeRunResult struct {
	NodeID       string `json:"nodeId"`
	NodeType     string `json:"nodeType"`
	Tool         string `json:"tool,omitempty"`
	Status       string `json:"status"`
	DurationMs   int64  `json:"durationMs"`
	Error        string `json:"error,omitempty"`
	ResultURL    string `json:"resultUrl,omitempty"`
	ResultNodeID string `json:"resultNodeId,omitempty"`
}

// runEntry is the internal record held by RunTracker. It owns the cancel
// func that lets the API cancel an in-flight run.
type runEntry struct {
	summary *RunSummary
	cancel  context.CancelFunc
}

// RunTracker is the canvas-side equivalent of server.TaskTracker. It is
// keyed by runId and stores summaries directly so callers can poll status.
type RunTracker struct {
	mu      sync.RWMutex
	runs    map[string]*runEntry
	now     func() time.Time // injection point for tests
	ttl     time.Duration    // completed-run retention
	stopCh  chan struct{}
	stopped bool
}

// NewRunTracker starts the background cleanup goroutine and returns a
// ready-to-use tracker.
func NewRunTracker() *RunTracker {
	t := &RunTracker{
		runs:   make(map[string]*runEntry),
		now:    time.Now,
		ttl:    30 * time.Minute,
		stopCh: make(chan struct{}),
	}
	go t.cleanup()
	return t
}

// Stop terminates the cleanup goroutine. Safe to call multiple times.
func (t *RunTracker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	t.stopped = true
	close(t.stopCh)
}

// Create reserves a runId, registers a fresh summary in "running" state,
// and stores the cancel func so Cancel can interrupt the run later.
func (t *RunTracker) Create(threadID string, cancel context.CancelFunc) string {
	id := uuid.New().String()
	t.mu.Lock()
	t.runs[id] = &runEntry{
		summary: &RunSummary{
			RunID:     id,
			ThreadID:  threadID,
			StartedAt: t.now(),
			Status:    RunStatusRunning,
			Nodes:     []NodeRunResult{},
		},
		cancel: cancel,
	}
	t.mu.Unlock()
	return id
}

// Update replaces the summary atomically. Callers should pass a fully
// populated copy — RunTracker does not merge fields.
func (t *RunTracker) Update(runID string, summary *RunSummary) {
	if summary == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.runs[runID]; ok {
		cp := *summary
		e.summary = &cp
	}
}

// Get returns a snapshot of the summary, or false when the run is unknown
// or has been evicted.
func (t *RunTracker) Get(runID string) (*RunSummary, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.runs[runID]
	if !ok {
		return nil, false
	}
	cp := *e.summary
	cp.Nodes = append([]NodeRunResult(nil), e.summary.Nodes...)
	return &cp, true
}

// Cancel triggers the run's context cancel func, returning false when no
// such run exists or it has already finished.
func (t *RunTracker) Cancel(runID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.runs[runID]
	if !ok || e.cancel == nil {
		return false
	}
	if e.summary.Status != RunStatusRunning {
		return false
	}
	e.cancel()
	return true
}

// cleanup runs every minute and evicts terminal runs older than ttl. It
// exits when Stop is called.
func (t *RunTracker) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.mu.Lock()
			cutoff := t.now().Add(-t.ttl)
			for id, e := range t.runs {
				if e.summary.Status == RunStatusRunning {
					continue
				}
				if !e.summary.FinishedAt.IsZero() && e.summary.FinishedAt.Before(cutoff) {
					delete(t.runs, id)
				}
			}
			t.mu.Unlock()
		}
	}
}
