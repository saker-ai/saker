package openai

import (
	"sync"
	"time"

	"github.com/saker-ai/saker/pkg/runhub"
	"github.com/prometheus/client_golang/prometheus"
)

// Runhub-specific prometheus collectors. Kept inside the openai package
// rather than pkg/metrics so the gateway owns its own observability surface
// — the metrics live next to the code that produces them, and operators
// who don't run the gateway never see runhub_* series in /metrics.
//
// Cardinality discipline: every label set is closed enum (status=ok|err,
// outcome=ok|fail). No per-run or per-tenant labels — those would explode
// to millions of series under load.
var (
	runhubEventsPersistedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "events_persisted_total",
		Help:      "Total events written to the runhub store, by outcome and tenant.",
	}, []string{"result", "tenant"}) // result = "ok" | "err"; tenant bounded by labelTenantBounded

	runhubListenersActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "listeners_active",
		Help:      "Current number of per-run LISTEN sessions held by the persistent hub (postgres backend).",
	})

	runhubNotifyDroppedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "notify_dropped_total",
		Help:      "Notifications dropped because a subscriber buffer was full (producer never blocks), by tenant.",
	}, []string{"tenant"}) // bounded by labelTenantBounded

	runhubBatchDropsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "batch_drops_total",
		Help:      "Events dropped by the async batch writer because the enqueue channel was full, by tenant.",
	}, []string{"tenant"}) // bounded by labelTenantBounded

	runhubBatchQueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "batch_queue_depth",
		Help:      "Current depth of the async batch writer's enqueue channel (sampled).",
	})

	runhubRevivalsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "revivals_total",
		Help:      "Total runs reloaded from the persistent store back into memory (Get + loadActive paths), by tenant.",
	}, []string{"tenant"}) // bounded by labelTenantBounded

	runhubSinkWriteDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "sink_write_seconds",
		Help:      "Wall-clock duration of a single store write (per event in Stage A; per batch flush in Stage B+).",
		// Buckets tuned for in-process SQLite + LAN postgres: ~100µs to ~5s.
		Buckets: []float64{0.0001, 0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	})

	runhubListenerReconnectsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "listener_reconnects_total",
		Help:      "Auto-reconnect attempts by the shared LISTEN pool, by outcome.",
	}, []string{"result"}) // result = "ok" | "fail"

	// runhubOversizedEventsTotal counts events rejected by Run.Publish /
	// DeliverExternal because the payload exceeds Config.MaxEventBytes.
	// The tenant label is bounded by oversizedTenantCap to keep series
	// count under control under malicious load — overflow lands in the
	// "_overflow" bucket so operators can still see the rejection signal.
	runhubOversizedEventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "oversized_events_total",
		Help:      "Events rejected because payload exceeded MaxEventBytes, by tenant.",
	}, []string{"tenant"})

	// runhubSubscribersIdleEvictedTotal counts subscriber channels closed
	// by the GC sweeper because the subscriber sat idle past
	// Config.SubscriberIdleTimeout. Targets the leaked-SSE-client failure
	// mode. Tenant label shares oversizedTenantCap (same labelTenantBounded
	// helper) so a tenant that overflows one signal overflows the other —
	// total runhub series stays bounded by the single cap.
	runhubSubscribersIdleEvictedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "subscribers_idle_evicted_total",
		Help:      "Subscriber channels closed by GC because they were idle past Config.SubscriberIdleTimeout, by tenant.",
	}, []string{"tenant"})

	// runhubSinkBreakerState exposes the persistent hub's circuit
	// breaker state as a numeric Gauge so dashboards can alert on
	// "breaker stuck open". Encoded as 0=closed, 1=half_open, 2=open.
	// A Gauge (rather than a state-set with multiple labels) keeps the
	// series count at exactly one regardless of how many transitions
	// have happened.
	runhubSinkBreakerState = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "sink_breaker_state",
		Help:      "Circuit breaker state for the persistent hub sink: 0=closed, 1=half_open, 2=open.",
	})

	// runhubSinkBreakerTransitionsTotal counts every transition with
	// from/to labels. The combination of from/to lets a dashboard
	// distinguish a transient blip (closed→open→half_open→closed) from
	// a persistent outage (closed→open→...→open) without parsing
	// timestamps.
	runhubSinkBreakerTransitionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "sink_breaker_transitions_total",
		Help:      "Circuit breaker transitions on the persistent hub sink, by from→to.",
	}, []string{"from", "to"})

	// runhubSinkBreakerSkippedTotal counts every event whose batch
	// flush was suppressed by an Open breaker. Operators chart this
	// alongside events_persisted_total to size the durability impact
	// of an outage (the events still flow to live subscribers; only
	// reconnect-after-restart loses them).
	runhubSinkBreakerSkippedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "sink_breaker_skipped_total",
		Help:      "Events whose batch flush was suppressed by an Open sink breaker (durability deferred until breaker closes).",
	})

	// runhubBatchSizeFlushed reports the actual batch size at every
	// store-bound flush. Distribution shape tells operators whether the
	// configured BatchSize is being saturated (peak buckets dominant) or
	// the interval trigger is dominant (low buckets dominant); a tail
	// stuck at exactly BatchSize means the queue is consistently full
	// and BatchSize/BatchInterval need re-tuning.
	runhubBatchSizeFlushed = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "batch_size_flushed",
		Help:      "Distribution of envelope counts in each store-bound batch flush.",
		// Power-of-two buckets up to 1024 — matches the default
		// BatchBufferSize ceiling, so a configured BatchSize of 64
		// (default) leaves headroom on both sides.
		Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024},
	})

	// runhubBatchFlushDuration is the end-to-end flush latency: time
	// from the batchWriter starting flush() to its return. Includes
	// row-marshal, InsertEventsBatch, AND per-run NOTIFY de-dup loop.
	// Distinct from runhubSinkWriteDuration (which times only the
	// InsertEventsBatch call) — the gap between the two histograms
	// surfaces NOTIFY storms or unexpected marshal cost regressions.
	runhubBatchFlushDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "saker",
		Subsystem: "runhub",
		Name:      "batch_flush_seconds",
		Help:      "End-to-end batchWriter.flush wall-clock duration (marshal + insert + notify dedup).",
		// Same buckets as sink_write_seconds so dashboards can plot the
		// two side-by-side without a unit mismatch.
		Buckets: []float64{0.0001, 0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	})
)

// oversizedTenantCap bounds the cardinality of the runhub_oversized_events_total
// tenant label. Once we've seen this many distinct tenants, all new tenants
// roll up to the synthetic "_overflow" label so prometheus series count stays
// bounded under high-tenant or adversarial load.
const oversizedTenantCap = 256

func init() {
	prometheus.MustRegister(
		runhubEventsPersistedTotal,
		runhubListenersActive,
		runhubNotifyDroppedTotal,
		runhubBatchDropsTotal,
		runhubBatchQueueDepth,
		runhubRevivalsTotal,
		runhubSinkWriteDuration,
		runhubListenerReconnectsTotal,
		runhubOversizedEventsTotal,
		runhubSubscribersIdleEvictedTotal,
		runhubSinkBreakerState,
		runhubSinkBreakerTransitionsTotal,
		runhubSinkBreakerSkippedTotal,
		runhubBatchSizeFlushed,
		runhubBatchFlushDuration,
	)
	// Pre-instantiate every closed-enum label child so /metrics surfaces
	// the series at value 0 from process start. Without this prometheus
	// *Vec collectors are lazy: a label combination only appears in the
	// gather output after its first Inc/Add, which means dashboards built
	// against these names see "no data" until the first event occurs.
	// events_persisted_total now has a tenant label too — pre-instantiate
	// the overflow bucket for both result values so dashboards see four
	// fixed series (ok/_overflow, err/_overflow) from boot.
	runhubEventsPersistedTotal.WithLabelValues("ok", "_overflow").Add(0)
	runhubEventsPersistedTotal.WithLabelValues("err", "_overflow").Add(0)
	runhubListenerReconnectsTotal.WithLabelValues("ok").Add(0)
	runhubListenerReconnectsTotal.WithLabelValues("fail").Add(0)
	// Synthetic overflow bucket for the tenant label — pre-instantiate
	// so dashboards see it at zero from boot, signalling "the cap is
	// configured" rather than "no oversized events yet". Every
	// tenant-labeled vec uses the same labelTenantBounded helper so the
	// "_overflow" label name is shared across them all.
	runhubOversizedEventsTotal.WithLabelValues("_overflow").Add(0)
	runhubSubscribersIdleEvictedTotal.WithLabelValues("_overflow").Add(0)
	runhubNotifyDroppedTotal.WithLabelValues("_overflow").Add(0)
	runhubBatchDropsTotal.WithLabelValues("_overflow").Add(0)
	runhubRevivalsTotal.WithLabelValues("_overflow").Add(0)
	// Pre-instantiate every breaker transition pair so dashboards
	// surface the time-series at value 0 from boot. Self-loops
	// (closed→closed etc) are skipped because OnSinkBreakerTransition
	// is only emitted when state actually changes.
	for _, from := range []string{"closed", "half_open", "open"} {
		for _, to := range []string{"closed", "half_open", "open"} {
			if from == to {
				continue
			}
			runhubSinkBreakerTransitionsTotal.WithLabelValues(from, to).Add(0)
		}
	}
}

// oversizedTenantTracker enforces oversizedTenantCap on the
// runhub_oversized_events_total tenant label. Bounded by package-level
// state so it survives across hubs created by different tests / handlers.
var (
	oversizedTenantMu      sync.Mutex
	oversizedSeenTenants   = make(map[string]struct{}, oversizedTenantCap)
	oversizedTenantOverCap bool
)

// labelTenantBounded returns the tenant label to use, rolling up to
// "_overflow" once the cap is hit.
func labelTenantBounded(tenant string) string {
	if tenant == "" {
		tenant = "_unknown"
	}
	oversizedTenantMu.Lock()
	defer oversizedTenantMu.Unlock()
	if _, ok := oversizedSeenTenants[tenant]; ok {
		return tenant
	}
	if oversizedTenantOverCap {
		return "_overflow"
	}
	oversizedSeenTenants[tenant] = struct{}{}
	if len(oversizedSeenTenants) >= oversizedTenantCap {
		oversizedTenantOverCap = true
	}
	return tenant
}

// runhubMetrics adapts the prometheus collectors above to the
// runhub.MetricsHooks interface so PersistentHub can stay decoupled
// from the prometheus library.
type runhubMetrics struct{}

// NewRunhubMetricsHooks returns a MetricsHooks that publishes to the
// package-level prometheus collectors. Safe to call multiple times — all
// callers share the same collector instances.
func NewRunhubMetricsHooks() runhub.MetricsHooks { return runhubMetrics{} }

// OnEventPersist observes the histogram once per flush (so the duration
// distribution stays tenant-free and bounded), then bumps the counter
// once per envelope using its tenant label. tenants may be empty (e.g.
// drained-to-zero flush) — caller passes an empty slice and no counter
// moves.
func (runhubMetrics) OnEventPersist(success bool, dur time.Duration, tenants []string) {
	result := "ok"
	if !success {
		result = "err"
	}
	runhubSinkWriteDuration.Observe(dur.Seconds())
	for _, t := range tenants {
		runhubEventsPersistedTotal.WithLabelValues(result, labelTenantBounded(t)).Inc()
	}
}

func (runhubMetrics) OnListenerStart() { runhubListenersActive.Inc() }
func (runhubMetrics) OnListenerStop()  { runhubListenersActive.Dec() }

func (runhubMetrics) OnNotifyDropped(tenant string) {
	runhubNotifyDroppedTotal.WithLabelValues(labelTenantBounded(tenant)).Inc()
}

func (runhubMetrics) OnBatchDrop(tenant string) {
	runhubBatchDropsTotal.WithLabelValues(labelTenantBounded(tenant)).Inc()
}
func (runhubMetrics) OnBatchQueueDepth(depth int) { runhubBatchQueueDepth.Set(float64(depth)) }
func (runhubMetrics) OnRevival(tenant string) {
	runhubRevivalsTotal.WithLabelValues(labelTenantBounded(tenant)).Inc()
}
func (runhubMetrics) OnListenerReconnect(ok bool) {
	result := "ok"
	if !ok {
		result = "fail"
	}
	runhubListenerReconnectsTotal.WithLabelValues(result).Inc()
}

func (runhubMetrics) OnOversizedEvent(tenant string) {
	runhubOversizedEventsTotal.WithLabelValues(labelTenantBounded(tenant)).Inc()
}

func (runhubMetrics) OnSubscriberIdleEvicted(tenant string) {
	runhubSubscribersIdleEvictedTotal.WithLabelValues(labelTenantBounded(tenant)).Inc()
}

func (runhubMetrics) OnSinkBreakerState(state string) {
	switch state {
	case "closed":
		runhubSinkBreakerState.Set(0)
	case "half_open":
		runhubSinkBreakerState.Set(1)
	case "open":
		runhubSinkBreakerState.Set(2)
	}
}

func (runhubMetrics) OnSinkBreakerTransition(from, to string) {
	runhubSinkBreakerTransitionsTotal.WithLabelValues(from, to).Inc()
}

func (runhubMetrics) OnSinkBreakerSkipped(rows int) {
	if rows <= 0 {
		return
	}
	runhubSinkBreakerSkippedTotal.Add(float64(rows))
}

func (runhubMetrics) OnBatchFlush(size int, dur time.Duration) {
	if size > 0 {
		runhubBatchSizeFlushed.Observe(float64(size))
	}
	runhubBatchFlushDuration.Observe(dur.Seconds())
}
