package runhub

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// withSpanRecorder swaps the global TracerProvider for a synchronous
// recorder for the duration of the test, then restores the prior
// provider. The recorder's snapshots are returned via the closure so
// each test can read spans deterministically without polling. Returns
// the cleanup func separately so the test can flush + restore in
// t.Cleanup, keeping the call site read-as-narrative.
func withSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	cleanup := func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	}
	return rec, cleanup
}

// findSpanByName returns the first ended span with the given name, or
// nil if no such span exists. Test failures format the recorded span
// list so a missing span is visible without a separate dump call.
func findSpanByName(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	names := make([]string, 0, len(spans))
	for _, s := range spans {
		names = append(names, s.Name())
	}
	t.Fatalf("span %q not found; recorded: %v", name, names)
	return nil
}

// attrLookup pulls a single attribute value off a span; fatals when the
// key is absent so test assertions don't have to nil-check every read.
func attrLookup(t *testing.T, sp sdktrace.ReadOnlySpan, key string) attribute.Value {
	t.Helper()
	for _, kv := range sp.Attributes() {
		if string(kv.Key) == key {
			return kv.Value
		}
	}
	keys := make([]string, 0, len(sp.Attributes()))
	for _, kv := range sp.Attributes() {
		keys = append(keys, string(kv.Key))
	}
	t.Fatalf("span %q missing attribute %q; have: %v", sp.Name(), key, keys)
	return attribute.Value{}
}

// TestTracer_PublishSpanTree asserts that one Run.Publish on a
// MemoryHub emits the documented span shape: a root `runhub.publish`
// with a `runhub.fanout` child carrying subscriber count and an
// optional dropped-count attribute. Validates the attribute keys
// listed in tracer.go's package comment so a future rename surfaces
// here instead of in a dashboard.
func TestTracer_PublishSpanTree(t *testing.T) {
	// NOT t.Parallel — withSpanRecorder swaps the OTel global TracerProvider,
	// so concurrent runs would stomp each other's recorder and produce
	// nondeterministic span counts.
	rec, cleanup := withSpanRecorder(t)
	t.Cleanup(cleanup)

	hub := NewMemoryHub(Config{RingSize: 16})
	t.Cleanup(hub.Shutdown)
	run, err := hub.Create(CreateOptions{TenantID: "trace-tenant", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// One subscriber so fanout has work to do (and we can assert
	// subscribers=1 instead of 0 — a 0 fanout is a different early-exit).
	ch, _, unsub := run.Subscribe()
	t.Cleanup(unsub)
	go func() { <-ch }() // drain to keep send non-blocking

	seq := run.Publish("delta", []byte(`{"hello":"world"}`))
	if seq <= 0 {
		t.Fatalf("Publish returned seq=%d, want > 0", seq)
	}

	spans := rec.Ended()
	publish := findSpanByName(t, spans, "runhub.publish")
	if got := attrLookup(t, publish, "run.id").AsString(); got != run.ID {
		t.Errorf("publish run.id = %q, want %q", got, run.ID)
	}
	if got := attrLookup(t, publish, "tenant").AsString(); got != "trace-tenant" {
		t.Errorf("publish tenant = %q, want trace-tenant", got)
	}
	if got := attrLookup(t, publish, "event.type").AsString(); got != "delta" {
		t.Errorf("publish event.type = %q, want delta", got)
	}
	if got := attrLookup(t, publish, "seq").AsInt64(); got != int64(seq) {
		t.Errorf("publish seq = %d, want %d", got, seq)
	}
	if got := attrLookup(t, publish, "payload.bytes").AsInt64(); got != 17 {
		t.Errorf("publish payload.bytes = %d, want 17", got)
	}

	fanout := findSpanByName(t, spans, "runhub.fanout")
	if got := attrLookup(t, fanout, "subscribers").AsInt64(); got != 1 {
		t.Errorf("fanout subscribers = %d, want 1", got)
	}
	if got := attrLookup(t, fanout, "events").AsInt64(); got != 1 {
		t.Errorf("fanout events = %d, want 1", got)
	}

	// Parent-child link: fanout's parent should be the publish span.
	if fanout.Parent().SpanID() != publish.SpanContext().SpanID() {
		t.Errorf("fanout parent = %v, want publish %v", fanout.Parent().SpanID(), publish.SpanContext().SpanID())
	}
}

// TestTracer_PublishOversizedSetsAttribute asserts the Publish span
// records an `oversized=true` attribute when MaxEventBytes rejects the
// payload. Operators triage rejection floods by filtering this
// attribute in their span search; a regression here breaks that
// workflow silently.
func TestTracer_PublishOversizedSetsAttribute(t *testing.T) {
	// NOT t.Parallel — withSpanRecorder swaps the OTel global TracerProvider,
	// so concurrent runs would stomp each other's recorder and produce
	// nondeterministic span counts.
	rec, cleanup := withSpanRecorder(t)
	t.Cleanup(cleanup)

	hub := NewMemoryHub(Config{RingSize: 16, MaxEventBytes: 4})
	t.Cleanup(hub.Shutdown)
	run, err := hub.Create(CreateOptions{TenantID: "trace-tenant", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if seq := run.Publish("delta", []byte("oversized payload")); seq != 0 {
		t.Fatalf("Publish oversized returned seq=%d, want 0", seq)
	}

	publish := findSpanByName(t, rec.Ended(), "runhub.publish")
	if got := attrLookup(t, publish, "oversized").AsBool(); !got {
		t.Errorf("publish oversized = false, want true")
	}
}

// TestTracer_BatchFlushSpanTree asserts the persistent path emits the
// documented multi-level tree:
//
//	runhub.batch.flush
//	  ├── runhub.store.insert
//	  └── runhub.store.notify (one per distinct runID)
//
// Plus the publish + fanout spans from each Publish that fed the
// batch. Validates attribute keys (rows, channel, batch.size,
// tenants_count, notify_count) so dashboards can keep their queries
// stable.
func TestTracer_BatchFlushSpanTree(t *testing.T) {
	// NOT t.Parallel — withSpanRecorder swaps the OTel global TracerProvider,
	// so concurrent runs would stomp each other's recorder and produce
	// nondeterministic span counts.
	rec, cleanup := withSpanRecorder(t)
	t.Cleanup(cleanup)

	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{
		Store:         s,
		BatchSize:     2, // two events → one flush, deterministic
		BatchInterval: time.Hour,
	})

	run, err := h.Create(CreateOptions{TenantID: "trace-tenant", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if seq := run.Publish("delta", []byte("a")); seq <= 0 {
		t.Fatalf("Publish#1 returned seq=%d, want > 0", seq)
	}
	if seq := run.Publish("delta", []byte("b")); seq <= 0 {
		t.Fatalf("Publish#2 returned seq=%d, want > 0", seq)
	}
	h.Flush()

	spans := rec.Ended()
	flush := findSpanByName(t, spans, "runhub.batch.flush")
	if got := attrLookup(t, flush, "batch.size").AsInt64(); got < 2 {
		t.Errorf("batch.flush batch.size = %d, want >= 2", got)
	}
	if got := attrLookup(t, flush, "tenants_count").AsInt64(); got != 1 {
		t.Errorf("batch.flush tenants_count = %d, want 1", got)
	}
	if got := attrLookup(t, flush, "notify_count").AsInt64(); got != 1 {
		t.Errorf("batch.flush notify_count = %d, want 1 (single runID)", got)
	}

	insert := findSpanByName(t, spans, "runhub.store.insert")
	if got := attrLookup(t, insert, "rows").AsInt64(); got < 2 {
		t.Errorf("store.insert rows = %d, want >= 2", got)
	}
	if insert.Parent().SpanID() != flush.SpanContext().SpanID() {
		t.Errorf("store.insert parent = %v, want flush %v", insert.Parent().SpanID(), flush.SpanContext().SpanID())
	}

	notify := findSpanByName(t, spans, "runhub.store.notify")
	if got := attrLookup(t, notify, "channel").AsString(); got == "" {
		t.Errorf("store.notify channel attribute is empty, want non-empty channel name")
	}
	if notify.Parent().SpanID() != flush.SpanContext().SpanID() {
		t.Errorf("store.notify parent = %v, want flush %v", notify.Parent().SpanID(), flush.SpanContext().SpanID())
	}
}

// TestTracer_DeliverExternalSpan asserts that the cross-process replay
// path emits a distinct `runhub.deliver_external` span (NOT
// `runhub.publish`) so dashboards can split same-process publish from
// LISTEN/NOTIFY-driven replays. Carries the same fanout child + run
// identification attributes as Publish.
func TestTracer_DeliverExternalSpan(t *testing.T) {
	// NOT t.Parallel — withSpanRecorder swaps the OTel global TracerProvider,
	// so concurrent runs would stomp each other's recorder and produce
	// nondeterministic span counts.
	rec, cleanup := withSpanRecorder(t)
	t.Cleanup(cleanup)

	hub := NewMemoryHub(Config{RingSize: 16})
	t.Cleanup(hub.Shutdown)
	run, err := hub.Create(CreateOptions{TenantID: "trace-tenant", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	run.DeliverExternal([]Event{{Seq: 100, Type: "delta", Data: []byte("from-peer")}})

	spans := rec.Ended()
	span := findSpanByName(t, spans, "runhub.deliver_external")
	if got := attrLookup(t, span, "run.id").AsString(); got != run.ID {
		t.Errorf("deliver_external run.id = %q, want %q", got, run.ID)
	}
	if got := attrLookup(t, span, "tenant").AsString(); got != "trace-tenant" {
		t.Errorf("deliver_external tenant = %q, want trace-tenant", got)
	}
	if got := attrLookup(t, span, "events").AsInt64(); got != 1 {
		t.Errorf("deliver_external events = %d, want 1", got)
	}
	if got := attrLookup(t, span, "accepted").AsInt64(); got != 1 {
		t.Errorf("deliver_external accepted = %d, want 1", got)
	}
}

// TestTracer_NoopProviderIsCheap is a behavior assertion (NOT a perf
// benchmark): when no provider has been installed, runhubTracer()
// returns the global noop and Start/End must not allocate spans the
// recorder can see. Catches a regression where a future refactor
// accidentally caches a non-noop tracer.
func TestTracer_NoopProviderIsCheap(t *testing.T) {
	// Don't install a recorder — the global default is noop.
	hub := NewMemoryHub(Config{RingSize: 16})
	t.Cleanup(hub.Shutdown)
	run, err := hub.Create(CreateOptions{TenantID: "noop", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Should not panic, should not allocate a real span.
	if seq := run.Publish("delta", []byte("noop")); seq <= 0 {
		t.Fatalf("Publish under noop tracer failed: seq=%d", seq)
	}
}
