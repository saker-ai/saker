package runhub

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// RunStatus mirrors the OpenAI Assistants run-state machine, keeping a
// single vocabulary across the gateway, the hub, and the persisted
// run row (P1).
type RunStatus string

const (
	RunStatusQueued         RunStatus = "queued"
	RunStatusInProgress     RunStatus = "in_progress"
	RunStatusRequiresAction RunStatus = "requires_action"
	RunStatusCompleted      RunStatus = "completed"
	RunStatusCancelling     RunStatus = "cancelling"
	RunStatusCancelled      RunStatus = "cancelled"
	RunStatusFailed         RunStatus = "failed"
	RunStatusExpired        RunStatus = "expired"
)

// Run carries the in-memory state of a single agent execution. One Run
// per OpenAI request; the same Run is fanned out to multiple SSE
// subscribers (initial POST + any reconnect with Last-Event-ID).
type Run struct {
	// ID is the OpenAI-style run id (e.g. "run_<hex>"). Unique within
	// the hub; the gateway emits it as the SSE id: line.
	ID string

	// SessionID is the saker session to attach the underlying agent run to.
	// Empty means a brand new session is created when the run starts.
	SessionID string

	// TenantID is the Bearer key tenant the run belongs to. Used by the
	// hub for per-tenant caps and authorization on reconnect.
	TenantID string

	// Cancel is the goroutine-side cancel for the run's underlying
	// context. The hub calls it from Cancel/Expire/Shutdown paths.
	Cancel context.CancelFunc

	// CreatedAt is when the run was registered with the hub.
	CreatedAt time.Time

	// ExpiresAt is the absolute deadline after which the GC will mark
	// the run expired. Zero means no expiry (rare — caller should always
	// pass one).
	ExpiresAt time.Time

	mu          sync.Mutex
	status      RunStatus
	ring        []Event
	ringHead    int  // next write slot; ring is implicitly a circular buffer
	ringFull    bool // true once we've wrapped at least once
	nextSeq     int
	subscribers []*subscriber
	closed      bool

	// sink is the optional persistence hook installed by PersistentHub.
	// MemoryHub leaves it nil; Publish / SnapshotSince treat nil as
	// "in-memory only" so the MemoryHub fast path stays untouched.
	sink eventSink

	// maxEventBytes is the per-event payload cap inherited from
	// Config.MaxEventBytes. Zero = unbounded. Read-only after construction
	// so it doesn't need to live under r.mu.
	maxEventBytes int64
	// metrics is the hook used to report oversized-event rejections.
	// Always non-nil (NopMetricsHooks fallback applied at hub construction).
	metrics MetricsHooks
	// logger is the per-run slog used for size-cap rejections + future
	// diagnostic events. Always non-nil (slog.Default fallback at hub).
	logger *slog.Logger
}

// eventSink is the persistence hook attached to a Run by PersistentHub.
// All methods are best-effort from Run's perspective: errors are logged
// inside the sink implementation and never propagate back to the producer
// or to subscribers.
//
// The PG-backed implementation extends this interface (see Stage 5) with
// a notify() method for cross-process LISTEN/NOTIFY fan-out.
type eventSink interface {
	// write persists one event. Called from Publish BEFORE fan-out so
	// subscribers can never observe an event that hasn't been stored.
	// tenantID is forwarded from Run.TenantID so the persistence layer
	// can attribute per-tenant counters (events_persisted_total,
	// batch_drops_total) without a hub-map lookup on the hot path.
	write(ctx context.Context, runID, tenantID string, e Event) error
	// loadSince returns persisted events with Seq > sinceSeq, oldest →
	// newest. Used by SnapshotSince when the ring has aged out (or the
	// run was revived from disk after a process restart).
	loadSince(ctx context.Context, runID string, sinceSeq int) ([]Event, error)
}

// attachSink installs the persistence hook. Called by PersistentHub
// immediately after Create. Safe to call before any Publish; not safe to
// swap after the run has started taking traffic.
func (r *Run) attachSink(s eventSink) {
	r.mu.Lock()
	r.sink = s
	r.mu.Unlock()
}

// newRun builds a Run with the supplied identity and capacity. Caller is
// responsible for storing it into the hub's map. metrics and logger must
// be non-nil — both hubs apply NopMetricsHooks / slog.Default fallbacks
// in their constructors.
func newRun(id, sessionID, tenantID string, cancel context.CancelFunc, ringSize int, expiresAt time.Time, maxEventBytes int64, metrics MetricsHooks, logger *slog.Logger) *Run {
	if ringSize <= 0 {
		ringSize = 512
	}
	if metrics == nil {
		metrics = NopMetricsHooks()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Run{
		ID:            id,
		SessionID:     sessionID,
		TenantID:      tenantID,
		Cancel:        cancel,
		CreatedAt:     time.Now(),
		ExpiresAt:     expiresAt,
		status:        RunStatusQueued,
		ring:          make([]Event, ringSize),
		nextSeq:       1,
		maxEventBytes: maxEventBytes,
		metrics:       metrics,
		logger:        logger,
	}
}

// Status returns the current run status. Safe to call concurrently.
func (r *Run) Status() RunStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// SetStatus updates the run status. Safe to call concurrently.
func (r *Run) SetStatus(s RunStatus) {
	r.mu.Lock()
	r.status = s
	r.mu.Unlock()
}

// IsTerminal reports whether the run has reached a terminal status.
func (r *Run) IsTerminal() bool {
	switch r.Status() {
	case RunStatusCompleted, RunStatusCancelled, RunStatusFailed, RunStatusExpired:
		return true
	}
	return false
}

// Publish appends an event to the per-run ring buffer (assigning Seq) and
// fans it out to every subscriber. Slow subscribers (full chan) drop the
// event rather than block the producer — the hub never blocks on a slow
// HTTP client.
//
// When a sink is attached (PersistentHub), the event is written to the
// sink BEFORE fan-out so subscribers never observe an event that the
// underlying store hasn't received. The sink is best-effort: errors and
// timeouts are absorbed inside the sink (logging is the sink's job) so a
// stalled DB can never wedge streaming.
//
// Returns the assigned Seq so the caller can tag follow-up state with it.
// Returns 0 (no seq assigned) when the run is closed OR the payload exceeds
// Config.MaxEventBytes (oversized event is logged + counted in metrics).
func (r *Run) Publish(typ string, data []byte) int {
	// Span: runhub.publish. Parent context not threaded through here
	// (Run.Publish is called from agent goroutines that own no caller
	// ctx); using context.Background as the parent makes this a root
	// span unless a future caller adopts the context-passing pattern.
	// Cheap when no provider — global noop.
	ctx, span := runhubTracer().Start(context.Background(), "runhub.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("run.id", r.ID),
			attribute.String("tenant", r.TenantID),
			attribute.String("event.type", typ),
			attribute.Int("payload.bytes", len(data)),
		),
	)
	defer span.End()

	if r.maxEventBytes > 0 && int64(len(data)) > r.maxEventBytes {
		r.metrics.OnOversizedEvent(r.TenantID)
		r.logger.Warn("runhub: rejecting oversized event",
			"run_id", r.ID,
			"tenant", r.TenantID,
			"event_type", typ,
			"size_bytes", len(data),
			"cap_bytes", r.maxEventBytes,
		)
		span.SetAttributes(attribute.Bool("oversized", true))
		return 0
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		span.SetAttributes(attribute.Bool("closed", true))
		return 0
	}
	seq := r.nextSeq
	r.nextSeq++
	evt := Event{Seq: seq, Type: typ, Data: data}
	span.SetAttributes(attribute.Int("seq", seq))

	// Ring write — overwrite oldest when full.
	r.ring[r.ringHead] = evt
	r.ringHead++
	if r.ringHead >= len(r.ring) {
		r.ringHead = 0
		r.ringFull = true
	}

	// Snapshot subscribers + sink under the lock; deliver outside the lock
	// so a slow Send (or DB write) can't deadlock with a concurrent
	// Subscribe/Unsubscribe.
	subs := make([]*subscriber, len(r.subscribers))
	copy(subs, r.subscribers)
	sink := r.sink
	r.mu.Unlock()

	if sink != nil {
		sctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_ = sink.write(sctx, r.ID, r.TenantID, evt)
		cancel()
	}

	r.fanout(ctx, subs, []Event{evt})
	return seq
}

// fanout delivers each event to every snapshotted subscriber. Slow
// subscribers (full chan) drop the event rather than block — recovery is
// the client's responsibility via Last-Event-ID replay. Emits a
// runhub.fanout span so the publish→fan-out cost split is visible in
// Jaeger / Tempo (subscribers + dropped attributes let operators
// size the impact of a stuck client without per-subscriber tracing).
func (r *Run) fanout(ctx context.Context, subs []*subscriber, events []Event) {
	if len(subs) == 0 || len(events) == 0 {
		return
	}
	_, span := runhubTracer().Start(ctx, "runhub.fanout",
		trace.WithAttributes(
			attribute.Int("subscribers", len(subs)),
			attribute.Int("events", len(events)),
		),
	)
	defer span.End()

	dropped := 0
	nowNano := time.Now().UnixNano()
	for _, evt := range events {
		for _, s := range subs {
			select {
			case s.ch <- evt:
				// Bump the subscriber's "last delivery" mark so the GC
				// sweeper's idle-eviction pass doesn't evict an actively-
				// served subscriber. Updated only on successful send: a
				// subscriber whose chan is full counts as idle from the
				// hub's perspective and will eventually be evicted.
				s.lastReadAt.Store(nowNano)
			default:
				s.dropped.Add(1)
				dropped++
			}
		}
	}
	if dropped > 0 {
		span.SetAttributes(attribute.Int("dropped", dropped))
	}
}

// DeliverExternal injects events that originated in another process (via
// PG LISTEN/NOTIFY) into the local ring + subscriber fan-out WITHOUT
// re-running the sink-write side of Publish. Used by PersistentHub's
// listener goroutine on revived shells: the events came FROM the store,
// re-writing them would be wasteful (and would re-NOTIFY).
//
// Events with Seq < nextSeq are silently dropped — a shell that races
// a same-process Publish wouldn't replay history. Events bump nextSeq
// past their seq so any rare local Publish on this shell doesn't
// collide with a persisted seq.
func (r *Run) DeliverExternal(events []Event) {
	if len(events) == 0 {
		return
	}
	// Span: runhub.deliver_external. Distinct name from runhub.publish so
	// dashboards can split same-process Publish from cross-process replays
	// driven by LISTEN/NOTIFY (a sudden spike here is a sibling process,
	// not a misbehaving local agent).
	ctx, span := runhubTracer().Start(context.Background(), "runhub.deliver_external",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("run.id", r.ID),
			attribute.String("tenant", r.TenantID),
			attribute.Int("events", len(events)),
		),
	)
	defer span.End()

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		span.SetAttributes(attribute.Bool("closed", true))
		return
	}
	accepted := make([]Event, 0, len(events))
	for _, e := range events {
		if e.Seq < r.nextSeq {
			continue
		}
		// Apply the same size cap on the cross-process path so a
		// misbehaving peer can't bypass MaxEventBytes by routing through
		// LISTEN/NOTIFY. Counted as an oversized event for the run's
		// tenant; the peer's slog already logged the upstream rejection.
		if r.maxEventBytes > 0 && int64(len(e.Data)) > r.maxEventBytes {
			r.metrics.OnOversizedEvent(r.TenantID)
			r.logger.Warn("runhub: rejecting oversized external event",
				"run_id", r.ID,
				"tenant", r.TenantID,
				"event_type", e.Type,
				"seq", e.Seq,
				"size_bytes", len(e.Data),
				"cap_bytes", r.maxEventBytes,
			)
			continue
		}
		r.ring[r.ringHead] = e
		r.ringHead++
		if r.ringHead >= len(r.ring) {
			r.ringHead = 0
			r.ringFull = true
		}
		r.nextSeq = e.Seq + 1
		accepted = append(accepted, e)
	}
	subs := make([]*subscriber, len(r.subscribers))
	copy(subs, r.subscribers)
	r.mu.Unlock()

	span.SetAttributes(attribute.Int("accepted", len(accepted)))
	r.fanout(ctx, subs, accepted)
}

// Snapshot returns a copy of the events currently held in the ring,
// ordered oldest → newest. Used by Subscribe to backfill new clients.
func (r *Run) Snapshot() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}

func (r *Run) snapshotLocked() []Event {
	if !r.ringFull {
		out := make([]Event, r.ringHead)
		copy(out, r.ring[:r.ringHead])
		return out
	}
	out := make([]Event, 0, len(r.ring))
	out = append(out, r.ring[r.ringHead:]...)
	out = append(out, r.ring[:r.ringHead]...)
	return out
}

// SnapshotSince returns the events with Seq strictly greater than the
// supplied sequence, oldest → newest. Used by Last-Event-ID reconnect.
//
// If the requested sequence has aged out of the ring AND no sink is
// attached, returns (nil, false) so the caller can decide whether to
// error or replay-from-zero. With a sink attached (PersistentHub), the
// missing prefix is loaded from the persistent store — restoring the
// "ring miss → 410" path to "ring miss → DB read".
func (r *Run) SnapshotSince(seq int) ([]Event, bool) {
	r.mu.Lock()
	all := r.snapshotLocked()
	sink := r.sink
	r.mu.Unlock()

	if len(all) == 0 {
		// Empty ring — could be a brand new run, or a run revived from
		// disk after a process restart. The sink fallback covers the
		// revived case; for a brand new run loadSince returns no rows.
		if sink == nil {
			return nil, true
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		evs, err := sink.loadSince(ctx, r.ID, seq)
		if err != nil {
			// Sink failure is opaque to the caller — treat as "no replay
			// data right now"; client can retry. We return recoverable=true
			// so we don't surface 410 over a transient DB blip.
			return nil, true
		}
		return evs, true
	}
	oldest := all[0].Seq
	if seq+1 < oldest {
		// Ring has aged out the requested prefix.
		if sink == nil {
			return nil, false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		evs, err := sink.loadSince(ctx, r.ID, seq)
		if err != nil {
			// DB read failed and the ring can't fill the gap — surface
			// the unrecoverable miss.
			return nil, false
		}
		return evs, true
	}
	out := make([]Event, 0, len(all))
	for _, e := range all {
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	return out, true
}
