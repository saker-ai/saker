package runhub

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestHub returns a MemoryHub with small caps so tests trigger boundaries
// fast. Tests need the concrete type for direct white-box access (e.g. ring
// internals via *Run.mu); production callers should rely on the Hub interface.
func newTestHub(t *testing.T) *MemoryHub {
	t.Helper()
	h := NewMemoryHub(Config{
		MaxRuns:          4,
		MaxRunsPerTenant: 2,
		RingSize:         8,
		GCInterval:       10 * time.Millisecond,
	})
	t.Cleanup(h.Shutdown)
	return h
}

// drainEvents reads ev until closed or timeout, returns received Events.
func drainEvents(t *testing.T, ev <-chan Event, want int, timeout time.Duration) []Event {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	out := make([]Event, 0, want)
	for {
		if len(out) >= want {
			return out
		}
		select {
		case e, ok := <-ev:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-deadline.C:
			return out
		}
	}
}

// ---------------------------------------------------------------- Hub basics

func TestHubCreate_AssignsUniqueRunIDs(t *testing.T) {
	h := newTestHub(t)
	r1, err := h.Create(CreateOptions{TenantID: "t1"})
	if err != nil {
		t.Fatalf("Create r1: %v", err)
	}
	r2, err := h.Create(CreateOptions{TenantID: "t1"})
	if err != nil {
		t.Fatalf("Create r2: %v", err)
	}
	if r1.ID == r2.ID {
		t.Fatalf("expected unique run ids, both = %s", r1.ID)
	}
	if r1.ID == "" || r1.ID[:4] != "run_" {
		t.Fatalf("expected run_<hex> prefix, got %q", r1.ID)
	}
}

func TestHubCreate_RespectsMaxRuns(t *testing.T) {
	h := newTestHub(t) // MaxRuns=4
	for i := 0; i < 4; i++ {
		if _, err := h.Create(CreateOptions{TenantID: fmt.Sprintf("t%d", i)}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := h.Create(CreateOptions{TenantID: "overflow"}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("expected ErrCapacity past MaxRuns, got %v", err)
	}
}

func TestHubCreate_RespectsMaxRunsPerTenant(t *testing.T) {
	h := newTestHub(t) // MaxRunsPerTenant=2
	for i := 0; i < 2; i++ {
		if _, err := h.Create(CreateOptions{TenantID: "t1"}); err != nil {
			t.Fatalf("tenant-%d: %v", i, err)
		}
	}
	if _, err := h.Create(CreateOptions{TenantID: "t1"}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("expected ErrCapacity past per-tenant cap, got %v", err)
	}
	// Different tenant still admitted.
	if _, err := h.Create(CreateOptions{TenantID: "t2"}); err != nil {
		t.Fatalf("different tenant should pass: %v", err)
	}
}

func TestHubGet_NotFound(t *testing.T) {
	h := newTestHub(t)
	if _, err := h.Get("run_doesnotexist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestHubRemove_DecrementsTenantCount(t *testing.T) {
	h := newTestHub(t)
	r, _ := h.Create(CreateOptions{TenantID: "t1"})
	if got := h.LenForTenant("t1"); got != 1 {
		t.Fatalf("LenForTenant after Create = %d, want 1", got)
	}
	h.Remove(r.ID)
	if got := h.LenForTenant("t1"); got != 0 {
		t.Fatalf("LenForTenant after Remove = %d, want 0", got)
	}
}

// ---------------------------------------------------------- Publish & Ring

func TestPublish_AssignsMonotonicSeq(t *testing.T) {
	h := newTestHub(t)
	r, _ := h.Create(CreateOptions{})
	for i := 1; i <= 5; i++ {
		seq := r.Publish("chunk", []byte(fmt.Sprintf("ev-%d", i)))
		if seq != i {
			t.Fatalf("Publish #%d got seq %d, want %d", i, seq, i)
		}
	}
}

func TestRing_OverwritesOldest(t *testing.T) {
	h := newTestHub(t) // RingSize=8
	r, _ := h.Create(CreateOptions{})
	// Publish 12 events into a 8-slot ring → first 4 evicted.
	for i := 1; i <= 12; i++ {
		r.Publish("chunk", []byte(fmt.Sprintf("ev-%d", i)))
	}
	snap := r.Snapshot()
	if len(snap) != 8 {
		t.Fatalf("Snapshot len = %d, want 8", len(snap))
	}
	if snap[0].Seq != 5 || snap[7].Seq != 12 {
		t.Fatalf("ring window = [%d..%d], want [5..12]", snap[0].Seq, snap[7].Seq)
	}
}

func TestSnapshotSince_NotRecoverableWhenAgedOut(t *testing.T) {
	h := newTestHub(t) // RingSize=8
	r, _ := h.Create(CreateOptions{})
	for i := 1; i <= 12; i++ {
		r.Publish("chunk", []byte("x"))
	}
	// Asking for events after seq 1 — but seq 2,3,4 have been evicted.
	got, ok := r.SnapshotSince(1)
	if ok {
		t.Fatalf("expected recoverable=false, got true (events=%d)", len(got))
	}
}

func TestSnapshotSince_RecoverableWhenInRing(t *testing.T) {
	h := newTestHub(t)
	r, _ := h.Create(CreateOptions{})
	for i := 1; i <= 6; i++ {
		r.Publish("chunk", []byte("x"))
	}
	got, ok := r.SnapshotSince(3)
	if !ok {
		t.Fatalf("expected recoverable=true")
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (seq 4,5,6)", len(got))
	}
	if got[0].Seq != 4 || got[2].Seq != 6 {
		t.Fatalf("got seqs %d..%d, want 4..6", got[0].Seq, got[2].Seq)
	}
}

// ----------------------------------------------------- Subscribe & Fan-Out

func TestSubscribe_Backfill(t *testing.T) {
	h := newTestHub(t)
	r, _ := h.Create(CreateOptions{})
	for i := 1; i <= 3; i++ {
		r.Publish("chunk", []byte(fmt.Sprintf("ev-%d", i)))
	}
	_, backfill, unsub := r.Subscribe()
	defer unsub()
	if len(backfill) != 3 {
		t.Fatalf("backfill = %d, want 3", len(backfill))
	}
	if backfill[0].Seq != 1 || backfill[2].Seq != 3 {
		t.Fatalf("backfill seqs %d..%d, want 1..3", backfill[0].Seq, backfill[2].Seq)
	}
}

func TestPublish_FansOutToAllSubscribers(t *testing.T) {
	h := newTestHub(t)
	r, _ := h.Create(CreateOptions{})

	const N = 4
	chans := make([]<-chan Event, N)
	unsubs := make([]func(), N)
	for i := 0; i < N; i++ {
		ev, _, u := r.Subscribe()
		chans[i] = ev
		unsubs[i] = u
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	r.Publish("chunk", []byte("hello"))

	for i, ch := range chans {
		select {
		case e := <-ch:
			if e.Seq != 1 || string(e.Data) != "hello" {
				t.Fatalf("subscriber %d got seq=%d data=%q", i, e.Seq, e.Data)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d did not receive within 1s", i)
		}
	}
}

func TestPublish_SlowSubscriberDoesNotBlockProducer(t *testing.T) {
	h := newTestHub(t) // RingSize=8 → subscriber chan cap = max(8/4, 16) = 16
	r, _ := h.Create(CreateOptions{})

	// Subscribe but never read — chan fills up at 16.
	_, _, unsub := r.Subscribe()
	defer unsub()

	// Publish 100 events quickly. Producer must not block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			r.Publish("chunk", []byte("x"))
		}
	}()

	select {
	case <-done:
		// Producer completed without blocking — good.
	case <-time.After(2 * time.Second):
		t.Fatalf("producer blocked on slow subscriber")
	}
}

func TestSubscribeSince_NotRecoverableReturnsNilChan(t *testing.T) {
	h := newTestHub(t)
	r, _ := h.Create(CreateOptions{})
	for i := 1; i <= 12; i++ {
		r.Publish("chunk", []byte("x"))
	}
	ch, _, ok, unsub := r.SubscribeSince(1)
	defer unsub()
	if ok {
		t.Fatalf("expected recoverable=false past ring")
	}
	if ch != nil {
		t.Fatalf("expected nil channel when not recoverable")
	}
}

func TestUnsub_Idempotent(t *testing.T) {
	h := newTestHub(t)
	r, _ := h.Create(CreateOptions{})
	_, _, unsub := r.Subscribe()
	unsub()
	unsub() // second call must not panic on double-close
}

func TestUnsub_RemovesFromFanOut(t *testing.T) {
	h := newTestHub(t)
	r, _ := h.Create(CreateOptions{})
	_, _, unsub1 := r.Subscribe()
	ch2, _, unsub2 := r.Subscribe()
	defer unsub2()
	unsub1()

	r.Publish("chunk", []byte("after-unsub"))
	select {
	case e := <-ch2:
		if string(e.Data) != "after-unsub" {
			t.Fatalf("ch2 got %q", e.Data)
		}
	case <-time.After(time.Second):
		t.Fatalf("ch2 did not receive after partner unsub")
	}
}

// ---------------------------------------------------------- Lifecycle

func TestCancel_CallsCancelFuncAndSetsStatus(t *testing.T) {
	h := newTestHub(t)
	called := atomic.Bool{}
	r, _ := h.Create(CreateOptions{
		Cancel: func() { called.Store(true) },
	})
	if err := h.Cancel(r.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if !called.Load() {
		t.Fatalf("Cancel did not invoke run cancel func")
	}
	if r.Status() != RunStatusCancelling {
		t.Fatalf("status after Cancel = %s, want cancelling", r.Status())
	}
}

func TestFinish_ClosesSubscribers(t *testing.T) {
	h := newTestHub(t)
	r, _ := h.Create(CreateOptions{})
	ev, _, unsub := r.Subscribe()
	defer unsub()

	r.Publish("chunk", []byte("only-event"))
	h.Finish(r.ID, RunStatusCompleted)

	// Drain — chan must close.
	got := drainEvents(t, ev, 100, 500*time.Millisecond)
	if len(got) < 1 || string(got[0].Data) != "only-event" {
		t.Fatalf("expected the published event before close, got %d events", len(got))
	}

	// Second receive must return chan-closed (zero value).
	if _, open := <-ev; open {
		t.Fatalf("expected channel closed after Finish")
	}

	if !r.IsTerminal() {
		t.Fatalf("expected terminal after Finish, got %s", r.Status())
	}
}

func TestPublish_AfterCloseIsNoop(t *testing.T) {
	h := newTestHub(t)
	r, _ := h.Create(CreateOptions{})
	h.Finish(r.ID, RunStatusCompleted)
	// Returns 0 after close — must not panic.
	if seq := r.Publish("chunk", []byte("late")); seq != 0 {
		t.Fatalf("publish after Finish returned seq %d, want 0", seq)
	}
}

func TestShutdown_CancelsAllRuns(t *testing.T) {
	h := NewMemoryHub(Config{MaxRuns: 16, RingSize: 8, GCInterval: 100 * time.Millisecond})
	cancelCount := atomic.Int32{}
	for i := 0; i < 3; i++ {
		_, err := h.Create(CreateOptions{
			TenantID: fmt.Sprintf("t%d", i),
			Cancel:   func() { cancelCount.Add(1) },
		})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	h.Shutdown()
	if got := cancelCount.Load(); got != 3 {
		t.Fatalf("Shutdown invoked %d cancel funcs, want 3", got)
	}
	if got := h.Len(); got != 0 {
		t.Fatalf("hub len after Shutdown = %d, want 0", got)
	}
}

// ---------------------------------------------------------------- GC

func TestGC_SweepsExpiredRuns(t *testing.T) {
	h := NewMemoryHub(Config{
		MaxRuns:    8,
		RingSize:   8,
		GCInterval: 10 * time.Millisecond,
	})
	defer h.Shutdown()

	cancelled := atomic.Bool{}
	r, _ := h.Create(CreateOptions{
		TenantID:  "t1",
		ExpiresAt: time.Now().Add(20 * time.Millisecond),
		Cancel:    func() { cancelled.Store(true) },
	})
	r.SetStatus(RunStatusInProgress) // non-terminal so the expire branch fires
	h.StartGC(context.Background())

	// Wait for sweep to pick up the expired run. Bound at 1s to keep
	// this test from hanging if the sweeper regresses.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cancelled.Load() && h.Len() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("GC did not sweep expired run within 1s (cancelled=%v len=%d)",
		cancelled.Load(), h.Len())
}

// ----------------------------------------------------- Concurrency / Race

// TestConcurrent_PublishAndSubscribe stresses the lock graph: many
// publishers + many subscribers + concurrent unsubs. Run with -race to
// catch any read/write under r.mu that escaped.
func TestConcurrent_PublishAndSubscribe(t *testing.T) {
	h := newTestHub(t)
	r, _ := h.Create(CreateOptions{})

	const (
		nProducers   = 4
		nSubscribers = 8
		perProducer  = 50
	)

	var wg sync.WaitGroup

	// Subscribers that read until close.
	for i := 0; i < nSubscribers; i++ {
		ev, _, unsub := r.Subscribe()
		wg.Add(1)
		go func(ch <-chan Event, u func()) {
			defer wg.Done()
			defer u()
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			for {
				select {
				case _, ok := <-ch:
					if !ok {
						return
					}
				case <-deadline.C:
					return
				}
			}
		}(ev, unsub)
	}

	// Producers.
	var prodWg sync.WaitGroup
	for i := 0; i < nProducers; i++ {
		prodWg.Add(1)
		go func(idx int) {
			defer prodWg.Done()
			for j := 0; j < perProducer; j++ {
				r.Publish("chunk", []byte(fmt.Sprintf("p%d-%d", idx, j)))
			}
		}(i)
	}
	prodWg.Wait()

	// Tear down so subscriber goroutines exit.
	h.Finish(r.ID, RunStatusCompleted)
	wg.Wait()

	// Sanity check: nextSeq advanced by exactly nProducers*perProducer.
	r.mu.Lock()
	got := r.nextSeq - 1
	r.mu.Unlock()
	if got != nProducers*perProducer {
		t.Fatalf("nextSeq = %d, want %d", got, nProducers*perProducer)
	}
}
