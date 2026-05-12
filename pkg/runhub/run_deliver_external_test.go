package runhub

import (
	"testing"
	"time"
)

// drainSubscriber pulls every event currently in the channel without
// blocking, with a small per-event grace period so events delivered
// asynchronously from DeliverExternal land before assertion.
func drainSubscriber(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var out []Event
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-deadline.C:
			return out
		}
	}
}

func TestDeliverExternal_PushesToSubscribers(t *testing.T) {
	t.Parallel()
	r := newRun("run_x", "", "t1", nil, 16, time.Time{}, 0, NopMetricsHooks(), nil)

	ch, _, unsub := r.Subscribe()
	defer unsub()

	r.DeliverExternal([]Event{
		{Seq: 1, Type: "chunk", Data: []byte("a")},
		{Seq: 2, Type: "chunk", Data: []byte("b")},
		{Seq: 3, Type: "chunk", Data: []byte("c")},
	})

	got := drainSubscriber(t, ch)
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	for i, e := range got {
		if e.Seq != i+1 {
			t.Errorf("got[%d].Seq = %d, want %d", i, e.Seq, i+1)
		}
	}
}

func TestDeliverExternal_DropsLowerSeqs(t *testing.T) {
	t.Parallel()
	r := newRun("run_x", "", "t1", nil, 16, time.Time{}, 0, NopMetricsHooks(), nil)
	// Bump nextSeq past 5 so anything with Seq <= 5 is a duplicate
	// from a same-process Publish race.
	r.mu.Lock()
	r.nextSeq = 6
	r.mu.Unlock()

	ch, _, unsub := r.Subscribe()
	defer unsub()

	r.DeliverExternal([]Event{
		{Seq: 3, Type: "chunk", Data: []byte("dup")},
		{Seq: 5, Type: "chunk", Data: []byte("dup")},
		{Seq: 6, Type: "chunk", Data: []byte("new")},
		{Seq: 7, Type: "chunk", Data: []byte("new")},
	})

	got := drainSubscriber(t, ch)
	if len(got) != 2 {
		t.Fatalf("expected 2 events (dropping seq < 6), got %d: %+v", len(got), got)
	}
	if got[0].Seq != 6 || got[1].Seq != 7 {
		t.Errorf("expected seqs [6 7], got [%d %d]", got[0].Seq, got[1].Seq)
	}
}

func TestDeliverExternal_BumpsNextSeq(t *testing.T) {
	t.Parallel()
	r := newRun("run_x", "", "t1", nil, 16, time.Time{}, 0, NopMetricsHooks(), nil)

	r.DeliverExternal([]Event{
		{Seq: 10, Type: "chunk", Data: []byte("x")},
	})

	// A subsequent local Publish must not collide with seq 10.
	seq := r.Publish("chunk", []byte("y"))
	if seq != 11 {
		t.Errorf("Publish after DeliverExternal{seq=10} returned %d, want 11", seq)
	}
}

func TestDeliverExternal_AfterCloseIsNoop(t *testing.T) {
	t.Parallel()
	r := newRun("run_x", "", "t1", nil, 16, time.Time{}, 0, NopMetricsHooks(), nil)
	r.closeAllSubscribers()

	// Should not panic, should not update nextSeq.
	r.DeliverExternal([]Event{{Seq: 5, Type: "chunk", Data: []byte("late")}})

	r.mu.Lock()
	got := r.nextSeq
	r.mu.Unlock()
	if got != 1 {
		t.Errorf("nextSeq advanced after close: got %d, want 1", got)
	}
}

func TestDeliverExternal_FillsRingForSnapshotSince(t *testing.T) {
	t.Parallel()
	r := newRun("run_x", "", "t1", nil, 16, time.Time{}, 0, NopMetricsHooks(), nil)

	r.DeliverExternal([]Event{
		{Seq: 1, Type: "chunk", Data: []byte("a")},
		{Seq: 2, Type: "chunk", Data: []byte("b")},
		{Seq: 3, Type: "chunk", Data: []byte("c")},
	})

	// A late SubscribeSince(0) should backfill from the ring (since we
	// have no sink installed, this is the only path).
	_, backfill, recoverable, unsub := r.SubscribeSince(0)
	defer unsub()
	if !recoverable {
		t.Fatalf("recoverable should be true with empty miss")
	}
	if len(backfill) != 3 {
		t.Fatalf("expected 3 backfilled events from ring, got %d", len(backfill))
	}
}
