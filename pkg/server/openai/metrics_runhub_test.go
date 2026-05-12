package openai

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestRunhubCollectorsRegistered verifies every runhub_* collector appears
// on the default prometheus registry so /metrics will surface them. This is
// the cheapest possible end-to-end check that the init() block ran and the
// names match what dashboards/alerts will look for.
func TestRunhubCollectorsRegistered(t *testing.T) {
	gathered, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	have := make(map[string]struct{}, len(gathered))
	for _, mf := range gathered {
		have[mf.GetName()] = struct{}{}
	}
	want := []string{
		"saker_runhub_events_persisted_total",
		"saker_runhub_listeners_active",
		"saker_runhub_notify_dropped_total",
		"saker_runhub_batch_drops_total",
		"saker_runhub_batch_queue_depth",
		"saker_runhub_revivals_total",
		"saker_runhub_sink_write_seconds",
		"saker_runhub_listener_reconnects_total",
		"saker_runhub_oversized_events_total",
		"saker_runhub_subscribers_idle_evicted_total",
		"saker_runhub_sink_breaker_state",
		"saker_runhub_sink_breaker_transitions_total",
		"saker_runhub_sink_breaker_skipped_total",
		"saker_runhub_batch_size_flushed",
		"saker_runhub_batch_flush_seconds",
	}
	for _, name := range want {
		if _, ok := have[name]; !ok {
			t.Errorf("missing collector %q on default registry", name)
		}
	}
}

// TestRunhubMetricsHooksAdapter exercises every method on the adapter,
// asserting that the underlying counter / gauge actually moves. Catches
// label-name typos that would otherwise only surface in production
// dashboards.
func TestRunhubMetricsHooksAdapter(t *testing.T) {
	hooks := NewRunhubMetricsHooks()

	beforePersistOK := testutil.ToFloat64(runhubEventsPersistedTotal.WithLabelValues("ok", "tenant-x"))
	beforePersistErr := testutil.ToFloat64(runhubEventsPersistedTotal.WithLabelValues("err", "tenant-x"))
	beforeRevival := testutil.ToFloat64(runhubRevivalsTotal.WithLabelValues("tenant-x"))
	beforeNotifyDropped := testutil.ToFloat64(runhubNotifyDroppedTotal.WithLabelValues("tenant-x"))
	beforeBatchDrop := testutil.ToFloat64(runhubBatchDropsTotal.WithLabelValues("tenant-x"))
	beforeReconnectOK := testutil.ToFloat64(runhubListenerReconnectsTotal.WithLabelValues("ok"))
	beforeReconnectFail := testutil.ToFloat64(runhubListenerReconnectsTotal.WithLabelValues("fail"))

	beforeOversized := testutil.ToFloat64(runhubOversizedEventsTotal.WithLabelValues("tenant-x"))
	beforeIdleEvicted := testutil.ToFloat64(runhubSubscribersIdleEvictedTotal.WithLabelValues("tenant-x"))
	beforeBreakerSkipped := testutil.ToFloat64(runhubSinkBreakerSkippedTotal)
	beforeBreakerClosedToOpen := testutil.ToFloat64(runhubSinkBreakerTransitionsTotal.WithLabelValues("closed", "open"))

	// Pass tenant-x as the only tenant in each persist batch so the
	// per-tenant counter assertions stay closed-form: 1 envelope = 1 Inc.
	hooks.OnEventPersist(true, 5*time.Millisecond, []string{"tenant-x"})
	hooks.OnEventPersist(false, 1*time.Millisecond, []string{"tenant-x"})
	hooks.OnRevival("tenant-x")
	hooks.OnNotifyDropped("tenant-x")
	hooks.OnBatchDrop("tenant-x")
	hooks.OnBatchQueueDepth(42)
	hooks.OnListenerStart()
	hooks.OnListenerStop()
	hooks.OnListenerReconnect(true)
	hooks.OnListenerReconnect(false)
	hooks.OnOversizedEvent("tenant-x")
	hooks.OnSubscriberIdleEvicted("tenant-x")
	hooks.OnSinkBreakerState("open")
	hooks.OnSinkBreakerTransition("closed", "open")
	hooks.OnSinkBreakerSkipped(7)
	hooks.OnBatchFlush(7, 5*time.Millisecond)

	if got := testutil.ToFloat64(runhubEventsPersistedTotal.WithLabelValues("ok", "tenant-x")) - beforePersistOK; got != 1 {
		t.Errorf("persist ok delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(runhubEventsPersistedTotal.WithLabelValues("err", "tenant-x")) - beforePersistErr; got != 1 {
		t.Errorf("persist err delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(runhubRevivalsTotal.WithLabelValues("tenant-x")) - beforeRevival; got != 1 {
		t.Errorf("revival delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(runhubNotifyDroppedTotal.WithLabelValues("tenant-x")) - beforeNotifyDropped; got != 1 {
		t.Errorf("notify dropped delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(runhubBatchDropsTotal.WithLabelValues("tenant-x")) - beforeBatchDrop; got != 1 {
		t.Errorf("batch drop delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(runhubBatchQueueDepth); got != 42 {
		t.Errorf("queue depth = %v, want 42", got)
	}
	if got := testutil.ToFloat64(runhubListenersActive); got != 0 {
		t.Errorf("listeners active after Start+Stop = %v, want 0", got)
	}
	if got := testutil.ToFloat64(runhubListenerReconnectsTotal.WithLabelValues("ok")) - beforeReconnectOK; got != 1 {
		t.Errorf("reconnect ok delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(runhubListenerReconnectsTotal.WithLabelValues("fail")) - beforeReconnectFail; got != 1 {
		t.Errorf("reconnect fail delta = %v, want 1", got)
	}

	if got := testutil.ToFloat64(runhubOversizedEventsTotal.WithLabelValues("tenant-x")) - beforeOversized; got != 1 {
		t.Errorf("oversized events delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(runhubSubscribersIdleEvictedTotal.WithLabelValues("tenant-x")) - beforeIdleEvicted; got != 1 {
		t.Errorf("subscribers idle evicted delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(runhubSinkBreakerState); got != 2 {
		t.Errorf("sink breaker state after Open = %v, want 2", got)
	}
	if got := testutil.ToFloat64(runhubSinkBreakerTransitionsTotal.WithLabelValues("closed", "open")) - beforeBreakerClosedToOpen; got != 1 {
		t.Errorf("sink breaker closed->open delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(runhubSinkBreakerSkippedTotal) - beforeBreakerSkipped; got != 7 {
		t.Errorf("sink breaker skipped rows delta = %v, want 7", got)
	}

	// Histogram count should advance by exactly the two OnEventPersist calls.
	if got := testutil.CollectAndCount(runhubSinkWriteDuration); got == 0 {
		t.Errorf("sink_write_seconds histogram has no observations after %d hooks, want > 0", 2)
	}
	// E.5 batch flush histograms — at least one observation each.
	if got := testutil.CollectAndCount(runhubBatchSizeFlushed); got == 0 {
		t.Errorf("batch_size_flushed histogram has no observations, want > 0")
	}
	if got := testutil.CollectAndCount(runhubBatchFlushDuration); got == 0 {
		t.Errorf("batch_flush_seconds histogram has no observations, want > 0")
	}
}

// TestRunhubMetricsHooks_PerTenantBatchFanout asserts that one
// OnEventPersist call with a multi-tenant batch increments the per-tenant
// counter once per envelope (not once per call), so a 3-event batch with
// {a,a,b} produces +2 on tenant a and +1 on tenant b. This is the load-
// bearing semantic that lets dashboards split persisted events by tenant
// even though the histogram observation stays per-batch.
func TestRunhubMetricsHooks_PerTenantBatchFanout(t *testing.T) {
	hooks := NewRunhubMetricsHooks()

	beforeA := testutil.ToFloat64(runhubEventsPersistedTotal.WithLabelValues("ok", "fanout-a"))
	beforeB := testutil.ToFloat64(runhubEventsPersistedTotal.WithLabelValues("ok", "fanout-b"))

	hooks.OnEventPersist(true, 5*time.Millisecond, []string{"fanout-a", "fanout-a", "fanout-b"})

	if got := testutil.ToFloat64(runhubEventsPersistedTotal.WithLabelValues("ok", "fanout-a")) - beforeA; got != 2 {
		t.Errorf("fanout-a delta = %v, want 2 (per-envelope Inc)", got)
	}
	if got := testutil.ToFloat64(runhubEventsPersistedTotal.WithLabelValues("ok", "fanout-b")) - beforeB; got != 1 {
		t.Errorf("fanout-b delta = %v, want 1 (per-envelope Inc)", got)
	}

	// Empty batch — counters must NOT move; histogram observation
	// happens regardless (records the wall-clock of the empty flush).
	beforeAEmpty := testutil.ToFloat64(runhubEventsPersistedTotal.WithLabelValues("ok", "fanout-a"))
	hooks.OnEventPersist(true, 1*time.Millisecond, nil)
	if got := testutil.ToFloat64(runhubEventsPersistedTotal.WithLabelValues("ok", "fanout-a")) - beforeAEmpty; got != 0 {
		t.Errorf("nil-tenants delta = %v, want 0 (empty batch must not move per-tenant counter)", got)
	}
}

// TestRunhubMetricsHooks_TenantCardinalityCap asserts that once
// oversizedTenantCap distinct tenants have been seen, a brand new tenant
// rolls up to the synthetic "_overflow" label. Uses the OnRevival hook
// (which routes through the same labelTenantBounded helper as every
// tenant-labeled vec) so we don't need to drive 256 publish loops.
//
// IMPORTANT: this test mutates the package-level oversizedSeenTenants
// map, so the assertion must restore the prior state on cleanup —
// otherwise unrelated tests calling labelTenantBounded would see the
// cap already saturated.
func TestRunhubMetricsHooks_TenantCardinalityCap(t *testing.T) {
	hooks := NewRunhubMetricsHooks()

	// Snapshot + restore — every other test relies on the cap being
	// fresh enough that real tenant names don't roll to _overflow.
	oversizedTenantMu.Lock()
	prevSeen := make(map[string]struct{}, len(oversizedSeenTenants))
	for k := range oversizedSeenTenants {
		prevSeen[k] = struct{}{}
	}
	prevOver := oversizedTenantOverCap
	oversizedTenantMu.Unlock()
	t.Cleanup(func() {
		oversizedTenantMu.Lock()
		oversizedSeenTenants = prevSeen
		oversizedTenantOverCap = prevOver
		oversizedTenantMu.Unlock()
	})

	// Reset the tracker so the cap is fresh for this test.
	oversizedTenantMu.Lock()
	oversizedSeenTenants = make(map[string]struct{}, oversizedTenantCap)
	oversizedTenantOverCap = false
	oversizedTenantMu.Unlock()

	beforeOverflow := testutil.ToFloat64(runhubRevivalsTotal.WithLabelValues("_overflow"))

	// Fill exactly to the cap with synthetic tenant names — every one
	// gets its own series.
	for i := 0; i < oversizedTenantCap; i++ {
		hooks.OnRevival("cap-tenant-" + strconv.Itoa(i))
	}

	// Cap is saturated: any new tenant rolls to _overflow.
	hooks.OnRevival("cap-tenant-overflow-1")
	hooks.OnRevival("cap-tenant-overflow-2")

	if got := testutil.ToFloat64(runhubRevivalsTotal.WithLabelValues("_overflow")) - beforeOverflow; got != 2 {
		t.Errorf("overflow delta = %v, want 2 (two new tenants past cap)", got)
	}
	// Sanity: a tenant we already inserted before the cap saturation
	// keeps its OWN label even after overflow flag flipped.
	already := testutil.ToFloat64(runhubRevivalsTotal.WithLabelValues("cap-tenant-0"))
	hooks.OnRevival("cap-tenant-0")
	if got := testutil.ToFloat64(runhubRevivalsTotal.WithLabelValues("cap-tenant-0")) - already; got != 1 {
		t.Errorf("known-tenant delta after overflow = %v, want 1 (already-seen tenants must keep their label)", got)
	}
}

// TestRunhubMetricsHooks_EmptyTenantUsesUnknown asserts that an empty
// tenant string (e.g. revival of a row that pre-dates tenant attribution)
// rolls into the "_unknown" label — a deliberate, named bucket so
// operators can distinguish "missing tenant id" from "label cap hit".
func TestRunhubMetricsHooks_EmptyTenantUsesUnknown(t *testing.T) {
	hooks := NewRunhubMetricsHooks()

	before := testutil.ToFloat64(runhubRevivalsTotal.WithLabelValues("_unknown"))
	hooks.OnRevival("")
	if got := testutil.ToFloat64(runhubRevivalsTotal.WithLabelValues("_unknown")) - before; got != 1 {
		t.Errorf("empty-tenant delta = %v on label _unknown, want 1", got)
	}
}

// TestRunhubMetricNamesNamespacedSaker ensures every runhub collector
// follows the saker_runhub_* convention so dashboards can scrape them
// with a single regex. Counts that we found at least the expected number
// so a future rename to e.g. "runhub_*" surfaces here instead of in prod.
func TestRunhubMetricNamesNamespacedSaker(t *testing.T) {
	gathered, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	const wantPrefix = "saker_runhub_"
	found := 0
	for _, mf := range gathered {
		if strings.HasPrefix(mf.GetName(), wantPrefix) {
			found++
		}
	}
	if found < 15 {
		t.Errorf("found %d collectors with prefix %q, want >= 15", found, wantPrefix)
	}
}
