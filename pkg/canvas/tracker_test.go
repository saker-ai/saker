package canvas

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunTrackerCreateAndGet(t *testing.T) {
	t.Parallel()
	tt := NewRunTracker()
	defer tt.Stop()

	id := tt.Create("thread-1", func() {})
	if id == "" {
		t.Fatal("Create returned empty id")
	}
	got, ok := tt.Get(id)
	if !ok || got == nil {
		t.Fatalf("Get returned ok=%v summary=%v", ok, got)
	}
	if got.ThreadID != "thread-1" || got.Status != RunStatusRunning {
		t.Fatalf("unexpected summary: %+v", got)
	}
}

func TestRunTrackerGetReturnsCopy(t *testing.T) {
	t.Parallel()
	tt := NewRunTracker()
	defer tt.Stop()
	id := tt.Create("t", func() {})
	got, _ := tt.Get(id)
	got.Nodes = append(got.Nodes, NodeRunResult{NodeID: "x"})
	again, _ := tt.Get(id)
	if len(again.Nodes) != 0 {
		t.Fatalf("Get should return a copy: %+v", again.Nodes)
	}
}

func TestRunTrackerUpdate(t *testing.T) {
	t.Parallel()
	tt := NewRunTracker()
	defer tt.Stop()
	id := tt.Create("t", func() {})
	tt.Update(id, &RunSummary{
		RunID:     id,
		ThreadID:  "t",
		Status:    RunStatusDone,
		Total:     2,
		Succeeded: 2,
		Nodes:     []NodeRunResult{{NodeID: "n1", Status: NodeStatusDone}},
	})
	got, ok := tt.Get(id)
	if !ok || got.Status != RunStatusDone || got.Succeeded != 2 || len(got.Nodes) != 1 {
		t.Fatalf("after update: %+v", got)
	}
}

func TestRunTrackerCancelTriggersCallback(t *testing.T) {
	t.Parallel()
	tt := NewRunTracker()
	defer tt.Stop()
	var called int32
	id := tt.Create("t", func() { atomic.StoreInt32(&called, 1) })
	if !tt.Cancel(id) {
		t.Fatal("Cancel should succeed for running run")
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Fatal("cancel func was not invoked")
	}
}

func TestRunTrackerCancelRefusesAfterTerminal(t *testing.T) {
	t.Parallel()
	tt := NewRunTracker()
	defer tt.Stop()
	id := tt.Create("t", func() {})
	tt.Update(id, &RunSummary{RunID: id, ThreadID: "t", Status: RunStatusDone})
	if tt.Cancel(id) {
		t.Fatal("Cancel should refuse a finished run")
	}
}

func TestRunTrackerCancelUnknownReturnsFalse(t *testing.T) {
	t.Parallel()
	tt := NewRunTracker()
	defer tt.Stop()
	if tt.Cancel("nope") {
		t.Fatal("Cancel should return false for unknown run")
	}
}

func TestRunTrackerCleanupEvictsTerminalRuns(t *testing.T) {
	t.Parallel()
	// Skip ticker entirely — drive cleanup logic by hand.
	tt := &RunTracker{
		runs:   make(map[string]*runEntry),
		ttl:    time.Hour,
		stopCh: make(chan struct{}),
		now:    time.Now,
	}
	id := tt.Create("t", func() {})
	tt.Update(id, &RunSummary{
		RunID:      id,
		ThreadID:   "t",
		Status:     RunStatusDone,
		FinishedAt: time.Now().Add(-2 * time.Hour),
	})
	// Manually trigger one cleanup pass.
	tt.mu.Lock()
	cutoff := tt.now().Add(-tt.ttl)
	for runID, e := range tt.runs {
		if e.summary.Status == RunStatusRunning {
			continue
		}
		if !e.summary.FinishedAt.IsZero() && e.summary.FinishedAt.Before(cutoff) {
			delete(tt.runs, runID)
		}
	}
	tt.mu.Unlock()
	if _, ok := tt.Get(id); ok {
		t.Fatal("cleanup failed to evict")
	}
}

func TestRunTrackerStopIsIdempotent(t *testing.T) {
	t.Parallel()
	tt := NewRunTracker()
	tt.Stop()
	tt.Stop() // must not panic
}

func TestRunTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	tt := NewRunTracker()
	defer tt.Stop()
	id := tt.Create("t", func() {})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tt.Update(id, &RunSummary{RunID: id, ThreadID: "t", Status: RunStatusRunning, Total: i})
			tt.Get(id)
		}(i)
	}
	wg.Wait()
	if _, ok := tt.Get(id); !ok {
		t.Fatal("run vanished under concurrent access")
	}
}
