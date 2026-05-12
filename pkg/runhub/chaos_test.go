package runhub

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestChaos_SinkErrorDoesntBlockSubscribers asserts that a sink whose
// every write errors never blocks the producer or starves subscribers
// — the Publish path absorbs sink failures and still fans the event out
// to live subscribers and the in-memory ring.
//
// Worst-case "DB is down" simulation: when persistence is failing for
// every event, the ring + subscriber path must keep serving live
// traffic. A transient backend outage degrades to "no replay across
// restart", not "no streaming". The test asserts two properties:
//  1. The producer's Publish loop returns within a tight watchdog (sink
//     errors don't backpressure into the publisher).
//  2. A drain goroutine that keeps up with the publisher receives every
//     event the bounded subscriber chan can hold (>= 16) — proving the
//     fan-out path still ran, not just that the producer side completed.
func TestChaos_SinkErrorDoesntBlockSubscribers(t *testing.T) {
	t.Parallel()
	// RingSize 1024 → subscriber chan cap = 256, comfortably above the
	// 100-event burst so a real-time consumer drains without drops. The
	// behavior under test (sink errors don't block fan-out) is measured by
	// (a) producer completion and (b) non-zero delivery, NOT every event.
	hub := NewMemoryHub(Config{RingSize: 1024})
	t.Cleanup(hub.Shutdown)

	run, err := hub.Create(CreateOptions{TenantID: "chaos", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	run.attachSink(failingSink{})

	ch, _, unsub := run.Subscribe()
	defer unsub()

	// Drain in the background so the bounded subscriber chan never blocks
	// the per-Run fan-out path — that's the layer under test, not the
	// (well-known) drop-on-full subscriber semantics covered separately.
	var wg sync.WaitGroup
	wg.Add(1)
	var got int
	stopDrain := make(chan struct{})
	go func() {
		defer wg.Done()
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
				got++
			case <-stopDrain:
				return
			}
		}
	}()

	// Watchdog the producer: a stuck Publish (sink-induced backpressure
	// regression) trips here instead of hanging the test indefinitely.
	const want = 100
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < want; i++ {
			run.Publish("chunk", []byte{byte(i)})
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("producer Publish loop blocked > 3s with failing sink — sink errors should not backpressure into Publish")
	}

	// Let the drain goroutine catch up, then assert non-zero delivery.
	time.Sleep(50 * time.Millisecond)
	close(stopDrain)
	wg.Wait()
	if got == 0 {
		t.Fatalf("subscriber received 0 events despite producer completing — fan-out is broken when sink errors")
	}
}

// TestChaos_SubscriberBufferOverflow_DropsToSlowOnly asserts that a
// subscriber with a full chan does NOT starve another subscriber on the
// same run. Per-Run fan-out drops to a full chan rather than blocking
// the producer, so each subscriber's slowness only hurts itself.
//
// This is the in-process analog of the postgres NOTIFY-buffer overflow
// covered in pkg/runhub/store/chaos_postgres_test.go.
func TestChaos_SubscriberBufferOverflow_DropsToSlowOnly(t *testing.T) {
	t.Parallel()
	hub := NewMemoryHub(Config{RingSize: 16})
	t.Cleanup(hub.Shutdown)

	run, err := hub.Create(CreateOptions{TenantID: "chaos", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	fastCh, _, fastUnsub := run.Subscribe()
	defer fastUnsub()
	slowCh, _, slowUnsub := run.Subscribe()
	defer slowUnsub()

	// Fast consumer drains in the background and counts what it gets.
	var fastWG sync.WaitGroup
	fastWG.Add(1)
	var fastGot int
	go func() {
		defer fastWG.Done()
		for range fastCh {
			fastGot++
		}
	}()

	// Slow consumer never reads. Its bounded chan fills up and the
	// producer drops events to it; the fast consumer must keep pace
	// regardless of slow's saturation.
	const sent = 1000
	for i := 0; i < sent; i++ {
		run.Publish("chunk", []byte{byte(i)})
	}

	hub.Finish(run.ID, RunStatusCompleted)
	fastWG.Wait()

	if fastGot == 0 {
		t.Fatalf("fast consumer received 0 events — slow consumer's full chan starved fan-out")
	}
	// Drain the (still-full) slow chan to verify it received SOME events
	// (the initial buffer-fill) but not every event (drops happened).
	slowGot := 0
	for range slowCh {
		slowGot++
	}
	if slowGot == 0 {
		t.Errorf("slow consumer received 0 events; expected at least the initial buffer-fill")
	}
	if slowGot >= sent {
		t.Errorf("slow consumer received %d events; expected drops (buffered cap is bounded, sent=%d)", slowGot, sent)
	}
}

// TestChaos_BatchWriterDoesntBlockProducer is the watchdog form of
// TestBatchWriter_DropOldestOnFull: under sustained burst with a
// stalled writer (no size or interval trigger fires), Publish must
// return quickly even when the enqueue chan is full. The producer is
// run in a goroutine guarded by a deadline; a stuck Publish trips the
// watchdog instead of hanging the test.
func TestChaos_BatchWriterDoesntBlockProducer(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{
		Store:           s,
		Metrics:         hooks,
		BatchSize:       1024,      // size trigger inactive
		BatchBufferSize: 4,         // tiny queue → forces drop-oldest
		BatchInterval:   time.Hour, // interval trigger inactive
	})
	run, err := h.Create(CreateOptions{TenantID: "chaos", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const burst = 5000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < burst; i++ {
			run.Publish("chunk", []byte{byte(i)})
		}
	}()
	select {
	case <-done:
		// Producer returned in time.
	case <-time.After(2 * time.Second):
		t.Fatalf("Publish loop blocked > 2s (burst=%d, queue=4) — drop-oldest should keep producer non-blocking", burst)
	}
	if hooks.dropTotal() == 0 {
		t.Errorf("expected drops > 0 (burst=%d, queue=4, no flush trigger), got 0", burst)
	}
}

// TestChaos_SubscribeAfterFinish_NoLeak is the regression for the
// closed-window race in Subscribe: before E.2, Subscribe didn't check
// r.closed under r.mu, so a Subscribe that lost the race to
// closeAllSubscribers would attach a fresh subscriber AFTER the slice
// was zeroed — leaving a chan that never sees a value AND never closes.
// The fix mirrors SubscribeSince's existing "if r.closed → pre-closed
// chan" branch. This test asserts the consumer's for-range exits within
// a tight watchdog, NOT after some indefinite block.
func TestChaos_SubscribeAfterFinish_NoLeak(t *testing.T) {
	t.Parallel()
	hub := NewMemoryHub(Config{RingSize: 16})
	t.Cleanup(hub.Shutdown)

	run, err := hub.Create(CreateOptions{TenantID: "leak-check", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Push the run into a terminal state so closeAllSubscribers has run
	// — Subscribe must now hand back a pre-closed channel.
	hub.Finish(run.ID, RunStatusCompleted)

	ch, _, unsub := run.Subscribe()
	defer unsub()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("Subscribe on finished run yielded a value, want closed channel")
		}
	case <-time.After(time.Second):
		t.Fatalf("Subscribe on finished run returned an open channel — leak (consumer would block forever)")
	}
}

// TestChaos_IdleSubscriberEvicted asserts the GC sweeper closes
// subscriber channels that sit idle past Config.SubscriberIdleTimeout
// AND that the eviction is reported via OnSubscriberIdleEvicted with
// the run's tenant label. This is the recovery path for SSE clients
// that disconnected without invoking their unsub closure.
func TestChaos_IdleSubscriberEvicted(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	hub := NewMemoryHub(Config{
		RingSize:              16,
		GCInterval:            20 * time.Millisecond,
		SubscriberIdleTimeout: 50 * time.Millisecond,
		Metrics:               hooks,
	})
	t.Cleanup(hub.Shutdown)
	hub.StartGC(context.Background())

	run, err := hub.Create(CreateOptions{TenantID: "idle-tenant", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Intentionally do NOT call unsub — that's the leak we're recovering from.
	ch, _, _ := run.Subscribe()

	// Poll the metric counter, NOT the channel close: chan close fires
	// inside evictIdleSubscribers, but the OnSubscriberIdleEvicted call
	// happens AFTER it returns to sweep. A test that reads the counter
	// the instant the chan closes can race the metric increment.
	deadline := time.Now().Add(2 * time.Second)
	var gotEvicts int
	var gotTenants []string
	for time.Now().Before(deadline) {
		hooks.mu.Lock()
		gotEvicts = hooks.idleEvictTotal
		gotTenants = append([]string(nil), hooks.idleEvictTenants...)
		hooks.mu.Unlock()
		if gotEvicts >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if gotEvicts < 1 {
		t.Fatalf("expected >= 1 idle eviction metric within deadline, got %d (subscriber leak path is broken)", gotEvicts)
	}

	// Confirm the chan was actually closed too — eviction without close
	// would leave the consumer goroutine wedged.
	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("evicted subscriber chan still yields values, want closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("evicted subscriber chan never closed (metric fired but chan stays open — bug in evictIdleSubscribers)")
	}

	foundTenant := false
	for _, tn := range gotTenants {
		if tn == "idle-tenant" {
			foundTenant = true
			break
		}
	}
	if !foundTenant {
		t.Errorf("expected idle eviction metric to carry tenant=idle-tenant, got %v", gotTenants)
	}
}

// TestChaos_ActiveSubscriberNotEvicted is the negative case of the
// above: as long as the subscriber receives events more often than the
// idle timeout, it must NOT be evicted. Otherwise an active SSE client
// would lose its stream just for being on a low-rate run.
func TestChaos_ActiveSubscriberNotEvicted(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	hub := NewMemoryHub(Config{
		RingSize:              16,
		GCInterval:            10 * time.Millisecond,
		SubscriberIdleTimeout: 100 * time.Millisecond,
		Metrics:               hooks,
	})
	t.Cleanup(hub.Shutdown)
	hub.StartGC(context.Background())

	run, err := hub.Create(CreateOptions{TenantID: "active", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ch, _, unsub := run.Subscribe()
	defer unsub()

	// Drain in the background so the chan never fills (a full chan would
	// stop bumping lastReadAt and the subscriber would qualify as idle).
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
			case <-stop:
				return
			}
		}
	}()

	// Publish well within the idle timeout for ~3x the timeout window;
	// the subscriber should not get evicted.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		run.Publish("chunk", []byte("x"))
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	wg.Wait()

	if got := hooks.idleEvictTotal; got != 0 {
		t.Errorf("active subscriber was evicted (count=%d) — eviction must respect lastReadAt updates", got)
	}
}

// failingSink is an eventSink whose write always errors. Used to assert
// that Publish's "sink is best-effort" guarantee actually holds — fan-
// out and ring writes proceed even when persistence is broken.
type failingSink struct{}

func (failingSink) write(context.Context, string, string, Event) error {
	return errors.New("chaos: simulated sink failure")
}

func (failingSink) loadSince(context.Context, string, int) ([]Event, error) {
	return nil, errors.New("chaos: simulated sink failure")
}

// TestChaos_SinkBreakerOpensOnPersistentFailure asserts that when the
// underlying store starts failing every InsertEventsBatch, the dbSink
// circuit breaker trips Open after `threshold` consecutive failures
// and starts skipping subsequent flushes (counted in
// OnSinkBreakerSkipped). The breaker prevents a stuck store from
// burning CPU + log volume on every batch interval.
//
// Setup: open a real store, then Close() it underneath the writer.
// The first few flushes fail (driving the consec-failure counter to
// `threshold`), the breaker trips Open, and from that point on no
// further store calls are made — every flush is suppressed.
//
// Drive the writer with one publish per ~5 ms — too slow to coalesce
// into a single batch, so each event becomes its own (failing) flush
// and the consec-failure counter ticks up the way the breaker contract
// expects. (Calling h.Flush() between batches would defeat the test
// because flushSync drains the entire enqueue chan into one batch.)
func TestChaos_SinkBreakerOpensOnPersistentFailure(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{
		Store:                s,
		Metrics:              hooks,
		BatchSize:            1, // every Publish triggers a flush — fastest path to threshold
		BatchInterval:        time.Hour,
		SinkBreakerThreshold: 3,
		SinkBreakerCooldown:  time.Hour, // long enough that recovery doesn't race
	})
	run, err := h.Create(CreateOptions{TenantID: "chaos", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Sabotage the store — every subsequent InsertEventsBatch will fail.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Publish slowly so each event lands in its own batch (BatchSize=1
	// + writer goroutine has time to drain between each Publish).
	for i := 0; i < 6; i++ {
		run.Publish("chunk", []byte{byte(i)})
		time.Sleep(20 * time.Millisecond)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		hooks.mu.Lock()
		opened := contains(hooks.breakerTransitions, "closed->open")
		hooks.mu.Unlock()
		if opened {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Now publish more events — these should be silently skipped by
	// the Open breaker (counted in breakerSkippedRows, NOT persistErr).
	for i := 6; i < 16; i++ {
		run.Publish("chunk", []byte{byte(i)})
		time.Sleep(20 * time.Millisecond)
	}

	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if !contains(hooks.breakerTransitions, "closed->open") {
		t.Fatalf("breaker never tripped Open after >= threshold failed flushes (transitions=%v, persistErr=%d)",
			hooks.breakerTransitions, hooks.persistErr)
	}
	if hooks.persistErr < 3 {
		t.Errorf("persistErr=%d, expected >= 3 (threshold)", hooks.persistErr)
	}
	if hooks.breakerSkippedRows == 0 {
		t.Errorf("breakerSkippedRows=0 — Open breaker should suppress post-trip flushes")
	}
	if hooks.breakerStateLast != "open" {
		t.Errorf("breakerStateLast=%q, want open", hooks.breakerStateLast)
	}
}

// contains is a tiny test helper so we don't pull in slices.Contains
// (saves a dep churn for what is one assertion in one test file).
func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
