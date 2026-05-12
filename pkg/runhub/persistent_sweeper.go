package runhub

import (
	"context"
	"fmt"
	"time"

	"github.com/cinience/saker/pkg/runhub/store"
)

// loadActive walks every active RunRow in the store and registers an
// in-memory shell so Get(id) can find it. Shells have no Cancel (no
// producer survived the restart) and an empty ring (the sink fills
// SubscribeSince calls from the store).
func (h *PersistentHub) loadActive(ctx context.Context) error {
	rows, err := h.store.LoadActiveRuns(ctx)
	if err != nil {
		return fmt.Errorf("runhub: load active runs: %w", err)
	}
	for _, row := range rows {
		h.reviveRow(ctx, row)
	}
	return nil
}

// reviveRow registers a Run shell for an existing store row. Caller
// must have verified no in-memory entry exists for row.ID under
// h.inner.mu, OR be okay with the shell being dropped on collision.
//
// Shells have a nil Cancel (no producer to cancel after restart) and
// an empty ring buffer. Reading subscribers fall back through the sink.
//
// On postgres backends a per-run LISTEN goroutine is started so the
// shell's local subscribers receive events that other processes
// publish to the same run. On other drivers this is a no-op.
func (h *PersistentHub) reviveRow(ctx context.Context, row store.RunRow) *Run {
	r := newRun(row.ID, row.SessionID, row.TenantID, nil, h.inner.cfg.RingSize, row.ExpiresAt, h.inner.cfg.MaxEventBytes, h.inner.cfg.Metrics, h.inner.cfg.Logger)
	r.CreatedAt = row.CreatedAt
	r.SetStatus(RunStatus(row.Status))
	r.attachSink(h.sink)
	// Bootstrap nextSeq from the store so any rare same-process publish
	// on a revived run can't collide with persisted seqs.
	if maxSeq, err := h.store.MaxSeq(ctx, row.ID); err == nil && maxSeq > 0 {
		r.mu.Lock()
		r.nextSeq = maxSeq + 1
		r.mu.Unlock()
	}
	h.inner.mu.Lock()
	if existing, ok := h.inner.runs[row.ID]; ok {
		h.inner.mu.Unlock()
		return existing
	}
	h.inner.runs[row.ID] = r
	if row.TenantID != "" {
		h.inner.perTenan[row.TenantID]++
	}
	h.inner.mu.Unlock()

	h.metrics.OnRevival(row.TenantID)
	if h.store.Driver() == "postgres" {
		h.startListener(r)
	}
	return r
}

// StartGC starts the in-memory sweeper plus a parallel store sweeper
// that flips overdue active rows to "expired" and deletes terminal
// rows past the retention window.
func (h *PersistentHub) StartGC(ctx context.Context) {
	h.inner.StartGC(ctx)
	h.storeGCOnce.Do(func() {
		h.storeGCWg.Add(1)
		go h.runStoreGC(ctx)
	})
}

func (h *PersistentHub) runStoreGC(ctx context.Context) {
	defer h.storeGCWg.Done()
	ticker := time.NewTicker(h.inner.cfg.GCInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.storeGCStop:
			return
		case <-ticker.C:
			h.sweepStore(ctx)
		}
	}
}

// sweepStore performs the persistent counterpart to MemoryHub.sweep:
// flips overdue active rows to expired (and tears down their in-memory
// shell if any), then deletes terminal rows older than the retention
// window. Both passes are best-effort — transient store failures get
// logged and retried next tick.
func (h *PersistentHub) sweepStore(ctx context.Context) {
	now := time.Now()
	expired, err := h.store.SweepExpired(ctx, now)
	if err != nil {
		h.logger.Warn("runhub: store SweepExpired failed", "err", err)
	}
	for _, id := range expired {
		if r, gerr := h.inner.Get(id); gerr == nil {
			r.SetStatus(RunStatusExpired)
			if r.Cancel != nil {
				r.Cancel()
			}
			r.closeAllSubscribers()
		}
	}
	finished, err := h.store.SweepFinished(ctx, now.Add(-h.inner.cfg.TerminalRetention))
	if err != nil {
		h.logger.Warn("runhub: store SweepFinished failed", "err", err)
	}
	for _, id := range finished {
		h.inner.Remove(id)
		if derr := h.store.DeleteRun(ctx, id); derr != nil {
			h.logger.Warn("runhub: store DeleteRun failed", "run_id", id, "err", derr)
		}
	}
}
