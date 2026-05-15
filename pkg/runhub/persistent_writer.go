package runhub

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/saker-ai/saker/pkg/runhub/store"
)

// Defaults for the async batch writer. Tuned conservatively so a stock
// PersistentHub (no operator override) sees a meaningful throughput
// improvement without risking memory blow-up under burst load:
//
//   - BatchSize 64       — amortizes one fsync over up to 64 events.
//   - BatchInterval 50ms — bounds tail latency for low-rate streams.
//   - BatchBufferSize 1024 — ~16× the batch size, gives the producer
//                            burst tolerance before backpressure kicks in.
//
// Operators tune these via Options.BatchSize/BatchInterval/BatchBufferSize
// (CLI flags), forwarded into PersistentConfig.
const (
	defaultBatchSize       = 64
	defaultBatchInterval   = 50 * time.Millisecond
	defaultBatchBufferSize = 1024
)

// eventEnvelope is one queued (runID, tenantID, event) tuple. Sized
// inline so a chan of envelopes stays cache-friendly even at 1000+
// buffer depth. tenantID is carried on the envelope (rather than looked
// up from the run map at flush time) so the writer goroutine can
// attribute persist counters / drop counters per tenant without a
// hub lock acquisition on the hot path.
type eventEnvelope struct {
	runID    string
	tenantID string
	e        Event
}

// batchWriter is the asynchronous persistence layer for PersistentHub.
// Replaces the synchronous Insert+Notify on the producer's hot path
// with a non-blocking enqueue; a single writer goroutine batches up to
// BatchSize envelopes (or up to BatchInterval idle time) and flushes
// them with one InsertEventsBatch + one Notify per distinct runID.
//
// Backpressure: when the enqueue chan is full, write drops the OLDEST
// queued envelope (best-effort try-receive then send) and increments
// metrics.OnBatchDrop, so the producer never blocks. Dropping the oldest
// (vs the newest) keeps the most recent stream of events flowing — a
// late "completed" event is more useful to a consumer than the 1024-th
// oldest token.
type batchWriter struct {
	store     *store.Store
	logger    *slog.Logger
	metrics   MetricsHooks
	enqueue   chan eventEnvelope
	flushCh   chan chan struct{} // sync flush requests; close inner chan to signal
	size      int
	interval  time.Duration
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	breaker   *sinkBreaker
}

// newBatchWriter starts the writer goroutine. Caller must call shutdown
// to drain the queue and stop the writer cleanly. breakerThreshold <= 0
// disables the circuit breaker (every flush calls the store).
func newBatchWriter(st *store.Store, logger *slog.Logger, metrics MetricsHooks, size, buffer int, interval time.Duration, breakerThreshold int, breakerCooldown time.Duration) *batchWriter {
	if size <= 0 {
		size = defaultBatchSize
	}
	if buffer <= 0 {
		buffer = defaultBatchBufferSize
	}
	if interval <= 0 {
		interval = defaultBatchInterval
	}
	if metrics == nil {
		metrics = NopMetricsHooks()
	}
	if logger == nil {
		logger = slog.Default()
	}
	w := &batchWriter{
		store:    st,
		logger:   logger,
		metrics:  metrics,
		enqueue:  make(chan eventEnvelope, buffer),
		flushCh:  make(chan chan struct{}, 1),
		size:     size,
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		breaker:  newSinkBreaker(breakerThreshold, breakerCooldown, metrics),
	}
	go w.run()
	return w
}

// write is the non-blocking enqueue hot path called by dbSink.write
// (which itself runs from Run.Publish under no lock). Drop-oldest
// semantics on a full queue: try one receive (discard the oldest
// envelope) then enqueue the new one. If the receive itself fails (race
// with the writer goroutine consuming the same envelope), fall through
// and try the send once more — if THAT fails too, count the drop and
// move on so the producer never blocks.
func (w *batchWriter) write(env eventEnvelope) {
	select {
	case w.enqueue <- env:
		w.metrics.OnBatchQueueDepth(len(w.enqueue))
		return
	default:
	}
	// Queue full — drop oldest, enqueue new. The drop counter is
	// attributed to the OLDEST envelope's tenant (the one we just
	// evicted) since that's the event whose durability is actually lost.
	// If the receive races and yields nothing, we fall through and the
	// retry-send below counts the drop against the NEW envelope's tenant.
	select {
	case dropped := <-w.enqueue:
		w.metrics.OnBatchDrop(dropped.tenantID)
	default:
		// Writer goroutine drained simultaneously; nothing to drop.
	}
	select {
	case w.enqueue <- env:
		w.metrics.OnBatchQueueDepth(len(w.enqueue))
	default:
		// Still full (extremely contended). Drop the new event so we don't
		// block. Counter accounts for both eviction and rejection styles.
		w.metrics.OnBatchDrop(env.tenantID)
	}
}

// run drains the enqueue chan in batches. Two flush triggers:
//   - batch buffer hits w.size envelopes (size trigger), or
//   - the interval ticker fires with at least one buffered envelope.
//
// On stop, drains every remaining envelope before returning so shutdown
// preserves all accepted writes.
func (w *batchWriter) run() {
	defer close(w.done)
	buf := make([]eventEnvelope, 0, w.size)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	flush := func() {
		if len(buf) == 0 {
			return
		}
		w.flush(buf)
		buf = buf[:0]
		w.metrics.OnBatchQueueDepth(len(w.enqueue))
	}

	for {
		select {
		case <-w.stop:
			// Drain everything left in the queue, then exit.
			for {
				select {
				case env := <-w.enqueue:
					buf = append(buf, env)
					if len(buf) >= w.size {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case env := <-w.enqueue:
			buf = append(buf, env)
			if len(buf) >= w.size {
				flush()
			}
		case <-ticker.C:
			flush()
		case done := <-w.flushCh:
			// Drain everything currently queued before signaling so the
			// caller's "after flush" reads see every accepted envelope.
			for {
				select {
				case env := <-w.enqueue:
					buf = append(buf, env)
				default:
					flush()
					close(done)
					goto nextIter
				}
			}
		nextIter:
		}
	}
}

// flushSync requests a synchronous drain of the enqueue chan and waits
// for the writer goroutine to confirm. Provides a fence for tests and
// for operator-driven graceful shutdowns where the caller wants
// "everything I've published so far is on disk now". Returns
// immediately if the writer has already stopped.
func (w *batchWriter) flushSync() {
	done := make(chan struct{})
	select {
	case <-w.stop:
		return
	case w.flushCh <- done:
	}
	select {
	case <-done:
	case <-w.stop:
	}
}

// flush batches every envelope into a single InsertEventsBatch and
// fires one Notify per distinct runID (so a batch of 64 events for the
// same run doesn't generate 64 NOTIFYs — N+1 vs 2N pg traffic).
//
// Circuit breaker: if the store has tripped the breaker Open, every
// envelope in this batch is silently dropped (no insert, no notify) and
// counted via OnSinkBreakerSkipped. The events ARE already in the
// in-memory ring (Run.Publish enqueues to the writer AFTER the
// fan-out), so live subscribers keep receiving them. We trade
// reconnect-after-restart durability for service stability during a
// long store outage — exactly the goal of an open breaker.
func (w *batchWriter) flush(envs []eventEnvelope) {
	// Span: runhub.batch.flush. Parent context is background — the
	// writer goroutine has no caller ctx; inserts/notifies span this
	// one. tenants_count is the distinct-tenant cardinality of the
	// batch, useful for spotting cross-tenant batches when tuning
	// fairness. breaker_skipped attribute distinguishes a "real" empty
	// flush from one where the breaker swallowed the batch.
	ctx, span := runhubTracer().Start(context.Background(), "runhub.batch.flush",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.Int("batch.size", len(envs))),
	)
	defer span.End()

	if !w.breaker.allow() {
		w.metrics.OnSinkBreakerSkipped(len(envs))
		span.SetAttributes(attribute.Bool("breaker_skipped", true))
		return
	}

	// flushStart bounds the OnBatchFlush observation: start at the top
	// of the row-marshal loop, end after every per-run NOTIFY has been
	// attempted. Distinct from the InsertEventsBatch-only timing
	// reported via OnEventPersist below — operators chart the gap to
	// surface NOTIFY storms or marshal regressions.
	flushStart := time.Now()
	defer func() {
		w.metrics.OnBatchFlush(len(envs), time.Since(flushStart))
	}()

	rows := make([]store.EventRow, len(envs))
	tenants := make([]string, len(envs))
	tenantSet := make(map[string]struct{}, 4)
	for i, env := range envs {
		rows[i] = store.EventRow{
			RunID: env.runID,
			Seq:   env.e.Seq,
			Type:  env.e.Type,
			Data:  env.e.Data,
		}
		tenants[i] = env.tenantID
		tenantSet[env.tenantID] = struct{}{}
	}
	span.SetAttributes(attribute.Int("tenants_count", len(tenantSet)))

	insertCtx, span2 := runhubTracer().Start(ctx, "runhub.store.insert",
		trace.WithAttributes(attribute.Int("rows", len(rows))),
	)
	start := time.Now()
	timedCtx, cancel := context.WithTimeout(insertCtx, 10*time.Second)
	err := w.store.InsertEventsBatch(timedCtx, rows)
	cancel()
	dur := time.Since(start)
	if err != nil {
		span2.RecordError(err)
		span2.SetStatus(codes.Error, "InsertEventsBatch failed")
	}
	span2.End()

	w.metrics.OnEventPersist(err == nil, dur, tenants)
	if err != nil {
		w.breaker.recordFailure()
		w.logger.Warn("runhub: batch insert failed", "rows", len(rows), "err", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "batch insert failed")
		return
	}
	w.breaker.recordSuccess()

	// Per-run NOTIFY de-dup. Only one NOTIFY per channel per batch — the
	// payload is opaque ("evt") so collapsing is safe.
	notified := make(map[string]struct{}, len(envs))
	for _, env := range envs {
		if _, ok := notified[env.runID]; ok {
			continue
		}
		notified[env.runID] = struct{}{}
		w.notifyOne(ctx, env.runID)
	}
	span.SetAttributes(attribute.Int("notify_count", len(notified)))
}

// notifyOne emits one NOTIFY for a run id and wraps it in a child span
// so cross-process delivery cost is visible separately from the
// per-batch flush span. NOTIFY failures degrade silently — listeners
// have a LoadEventsSince fallback.
func (w *batchWriter) notifyOne(parent context.Context, runID string) {
	channel := notifyChannelName(runID)
	ctx, span := runhubTracer().Start(parent, "runhub.store.notify",
		trace.WithAttributes(attribute.String("channel", channel)),
	)
	defer span.End()
	nctx, ncancel := context.WithTimeout(ctx, 2*time.Second)
	nerr := w.store.Notify(nctx, channel, "evt")
	ncancel()
	if nerr != nil {
		w.logger.Debug("runhub: batch notify failed", "run_id", runID, "err", nerr)
		span.RecordError(nerr)
		span.SetStatus(codes.Error, "notify failed")
	}
}

// shutdown signals the writer to drain + stop and waits for the
// goroutine to exit. Idempotent — safe to call multiple times even
// concurrently.
func (w *batchWriter) shutdown() {
	w.closeOnce.Do(func() {
		close(w.stop)
	})
	<-w.done
}
