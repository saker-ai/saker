package runhub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/saker-ai/saker/pkg/runhub/store"
)

// PersistentHub composes a MemoryHub with a runhub-store backed event
// sink, persisting run metadata + events. Compared to MemoryHub it adds:
//
//   - Restart-replay: NewPersistentHub loads every active RunRow into
//     the in-memory map as a "shell" Run (empty ring, sink attached),
//     so a client reconnecting by run_id after a process restart can
//     read the historical event stream from the store.
//   - Hot-path persistence: every Run.Publish synchronously writes the
//     event to the store BEFORE fanning out, so the store is the
//     authoritative log for clients reconnecting via Last-Event-ID after
//     the in-memory ring has aged out.
//   - Cross-process fan-out (postgres only): each persisted event also
//     fires a NOTIFY on the run's channel; revived shells in other
//     processes pick that up via LISTEN and deliver the new events to
//     their local subscribers, so a producer in process A and a
//     reconnect client in process B see end-to-end streaming. SQLite /
//     other drivers degrade to single-process semantics.
//   - Cleanup: on top of the in-memory GC sweep, a parallel store
//     sweeper flips overdue active rows to "expired" and deletes
//     terminal rows past the retention window.
//
// PersistentHub satisfies the Hub interface; gateway code holds it
// behind that interface and stays backend-agnostic.
type PersistentHub struct {
	inner   *MemoryHub
	store   *store.Store
	logger  *slog.Logger
	metrics MetricsHooks
	sink    *dbSink

	storeGCOnce sync.Once
	storeGCStop chan struct{}
	storeGCWg   sync.WaitGroup

	// listeners holds the per-run LISTEN sessions started by reviveRow
	// when the store driver is postgres. Keyed by runID. nil-map-safe
	// access is gated by listenersMu.
	listenersMu sync.Mutex
	listeners   map[string]*runListener
}

// PersistentConfig wraps Config with the store handle. The store must
// already be open and migrated (callers go through store.Open).
// PersistentHub.Shutdown closes the store.
type PersistentConfig struct {
	Config
	Store *store.Store
	// Metrics is the observability hook fired on persistence + listener
	// lifecycle events. nil falls back to NopMetricsHooks so the hub can
	// always call hook methods unconditionally on hot paths.
	Metrics MetricsHooks
	// BatchSize bounds the number of envelopes the async writer
	// accumulates before issuing one InsertEventsBatch. Zero → default
	// (defaultBatchSize). Tune up for throughput, down for tail latency.
	BatchSize int
	// BatchBufferSize bounds the enqueue chan capacity. When the producer
	// outruns the writer the batchWriter drops the oldest queued envelope
	// (counted via MetricsHooks.OnBatchDrop) so Run.Publish never blocks.
	// Zero → default (defaultBatchBufferSize).
	BatchBufferSize int
	// BatchInterval bounds the writer's idle time — even a partially
	// filled buffer is flushed every BatchInterval so a low-rate stream
	// doesn't stall waiting for size to fill. Zero → default
	// (defaultBatchInterval).
	BatchInterval time.Duration
	// SinkBreakerThreshold is the number of consecutive batch-flush
	// failures that trips the dbSink circuit breaker open, suppressing
	// further store calls until SinkBreakerCooldown elapses. Zero
	// disables the breaker entirely (every flush calls the store no
	// matter how many in a row failed) — fine for tests, NOT recommended
	// in production where a stuck store would otherwise burn CPU + log
	// volume on every batch interval.
	SinkBreakerThreshold int
	// SinkBreakerCooldown is how long the breaker stays Open before
	// transitioning to HalfOpen and allowing one probe call. Zero with
	// a non-zero threshold latches the breaker Open until restart
	// (operator opt-in for fail-fast envs).
	SinkBreakerCooldown time.Duration
}

// Compile-time check that PersistentHub satisfies the Hub interface.
var _ Hub = (*PersistentHub)(nil)

// NewPersistentHub builds a hub backed by an open *store.Store. At
// startup it loads every active RunRow into the in-memory map so
// clients reconnecting by run_id after a restart can find their run.
//
// Caller is responsible for opening the store; PersistentHub.Shutdown
// closes it.
func NewPersistentHub(cfg PersistentConfig) (*PersistentHub, error) {
	if cfg.Store == nil {
		return nil, errors.New("runhub: PersistentHub requires a non-nil Store")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Metrics == nil {
		cfg.Metrics = NopMetricsHooks()
	}
	// Mirror the metrics hook into the embedded Config so the in-memory
	// hub (Run.Publish oversized-event check, future hub-level signals)
	// shares the same prometheus collectors as the persistence layer.
	// Caller-supplied cfg.Config.Metrics still wins if set explicitly.
	if cfg.Config.Metrics == nil {
		cfg.Config.Metrics = cfg.Metrics
	}
	inner := NewMemoryHub(cfg.Config)
	h := &PersistentHub{
		inner:       inner,
		store:       cfg.Store,
		logger:      cfg.Logger,
		metrics:     cfg.Metrics,
		storeGCStop: make(chan struct{}),
		listeners:   make(map[string]*runListener),
	}
	h.sink = newDBSink(cfg.Store, cfg.Logger, cfg.Metrics, cfg.BatchSize, cfg.BatchBufferSize, cfg.BatchInterval, cfg.SinkBreakerThreshold, cfg.SinkBreakerCooldown)
	if err := h.loadActive(context.Background()); err != nil {
		return nil, err
	}
	return h, nil
}

// Create registers a new run with both the store and the in-memory
// hub, attaching the dbSink so subsequent Publish calls land in the
// store before fanning out.
func (h *PersistentHub) Create(opts CreateOptions) (*Run, error) {
	r, err := h.inner.Create(opts)
	if err != nil {
		return nil, err
	}
	row := store.RunRow{
		ID:        r.ID,
		SessionID: r.SessionID,
		TenantID:  r.TenantID,
		Status:    string(r.Status()),
		ExpiresAt: r.ExpiresAt,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.store.UpsertRun(ctx, row); err != nil {
		// Roll back the in-memory create so the caller sees a clean
		// failure instead of an in-memory run that the store doesn't
		// know about.
		h.inner.Remove(r.ID)
		return nil, fmt.Errorf("runhub: persist run row: %w", err)
	}
	r.attachSink(h.sink)
	return r, nil
}

// Get returns the run by id, looking first in memory, then in the
// store (revival after restart). ErrNotFound when neither holds it.
func (h *PersistentHub) Get(id string) (*Run, error) {
	if r, err := h.inner.Get(id); err == nil {
		return r, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	row, err := h.store.LoadRun(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return h.reviveRow(ctx, row), nil
}

// Cancel forwards to MemoryHub and updates the store status.
func (h *PersistentHub) Cancel(id string) error {
	if err := h.inner.Cancel(id); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.store.UpdateStatus(ctx, id, string(RunStatusCancelling)); err != nil {
		h.logger.Warn("runhub: persist cancel status failed", "run_id", id, "err", err)
	}
	return nil
}

// Finish forwards to MemoryHub and updates the store status.
func (h *PersistentHub) Finish(id string, status RunStatus) {
	h.inner.Finish(id, status)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.store.UpdateStatus(ctx, id, string(status)); err != nil {
		h.logger.Warn("runhub: persist finish status failed", "run_id", id, "err", err)
	}
}

// Remove evicts both the in-memory entry and the store rows.
func (h *PersistentHub) Remove(id string) {
	h.stopListener(id)
	h.inner.Remove(id)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.store.DeleteRun(ctx, id); err != nil {
		h.logger.Warn("runhub: store delete failed", "run_id", id, "err", err)
	}
}

// Flush blocks until every event accepted by Publish before this call
// has been persisted by the async batch writer. Provides a fence for
// tests and for operator-driven graceful shutdowns where the caller
// wants "everything I've published so far is on disk now". Cheap when
// the queue is empty (one channel round-trip).
func (h *PersistentHub) Flush() {
	if h.sink != nil {
		h.sink.flush()
	}
}

// Len returns the total number of in-memory runs (matches MemoryHub).
// Rows that exist only in the store and haven't been revived are not
// counted; those load on demand via Get.
func (h *PersistentHub) Len() int { return h.inner.Len() }

// LenForTenant returns the in-memory run count for one tenant.
func (h *PersistentHub) LenForTenant(tenantID string) int { return h.inner.LenForTenant(tenantID) }

// Shutdown stops the store sweeper, every per-run LISTEN session, the
// inner MemoryHub, and closes the store handle. Idempotent.
func (h *PersistentHub) Shutdown() {
	select {
	case <-h.storeGCStop:
		// already shutdown
	default:
		close(h.storeGCStop)
	}
	h.storeGCWg.Wait()

	h.listenersMu.Lock()
	ids := make([]string, 0, len(h.listeners))
	for id := range h.listeners {
		ids = append(ids, id)
	}
	h.listenersMu.Unlock()
	for _, id := range ids {
		h.stopListener(id)
	}

	h.inner.Shutdown()
	// Drain the async batch writer BEFORE closing the store — any
	// envelopes still queued are flushed on the way out so a graceful
	// shutdown preserves accepted events. Then close the store, which
	// internally also tears down the LISTEN pool.
	if h.sink != nil {
		h.sink.shutdown()
	}
	if err := h.store.Close(); err != nil {
		h.logger.Warn("runhub: store close failed", "err", err)
	}
}
