package runhub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Errors returned by the hub.
var (
	// ErrCapacity is returned when MaxRuns or MaxRunsPerTenant would be
	// exceeded by a new Create. The gateway maps this to HTTP 429.
	ErrCapacity = errors.New("runhub: capacity exhausted")
	// ErrNotFound is returned by Get / Cancel when the run id is unknown
	// (or has aged out / been swept). The gateway maps this to HTTP 404.
	ErrNotFound = errors.New("runhub: run not found")
	// ErrEventTooLarge is returned (logged) by Run.Publish / DeliverExternal
	// when the event payload exceeds Config.MaxEventBytes. Cap exists to
	// stop a single oversized payload from clogging the ring buffer or the
	// async writer queue. Returned only via metrics + slog; Publish itself
	// keeps its int return contract (seq=0 on rejection, mirroring the
	// "run closed" branch).
	ErrEventTooLarge = errors.New("runhub: event payload exceeds MaxEventBytes")
)

// Config carries the hub's per-instance limits. Zero values fall back to
// sensible defaults (matching .docs §15).
type Config struct {
	MaxRuns          int
	MaxRunsPerTenant int
	RingSize         int
	GCInterval       time.Duration
	// TerminalRetention is the grace window between a run reaching a
	// terminal state (completed/cancelled/failed/expired) and the GC
	// sweeper deleting the in-memory row (and, for PersistentHub, the
	// stored row + events). Long enough that a client can reconnect
	// once and read final status; short enough that a runaway client
	// pattern can't fill the hub with completed rows. Zero falls back
	// to defaultTerminalRetention (60s).
	//
	// Both MemoryHub and PersistentHub honor this; the persistent
	// sweeper additionally extends it across process restarts via the
	// store's finished_at column.
	TerminalRetention time.Duration
	// MaxEventBytes caps the byte length of any single event payload that
	// flows through Run.Publish or DeliverExternal. Oversized payloads are
	// rejected (seq=0 returned) and counted in MetricsHooks.OnOversizedEvent.
	// Zero means unbounded (legacy behavior). Recommend ~1 MiB in production
	// to keep one runaway payload from monopolizing the ring buffer or the
	// async writer queue.
	MaxEventBytes int64
	// Metrics is the observability hook for hub-level events (currently
	// oversized-event rejections). nil → NopMetricsHooks. Both MemoryHub
	// and PersistentHub honor it; PersistentHub additionally fans the
	// hooks into its persistence layer.
	Metrics MetricsHooks
	// SubscriberIdleTimeout is the wall-clock window a subscriber's
	// channel may sit without receiving a successful fan-out send before
	// the GC sweeper closes it. Targets the leaked-SSE-client failure
	// mode: an HTTP client that disconnected without firing its unsub
	// closure leaves a subscriber pinned to the run forever, exhausting
	// per-run capacity and the run's ring fan-out budget.
	//
	// Zero (default) disables eviction — useful for low-rate runs whose
	// normal cadence exceeds any sensible timeout. Recommend 5–15 minutes
	// in production once the event-rate floor is measured. Eviction fires
	// on every GCInterval tick, so the realized timeout is
	// [SubscriberIdleTimeout, SubscriberIdleTimeout + GCInterval).
	SubscriberIdleTimeout time.Duration
	Logger                *slog.Logger
}

// defaultTerminalRetention is the fallback for Config.TerminalRetention
// when callers leave it zero. Mirrors the historical hardcoded value so
// the default GC behavior didn't shift when this knob became
// configurable.
const defaultTerminalRetention = 60 * time.Second

// Hub is the polymorphic surface every gateway handler talks to. The
// in-process implementation is *MemoryHub; the persistence-backed variant
// is *PersistentHub (see pkg/runhub/persistent_hub.go) and wraps a
// *MemoryHub plus a *store.Store. Keeping this interface narrow lets the
// gateway swap backends via Options.RunHubDSN without touching handler code.
type Hub interface {
	Create(opts CreateOptions) (*Run, error)
	Get(id string) (*Run, error)
	Cancel(id string) error
	Finish(id string, status RunStatus)
	Remove(id string)
	Len() int
	LenForTenant(tenantID string) int
	StartGC(ctx context.Context)
	Shutdown()
	// Flush blocks until every event accepted by Publish before this
	// call has been persisted. No-op on MemoryHub (writes are
	// synchronous to the in-memory ring); fence on PersistentHub for
	// the async batch writer. Tests use this as a synchronization point
	// before reading back from the underlying store.
	Flush()
}

// MemoryHub holds in-flight runs and dispatches subscribe / publish / cancel
// across goroutines. One MemoryHub per gateway instance when no persistence
// DSN is configured. Implements Hub.
type MemoryHub struct {
	cfg Config

	mu       sync.RWMutex
	runs     map[string]*Run
	perTenan map[string]int

	gcCancel context.CancelFunc
	gcOnce   sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewHub builds an empty MemoryHub with the supplied config and returns it
// as the Hub interface so the call site is backend-agnostic. Defaults
// applied. Use NewMemoryHub if you need the concrete type back (tests).
func NewHub(cfg Config) Hub {
	return NewMemoryHub(cfg)
}

// NewMemoryHub is the concrete-typed constructor. Same semantics as NewHub.
func NewMemoryHub(cfg Config) *MemoryHub {
	if cfg.MaxRuns <= 0 {
		cfg.MaxRuns = 256
	}
	if cfg.MaxRunsPerTenant < 0 {
		cfg.MaxRunsPerTenant = 0
	}
	if cfg.RingSize <= 0 {
		cfg.RingSize = 512
	}
	if cfg.GCInterval <= 0 {
		cfg.GCInterval = 30 * time.Second
	}
	if cfg.TerminalRetention <= 0 {
		cfg.TerminalRetention = defaultTerminalRetention
	}
	if cfg.MaxEventBytes < 0 {
		cfg.MaxEventBytes = 0
	}
	if cfg.SubscriberIdleTimeout < 0 {
		cfg.SubscriberIdleTimeout = 0
	}
	if cfg.Metrics == nil {
		cfg.Metrics = NopMetricsHooks()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &MemoryHub{
		cfg:      cfg,
		runs:     make(map[string]*Run),
		perTenan: make(map[string]int),
		stopCh:   make(chan struct{}),
	}
}

// CreateOptions carries per-run create-time inputs.
type CreateOptions struct {
	SessionID string
	TenantID  string
	ExpiresAt time.Time
	// Cancel is the goroutine-side cancel for the underlying agent
	// context. The hub stores it so the cancel/expire paths can stop
	// the producer goroutine.
	Cancel context.CancelFunc
}

// Create registers a new run and returns it. Returns ErrCapacity when the
// hub or per-tenant cap would be exceeded.
func (h *MemoryHub) Create(opts CreateOptions) (*Run, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.runs) >= h.cfg.MaxRuns {
		return nil, ErrCapacity
	}
	if h.cfg.MaxRunsPerTenant > 0 && opts.TenantID != "" {
		if h.perTenan[opts.TenantID] >= h.cfg.MaxRunsPerTenant {
			return nil, ErrCapacity
		}
	}
	id := generateRunID()
	for _, exists := h.runs[id]; exists; _, exists = h.runs[id] {
		id = generateRunID()
	}
	run := newRun(id, opts.SessionID, opts.TenantID, opts.Cancel, h.cfg.RingSize, opts.ExpiresAt, h.cfg.MaxEventBytes, h.cfg.Metrics, h.cfg.Logger)
	h.runs[id] = run
	if opts.TenantID != "" {
		h.perTenan[opts.TenantID]++
	}
	return run, nil
}

// Get returns the run by id, or ErrNotFound.
func (h *MemoryHub) Get(id string) (*Run, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.runs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return r, nil
}

// Cancel marks the run cancelling, calls its cancel func, and lets the
// producer drain. Idempotent.
func (h *MemoryHub) Cancel(id string) error {
	h.mu.RLock()
	r, ok := h.runs[id]
	h.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	r.SetStatus(RunStatusCancelling)
	if r.Cancel != nil {
		r.Cancel()
	}
	return nil
}

// Finish marks a run terminal and tears down its subscribers. The run
// row stays in the map briefly so a reconnect can fetch the final
// status; the GC sweeps it after a short retention window.
func (h *MemoryHub) Finish(id string, status RunStatus) {
	h.mu.RLock()
	r, ok := h.runs[id]
	h.mu.RUnlock()
	if !ok {
		return
	}
	r.FinishedAt = time.Now()
	r.SetStatus(status)
	r.closeAllSubscribers()
}

// Remove evicts a run row from the map. Called by the GC after the
// post-finish retention window or on Shutdown.
func (h *MemoryHub) Remove(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.runs[id]
	if !ok {
		return
	}
	delete(h.runs, id)
	if r.TenantID != "" {
		if c := h.perTenan[r.TenantID]; c > 0 {
			h.perTenan[r.TenantID] = c - 1
			if h.perTenan[r.TenantID] == 0 {
				delete(h.perTenan, r.TenantID)
			}
		}
	}
}

// Len returns the number of runs currently held by the hub. Snapshot —
// races with concurrent Create/Remove are expected and harmless.
func (h *MemoryHub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.runs)
}

// LenForTenant returns the number of in-flight runs for one tenant.
func (h *MemoryHub) LenForTenant(tenantID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.perTenan[tenantID]
}

// StartGC kicks off the background sweeper goroutine. Idempotent; only
// the first call starts a goroutine. Caller stops it via Shutdown.
func (h *MemoryHub) StartGC(parent context.Context) {
	h.gcOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		h.gcCancel = cancel
		h.wg.Add(1)
		go h.runGC(ctx)
	})
}

func (h *MemoryHub) runGC(ctx context.Context) {
	defer h.wg.Done()
	ticker := time.NewTicker(h.cfg.GCInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.sweep()
		}
	}
}

// sweep evicts runs whose ExpiresAt is in the past or which have been
// terminal for longer than cfg.TerminalRetention. Default retention is
// short enough (60s) that a runaway client pattern can't fill the hub
// with completed rows, but long enough that a typical reconnect can
// still observe the terminal status.
func (h *MemoryHub) sweep() {
	now := time.Now()
	finishedRetention := h.cfg.TerminalRetention
	idleTimeout := h.cfg.SubscriberIdleTimeout

	type victim struct {
		id     string
		expire bool
	}
	var victims []victim
	// idleTargets is the snapshot of still-active runs eligible for the
	// idle-subscriber sweep. Collected under h.mu and walked outside the
	// lock so each run's eviction (which takes its own r.mu) doesn't
	// nest hub-level and run-level locks.
	var idleTargets []*Run

	h.mu.RLock()
	for id, r := range h.runs {
		st := r.Status()
		switch st {
		case RunStatusCompleted, RunStatusCancelled, RunStatusFailed, RunStatusExpired:
			if !r.FinishedAt.IsZero() && now.Sub(r.FinishedAt) > finishedRetention {
				victims = append(victims, victim{id: id})
			}
		default:
			if !r.ExpiresAt.IsZero() && now.After(r.ExpiresAt) {
				victims = append(victims, victim{id: id, expire: true})
			} else if idleTimeout > 0 {
				idleTargets = append(idleTargets, r)
			}
		}
	}
	h.mu.RUnlock()

	for _, r := range idleTargets {
		if n := r.evictIdleSubscribers(now, idleTimeout); n > 0 {
			h.cfg.Logger.Info("runhub: evicting idle subscribers",
				"run_id", r.ID,
				"tenant", r.TenantID,
				"count", n,
				"timeout", idleTimeout,
			)
			for i := 0; i < n; i++ {
				h.cfg.Metrics.OnSubscriberIdleEvicted(r.TenantID)
			}
		}
	}

	for _, v := range victims {
		if v.expire {
			r, err := h.Get(v.id)
			if err == nil && r != nil {
				h.cfg.Logger.Info("runhub: expiring run", "run_id", v.id)
				r.SetStatus(RunStatusExpired)
				if r.Cancel != nil {
					r.Cancel()
				}
				r.closeAllSubscribers()
			}
		}
		h.Remove(v.id)
	}
}

// Shutdown stops the GC, cancels every in-flight run, and tears down
// subscribers. Idempotent.
// Flush is a no-op on MemoryHub — Publish writes synchronously to the
// in-memory ring, so there's nothing to drain. Implemented to satisfy
// the Hub interface; PersistentHub provides the meaningful version.
func (h *MemoryHub) Flush() {}

func (h *MemoryHub) Shutdown() {
	select {
	case <-h.stopCh:
		return
	default:
		close(h.stopCh)
	}
	if h.gcCancel != nil {
		h.gcCancel()
	}
	h.wg.Wait()

	h.mu.Lock()
	runs := make([]*Run, 0, len(h.runs))
	for _, r := range h.runs {
		runs = append(runs, r)
	}
	h.runs = make(map[string]*Run)
	h.perTenan = make(map[string]int)
	h.mu.Unlock()

	for _, r := range runs {
		if r.Cancel != nil {
			r.Cancel()
		}
		r.SetStatus(RunStatusCancelled)
		r.closeAllSubscribers()
	}
}

// generateRunID returns "run_<24-hex>". The hex space is plenty wide for
// in-process uniqueness; the explicit prefix mirrors OpenAI's run ids.
func generateRunID() string {
	var b [12]byte
	_, err := rand.Read(b[:])
	if err != nil {
		// crypto/rand failure on Linux is essentially impossible in
		// practice (would mean /dev/urandom is gone). Panic so we never
		// emit a deterministic id silently.
		panic("runhub: rand.Read failed: " + err.Error())
	}
	return "run_" + hex.EncodeToString(b[:])
}
