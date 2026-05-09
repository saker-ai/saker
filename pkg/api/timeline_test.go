package api

import (
	"sync"
	"testing"
)

func TestTimelineCollectorConcurrentAdd(t *testing.T) {
	c := &timelineCollector{}
	const goroutines = 50
	const entriesPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < entriesPerGoroutine; i++ {
				c.add(TimelineEntry{
					Kind: TimelineToolCall,
					Name: "concurrent-step",
				})
			}
		}(g)
	}
	wg.Wait()

	snapshot := c.snapshot()
	expected := goroutines * entriesPerGoroutine
	if len(snapshot) != expected {
		t.Fatalf("expected %d entries, got %d — concurrent add lost entries", expected, len(snapshot))
	}
}

func TestTimelineCollectorSnapshotIsolation(t *testing.T) {
	c := &timelineCollector{}
	c.add(TimelineEntry{Kind: TimelineToolCall, Name: "step-1"})
	c.add(TimelineEntry{Kind: TimelineToolResult, Name: "step-1"})

	snap := c.snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 entries in snapshot, got %d", len(snap))
	}

	// Adding more entries after snapshot should NOT affect the snapshot
	c.add(TimelineEntry{Kind: TimelineCacheHit, Name: "step-2"})

	if len(snap) != 2 {
		t.Fatal("snapshot was mutated by subsequent add — isolation broken")
	}

	snap2 := c.snapshot()
	if len(snap2) != 3 {
		t.Fatalf("expected 3 entries in second snapshot, got %d", len(snap2))
	}
}

func TestTimelineCollectorSnapshotEmpty(t *testing.T) {
	c := &timelineCollector{}
	snap := c.snapshot()
	if snap != nil {
		t.Fatalf("expected nil snapshot for empty collector, got %v", snap)
	}
}

func TestTimelineCollectorTimestampPopulated(t *testing.T) {
	c := &timelineCollector{}
	c.add(TimelineEntry{Kind: TimelineToolCall, Name: "ts-check"})

	snap := c.snapshot()
	if snap[0].Timestamp.IsZero() {
		t.Fatal("expected add() to populate Timestamp, got zero")
	}
}
