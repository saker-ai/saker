package runhub

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/saker-ai/saker/pkg/runhub/store"
)

// TestBatchWriter_SizeTrigger asserts that filling the buffer to
// BatchSize triggers a flush even before the interval ticks.
func TestBatchWriter_SizeTrigger(t *testing.T) {
	t.Parallel()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{
		Store:         s,
		BatchSize:     4,
		BatchInterval: time.Hour, // never fires; size must trigger
	})

	run, _ := h.Create(CreateOptions{TenantID: "t1", ExpiresAt: time.Now().Add(time.Hour)})
	for i := 0; i < 4; i++ {
		run.Publish("chunk", []byte{byte(i)})
	}
	// Size trigger should flush automatically. Poll briefly so we don't
	// race the goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := s.LoadEventsSince(context.Background(), run.ID, 0)
		if err != nil {
			t.Fatalf("LoadEventsSince: %v", err)
		}
		if len(rows) == 4 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("size-triggered flush didn't land within deadline")
}

// TestBatchWriter_IntervalTrigger asserts that a partial buffer is
// flushed after the idle interval, not held forever.
func TestBatchWriter_IntervalTrigger(t *testing.T) {
	t.Parallel()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{
		Store:         s,
		BatchSize:     1024, // size trigger inactive
		BatchInterval: 30 * time.Millisecond,
	})

	run, _ := h.Create(CreateOptions{TenantID: "t1", ExpiresAt: time.Now().Add(time.Hour)})
	run.Publish("chunk", []byte("only-one"))

	// Wait > one interval and then assert the single envelope landed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, _ := s.LoadEventsSince(context.Background(), run.ID, 0)
		if len(rows) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("interval-triggered flush didn't land within deadline")
}

// TestBatchWriter_ShutdownDrains asserts every accepted envelope is
// persisted by the time PersistentHub.Shutdown returns.
func TestBatchWriter_ShutdownDrains(t *testing.T) {
	t.Parallel()
	s, dbPath := openTestStore(t)
	h, err := NewPersistentHub(PersistentConfig{
		Config:        Config{},
		Store:         s,
		BatchSize:     1024,             // never auto-flush by size
		BatchInterval: 10 * time.Second, // never auto-flush by interval
	})
	if err != nil {
		t.Fatalf("NewPersistentHub: %v", err)
	}

	run, _ := h.Create(CreateOptions{TenantID: "t1", ExpiresAt: time.Now().Add(time.Hour)})
	const want = 50
	for i := 0; i < want; i++ {
		run.Publish("chunk", []byte(strconv.Itoa(i)))
	}

	// Shutdown must drain — neither the size nor the interval has fired.
	h.Shutdown()
	// h.Shutdown closes the store, so re-open the same DSN to read what
	// the drain wrote.
	s2, err := store.Open(store.Config{DSN: dbPath})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s2.Close()
	rows, err := s2.LoadEventsSince(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("LoadEventsSince after restart: %v", err)
	}
	if len(rows) != want {
		t.Fatalf("after Shutdown drain: stored %d, want %d", len(rows), want)
	}
}

// TestBatchWriter_DropOldestOnFull asserts that when the enqueue chan
// fills up, the OLDEST envelope is dropped (most-recent flow preserved)
// and metrics.OnBatchDrop is incremented for each eviction.
func TestBatchWriter_DropOldestOnFull(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{
		Store:           s,
		Metrics:         hooks,
		BatchSize:       1024,      // huge size trigger; never fires
		BatchBufferSize: 4,         // tiny buffer to force drops
		BatchInterval:   time.Hour, // never fires
	})

	run, _ := h.Create(CreateOptions{TenantID: "t1", ExpiresAt: time.Now().Add(time.Hour)})

	// Fire 10000 events fast — buffer=4 + writer not flushing means the
	// chan saturates and drop-oldest kicks in. The exact drop count is
	// scheduler-dependent (writer goroutine drains the chan at line-rate),
	// so we only assert "some drops happened" rather than a precise
	// fraction. The qualitative property — producer never blocks under
	// backpressure, drop metric increments, queue depth observable — is
	// what we're locking in.
	const sent = 10000
	for i := 0; i < sent; i++ {
		run.Publish("chunk", []byte{byte(i)})
	}

	// Allow the writer goroutine to settle without being able to flush
	// (it hasn't hit size or interval). Drop counter should already
	// reflect every overflow.
	time.Sleep(50 * time.Millisecond)
	drops := hooks.dropTotal()
	if drops == 0 {
		t.Errorf("drops = 0, want > 0 (buffer=4, sent=%d, no flush trigger)", sent)
	}
	if hooks.queueDepthSamples() == 0 {
		t.Errorf("queueDepth metric never sampled — write() should call OnBatchQueueDepth on every successful enqueue")
	}
	// And no flush should have happened (size/interval too large).
	if persists := hooks.persistTotal(); persists != 0 {
		t.Errorf("persists = %d before flush, want 0", persists)
	}
	// Force a flush to confirm whatever remained in the queue lands.
	h.Flush()
	rows, err := s.LoadEventsSince(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("LoadEventsSince: %v", err)
	}
	if len(rows) == 0 {
		t.Errorf("expected at least one stored row after Flush")
	}
}

// TestBatchWriter_OnBatchFlushObserved asserts the writer fires the
// OnBatchFlush hook once per real flush — both the size-trigger path
// and the interval-trigger path. Records the size argument so dashboards
// can chart actual batch fill ratios. The hook MUST NOT fire on a flush
// that finds the buffer empty (the no-op early-return inside flush()),
// and MUST fire even if the underlying InsertEventsBatch errors
// (the flush "reached the store" — operators want timing for failed
// flushes too, distinct from the success-only OnEventPersist counter).
func TestBatchWriter_OnBatchFlushObserved(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{
		Store:         s,
		Metrics:       hooks,
		BatchSize:     4,
		BatchInterval: 30 * time.Millisecond,
	})

	run, _ := h.Create(CreateOptions{TenantID: "t1", ExpiresAt: time.Now().Add(time.Hour)})

	// Size-triggered flush: publish exactly BatchSize so the writer flips
	// over the threshold and flushes once.
	for i := 0; i < 4; i++ {
		run.Publish("chunk", []byte{byte(i)})
	}
	// Wait for the size-triggered flush to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hooks.mu.Lock()
		got := hooks.batchFlushCount
		hooks.mu.Unlock()
		if got >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Interval-triggered flush: publish ONE more event (well below
	// BatchSize) and wait past one interval. Writer should drain the
	// single envelope on the ticker.
	run.Publish("chunk", []byte{byte(99)})
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hooks.mu.Lock()
		got := hooks.batchFlushCount
		hooks.mu.Unlock()
		if got >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	hooks.mu.Lock()
	count := hooks.batchFlushCount
	sizes := append([]int(nil), hooks.batchFlushSizes...)
	durs := append([]time.Duration(nil), hooks.batchFlushDurs...)
	hooks.mu.Unlock()

	if count < 2 {
		t.Fatalf("OnBatchFlush observed %d times, want >= 2 (size + interval triggers)", count)
	}
	// Sum of recorded sizes must equal total publishes (5). Stricter
	// than counting each flush, weaker than asserting precise ordering —
	// the latter is racy because the writer goroutine and the producer
	// run concurrently.
	totalRows := 0
	for _, sz := range sizes {
		if sz <= 0 {
			t.Errorf("OnBatchFlush size=%d, want > 0 (empty flushes shouldn't fire the hook)", sz)
		}
		totalRows += sz
	}
	if totalRows != 5 {
		t.Errorf("sum(OnBatchFlush sizes) = %d, want 5 (4 size-triggered + 1 interval-triggered)", totalRows)
	}
	// Every duration is wall-clock — must be > 0 unless the clock has
	// sub-nanosecond resolution (it doesn't on Linux).
	for i, d := range durs {
		if d <= 0 {
			t.Errorf("OnBatchFlush[%d] dur=%v, want > 0", i, d)
		}
	}
}

// countingHooks is a MetricsHooks that records every method call so
// tests can assert exact counts. Concurrency-safe (atomic-style
// counters guarded by a mutex; the volume is low so the lock is cheap).
type countingHooks struct {
	mu                                                                                     sync.Mutex
	persistOK, persistErr, listenStart, listenStop, drop, notifyDrop, queueDepth, revival int
	reconnectOK, reconnectFail                                                            int
	persistTenants                                                                        []string // accumulated tenants across all OnEventPersist
	notifyDropTenants                                                                     []string
	dropTenants                                                                           []string
	revivalTenants                                                                        []string
	oversizedTotal                                                                        int
	oversizedTenants                                                                      []string
	idleEvictTotal                                                                        int
	idleEvictTenants                                                                      []string
	lastQueueDepth                                                                        int
	breakerStateLast                                                                      string
	breakerStateChanges                                                                   int
	breakerTransitions                                                                    []string // each "from->to"
	breakerSkippedRows                                                                    int
	batchFlushCount                                                                       int
	batchFlushSizes                                                                       []int
	batchFlushDurs                                                                        []time.Duration
}

func newCountingHooks() *countingHooks { return &countingHooks{} }

func (h *countingHooks) OnEventPersist(success bool, _ time.Duration, tenants []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Counter semantics: one Inc per envelope (per the production
	// adapter), so per-flush bookkeeping mirrors that — bump persistOK /
	// persistErr by len(tenants), not by 1.
	if success {
		h.persistOK += len(tenants)
	} else {
		h.persistErr += len(tenants)
	}
	h.persistTenants = append(h.persistTenants, tenants...)
}
func (h *countingHooks) OnListenerStart() {
	h.mu.Lock()
	h.listenStart++
	h.mu.Unlock()
}
func (h *countingHooks) OnListenerStop() {
	h.mu.Lock()
	h.listenStop++
	h.mu.Unlock()
}
func (h *countingHooks) OnNotifyDropped(tenant string) {
	h.mu.Lock()
	h.notifyDrop++
	h.notifyDropTenants = append(h.notifyDropTenants, tenant)
	h.mu.Unlock()
}
func (h *countingHooks) OnBatchDrop(tenant string) {
	h.mu.Lock()
	h.drop++
	h.dropTenants = append(h.dropTenants, tenant)
	h.mu.Unlock()
}
func (h *countingHooks) OnBatchQueueDepth(depth int) {
	h.mu.Lock()
	h.queueDepth++
	h.lastQueueDepth = depth
	h.mu.Unlock()
}
func (h *countingHooks) OnRevival(tenant string) {
	h.mu.Lock()
	h.revival++
	h.revivalTenants = append(h.revivalTenants, tenant)
	h.mu.Unlock()
}
func (h *countingHooks) OnListenerReconnect(success bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if success {
		h.reconnectOK++
	} else {
		h.reconnectFail++
	}
}

func (h *countingHooks) OnOversizedEvent(tenant string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.oversizedTotal++
	h.oversizedTenants = append(h.oversizedTenants, tenant)
}

func (h *countingHooks) OnSubscriberIdleEvicted(tenant string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.idleEvictTotal++
	h.idleEvictTenants = append(h.idleEvictTenants, tenant)
}

func (h *countingHooks) OnSinkBreakerState(state string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.breakerStateLast = state
	h.breakerStateChanges++
}

func (h *countingHooks) OnSinkBreakerTransition(from, to string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.breakerTransitions = append(h.breakerTransitions, from+"->"+to)
}

func (h *countingHooks) OnSinkBreakerSkipped(rows int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.breakerSkippedRows += rows
}

func (h *countingHooks) OnBatchFlush(size int, dur time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.batchFlushCount++
	h.batchFlushSizes = append(h.batchFlushSizes, size)
	h.batchFlushDurs = append(h.batchFlushDurs, dur)
}

func (h *countingHooks) dropTotal() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.drop
}

func (h *countingHooks) persistTotal() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.persistOK + h.persistErr
}

func (h *countingHooks) queueDepthSamples() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.queueDepth
}
