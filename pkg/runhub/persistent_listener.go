package runhub

import (
	"context"
	"time"
)

// runListener pairs a *store.Listener with the goroutine that drains
// notifications and feeds them into Run.DeliverExternal. Closed by
// Remove/Finish/Shutdown.
type runListener struct {
	listener listenerHandle
	stop     chan struct{}
	done     chan struct{}
}

// listenerHandle abstracts the store.Listener surface used here. Lets
// Stage B swap in a shared LISTEN pool subscriber without changing the
// loop body. Implemented today by *store.Listener; future implementations
// only need to expose the same Notifications/Close pair.
type listenerHandle interface {
	Notifications() <-chan string
	Close() error
}

// notifyChannelName returns the postgres channel name used for a run's
// LISTEN/NOTIFY fan-out. The "runhub_" prefix gives operators a clear
// signal in pg_stat_activity; the runID portion is hex so it's a valid
// unquoted identifier under the 63-byte limit.
func notifyChannelName(runID string) string {
	return "runhub_" + runID
}

// startListener spins up a LISTEN session on the run's notify channel
// and a goroutine that pulls fresh events from the store on every
// notification. Idempotent — a second call for the same run id is a
// no-op.
//
// Safe-by-design: on postgres errors (Listen failure, conn drop) the
// goroutine exits cleanly and clients still get backfill via the
// sink-loadSince path on every fresh SubscribeSince.
func (h *PersistentHub) startListener(r *Run) {
	h.listenersMu.Lock()
	if _, ok := h.listeners[r.ID]; ok {
		h.listenersMu.Unlock()
		return
	}
	channel := notifyChannelName(r.ID)
	listener, err := h.store.Listen(context.Background(), channel)
	if err != nil {
		h.listenersMu.Unlock()
		h.logger.Warn("runhub: store Listen failed", "run_id", r.ID, "channel", channel, "err", err)
		return
	}
	rl := &runListener{
		listener: listener,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	h.listeners[r.ID] = rl
	h.listenersMu.Unlock()

	h.metrics.OnListenerStart()
	go h.runListenerLoop(r, rl)
}

// runListenerLoop drains notifications and pushes the resulting
// freshly-persisted events into the local Run via DeliverExternal.
// lastSeq tracks the highest seq we've already delivered so a
// duplicate notification (or one received between LoadEventsSince
// pages) doesn't replay events.
func (h *PersistentHub) runListenerLoop(r *Run, rl *runListener) {
	defer close(rl.done)
	lastSeq := 0
	r.mu.Lock()
	if r.nextSeq > 1 {
		lastSeq = r.nextSeq - 1
	}
	r.mu.Unlock()

	notifications := rl.listener.Notifications()
	for {
		select {
		case <-rl.stop:
			return
		case _, ok := <-notifications:
			if !ok {
				// Channel closed by Listener.Close or conn drop. Done.
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			rows, err := h.store.LoadEventsSince(ctx, r.ID, lastSeq)
			cancel()
			if err != nil {
				h.logger.Warn("runhub: listener LoadEventsSince failed", "run_id", r.ID, "err", err)
				continue
			}
			if len(rows) == 0 {
				continue
			}
			events := make([]Event, len(rows))
			for i, row := range rows {
				events[i] = Event{Seq: row.Seq, Type: row.Type, Data: row.Data}
			}
			r.DeliverExternal(events)
			lastSeq = events[len(events)-1].Seq
		}
	}
}

// stopListener tears down the LISTEN session for a run id. Idempotent:
// a missing run id is silently ignored.
func (h *PersistentHub) stopListener(id string) {
	h.listenersMu.Lock()
	rl, ok := h.listeners[id]
	if !ok {
		h.listenersMu.Unlock()
		return
	}
	delete(h.listeners, id)
	h.listenersMu.Unlock()

	close(rl.stop)
	if err := rl.listener.Close(); err != nil {
		h.logger.Warn("runhub: listener close failed", "run_id", id, "err", err)
	}
	<-rl.done
	h.metrics.OnListenerStop()
}
