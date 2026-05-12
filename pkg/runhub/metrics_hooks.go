package runhub

import "time"

// MetricsHooks lets a hub publish observability events without depending
// directly on prometheus or any other concrete instrumentation library.
// The gateway wires a prometheus-backed implementation; tests pass a noop.
//
// All methods must be safe for concurrent use and cheap (no I/O, no locks
// outside the metric library's own internals) — they are called on hot
// paths like every Run.Publish.
type MetricsHooks interface {
	// OnEventPersist is called once per batchWriter.flush that reached
	// the store (success=true) or hit an error from InsertEventsBatch
	// (success=false). `dur` is the InsertEventsBatch wall-clock —
	// observed once per call regardless of batch size, so histogram
	// dimensions stay tenant-free. `tenants` lists the tenant IDs of
	// every envelope in the flushed batch (length == batch size); the
	// implementation increments the per-tenant counter once per entry,
	// so a flush of N envelopes produces N counter bumps split across
	// however many tenants those envelopes spanned. An empty slice is
	// allowed (e.g. drained-to-zero flush) and bumps no counter.
	OnEventPersist(success bool, dur time.Duration, tenants []string)
	// OnListenerStart / OnListenerStop bracket each per-run LISTEN session
	// (postgres backend only). Used to track active subscription count.
	OnListenerStart()
	OnListenerStop()
	// OnNotifyDropped is incremented when a notification can't be delivered
	// to a subscriber because their buffer is full. Producer never blocks.
	// `tenant` is the originating run's tenant id (empty for runs without
	// tenant attribution); cardinality is bounded on the implementation
	// side by the same overflow scheme as oversized_events_total.
	OnNotifyDropped(tenant string)
	// OnBatchDrop is incremented when the async batch writer's enqueue
	// channel is full and the oldest event is dropped to make room.
	// `tenant` is the dropped envelope's tenant id (empty for runs
	// without tenant attribution); cardinality bounded as above.
	OnBatchDrop(tenant string)
	// OnBatchQueueDepth reports the current depth of the batch writer's
	// enqueue channel. Sampled, not real-time. Hub-level signal — no
	// tenant attribution because a single number can't fan out per tenant.
	OnBatchQueueDepth(depth int)
	// OnRevival is incremented every time a run is loaded from the store
	// back into memory (PersistentHub Get / loadActive paths). `tenant`
	// is the revived run's tenant id (empty for runs without tenant
	// attribution); cardinality bounded as above.
	OnRevival(tenant string)
	// OnListenerReconnect tracks the outcome of an auto-reconnect attempt
	// by the shared LISTEN pool reader goroutine.
	OnListenerReconnect(success bool)
	// OnOversizedEvent is incremented when Run.Publish or DeliverExternal
	// rejects an event because its payload exceeds Config.MaxEventBytes.
	// The tenant arg is the originating tenant id (empty for runs without
	// tenant attribution); a high-cardinality cap on the prometheus side
	// is the implementation's responsibility.
	OnOversizedEvent(tenant string)
	// OnSubscriberIdleEvicted is incremented once per subscriber the GC
	// sweeper closes because their channel sat idle for longer than
	// Config.SubscriberIdleTimeout. The tenant arg lets operators see
	// which tenant has leaky SSE clients.
	OnSubscriberIdleEvicted(tenant string)
	// OnSinkBreakerState reports the current state of the persistent
	// hub's sink circuit breaker (closed | half_open | open). Called
	// every time the breaker transitions AND once on construction so
	// dashboards see the initial state from boot.
	OnSinkBreakerState(state string)
	// OnSinkBreakerTransition is incremented once per state change.
	// Both `from` and `to` are one of {closed, half_open, open}; the
	// pair lets a dashboard distinguish "transient blip" (half_open
	// -> closed) from "persistent outage" (closed -> open).
	OnSinkBreakerTransition(from, to string)
	// OnSinkBreakerSkipped is incremented for every batch flush
	// suppressed by an Open breaker. The argument is the number of
	// envelopes in the dropped batch — operators can chart skipped
	// rows/sec vs persisted rows/sec to size the impact of an outage.
	OnSinkBreakerSkipped(rows int)
	// OnBatchFlush is called once per batchWriter.flush invocation that
	// reaches the store (i.e. NOT suppressed by an Open breaker). `size`
	// is the envelope count actually sent to the store; `dur` is the
	// wall-clock time from the moment flush started draining the buffer
	// to the moment it returned. Distinct from OnEventPersist.dur, which
	// times only the InsertEventsBatch call — this hook also covers the
	// row-marshal loop, the per-run NOTIFY de-dup loop, and per-run
	// Notify calls. Operators chart the gap between the two histograms
	// to detect NOTIFY storms or marshalling regressions.
	OnBatchFlush(size int, dur time.Duration)
}

// noopHooks is the default MetricsHooks used when none is provided. All
// methods are no-ops so production code can call hooks unconditionally.
type noopHooks struct{}

func (noopHooks) OnEventPersist(bool, time.Duration, []string) {}
func (noopHooks) OnListenerStart()                             {}
func (noopHooks) OnListenerStop()                              {}
func (noopHooks) OnNotifyDropped(string)                       {}
func (noopHooks) OnBatchDrop(string)                           {}
func (noopHooks) OnBatchQueueDepth(int)                        {}
func (noopHooks) OnRevival(string)                             {}
func (noopHooks) OnListenerReconnect(bool)                     {}
func (noopHooks) OnOversizedEvent(string)                      {}
func (noopHooks) OnSubscriberIdleEvicted(string)               {}
func (noopHooks) OnSinkBreakerState(string)                    {}
func (noopHooks) OnSinkBreakerTransition(string, string)       {}
func (noopHooks) OnSinkBreakerSkipped(int)                     {}
func (noopHooks) OnBatchFlush(int, time.Duration)              {}

// NopMetricsHooks returns a MetricsHooks that ignores every call. Useful
// for tests and for the MemoryHub default path.
func NopMetricsHooks() MetricsHooks { return noopHooks{} }
