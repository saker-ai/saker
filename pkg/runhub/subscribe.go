package runhub

import (
	"sync"
	"sync/atomic"
	"time"
)

// subscriber is one downstream listener (typically an SSE client). Each
// subscriber has its own bounded channel so a slow client only hurts itself.
type subscriber struct {
	ch      chan Event
	dropped atomic.Uint64 // count of events dropped due to slow consumer
	// lastReadAt is the unix-nano timestamp of the most recent successful
	// fan-out send to this subscriber's channel. Initialized to subscription
	// time so a brand-new subscriber on a quiet run isn't evicted on the
	// first sweep. Used by the GC sweeper to evict leaked SSE streams whose
	// consumer disconnected without invoking unsub.
	lastReadAt atomic.Int64
	// closeOnce guards close(ch) so the unsub closure, idle eviction, and
	// closeAllSubscribers can all attempt to close the channel without
	// panicking on the second caller.
	closeOnce sync.Once
}

// closeChan closes the subscriber's channel exactly once, even when the
// unsub callback, the idle-eviction sweeper, and closeAllSubscribers
// race to do so. The producer-side fan-out continues to send via the
// non-blocking select so a closed channel doesn't panic the producer
// (a select-default treats a closed chan with no buffer as ready and
// would actually panic; in practice the subscribers slice has already
// dropped the entry by the time the channel is closed, so the producer
// never sees it).
func (s *subscriber) closeChan() {
	s.closeOnce.Do(func() { close(s.ch) })
}

// Subscribe registers a new listener and returns its event channel plus
// the events already buffered in the ring (for backfill). Channel
// capacity defaults to ringSize / 4 (min 16) so a momentarily slow client
// can absorb a small burst without dropping.
//
// The unsub function MUST be called when the subscriber is done — it
// removes the subscriber from the run's fan-out and closes the channel.
// Calling unsub more than once is safe (idempotent).
//
// If the run has already reached a terminal state (closeAllSubscribers
// already ran), the returned channel is pre-closed so the caller's
// for-range loop drains backfill and exits cleanly instead of leaking
// a goroutine on a never-closed channel. Mirrors SubscribeSince.
func (r *Run) Subscribe() (events <-chan Event, backfill []Event, unsub func()) {
	r.mu.Lock()
	if r.closed {
		// Run is terminal — hand back a pre-closed channel + the final
		// ring snapshot. Without this branch a Subscribe that races
		// closeAllSubscribers can land its subscriber into the now-zeroed
		// slice, leaving a chan that never sees a value AND never closes.
		ch := make(chan Event)
		close(ch)
		backfill = r.snapshotLocked()
		r.mu.Unlock()
		return ch, backfill, func() {}
	}
	cap := len(r.ring) / 4
	if cap < 16 {
		cap = 16
	}
	s := &subscriber{ch: make(chan Event, cap)}
	s.lastReadAt.Store(time.Now().UnixNano())
	r.subscribers = append(r.subscribers, s)
	backfill = r.snapshotLocked()
	r.mu.Unlock()

	var done atomic.Bool
	unsub = func() {
		if !done.CompareAndSwap(false, true) {
			return
		}
		r.mu.Lock()
		for i, x := range r.subscribers {
			if x == s {
				r.subscribers = append(r.subscribers[:i], r.subscribers[i+1:]...)
				break
			}
		}
		r.mu.Unlock()
		s.closeChan()
	}
	return s.ch, backfill, unsub
}

// SubscribeSince is like Subscribe but only returns events with Seq
// strictly greater than the supplied sequence. The boolean reports
// whether the requested seq is still recoverable from the ring; when
// false, the caller should fall back to a fresh subscription (or 410).
//
// If the run has already reached a terminal state (closeAllSubscribers
// already ran), the returned channel is pre-closed so the caller's
// for-range loop drains backfill and exits cleanly instead of hanging
// on a never-closed channel.
func (r *Run) SubscribeSince(seq int) (events <-chan Event, backfill []Event, recoverable bool, unsub func()) {
	backfill, recoverable = r.SnapshotSince(seq)
	if !recoverable {
		return nil, nil, false, func() {}
	}
	r.mu.Lock()
	if r.closed {
		// Run is terminal — hand back a pre-closed channel so the
		// caller emits [DONE] right after the backfill instead of
		// blocking on a phantom subscription.
		ch := make(chan Event)
		close(ch)
		r.mu.Unlock()
		return ch, backfill, true, func() {}
	}
	cap := len(r.ring) / 4
	if cap < 16 {
		cap = 16
	}
	s := &subscriber{ch: make(chan Event, cap)}
	s.lastReadAt.Store(time.Now().UnixNano())
	r.subscribers = append(r.subscribers, s)
	r.mu.Unlock()

	var done atomic.Bool
	unsub = func() {
		if !done.CompareAndSwap(false, true) {
			return
		}
		r.mu.Lock()
		for i, x := range r.subscribers {
			if x == s {
				r.subscribers = append(r.subscribers[:i], r.subscribers[i+1:]...)
				break
			}
		}
		r.mu.Unlock()
		s.closeChan()
	}
	return s.ch, backfill, true, unsub
}

// closeAllSubscribers tears down every subscriber's channel. Called on
// run completion / cancellation so the SSE writers exit their for-range
// loops cleanly.
func (r *Run) closeAllSubscribers() {
	r.mu.Lock()
	subs := r.subscribers
	r.subscribers = nil
	r.closed = true
	r.mu.Unlock()
	for _, s := range subs {
		s.closeChan()
	}
}

// evictIdleSubscribers drops every subscriber whose lastReadAt is older
// than now-timeout, closing its channel so the consumer's for-range
// exits. Returns the number of subscribers evicted so the caller can
// emit one metric per eviction (with the run's tenant label).
//
// timeout <= 0 disables eviction (the sweeper skips it). Idempotent
// against closed runs (a terminal run's subscribers were already closed
// by closeAllSubscribers; this returns 0).
func (r *Run) evictIdleSubscribers(now time.Time, timeout time.Duration) int {
	if timeout <= 0 {
		return 0
	}
	cutoff := now.Add(-timeout).UnixNano()
	r.mu.Lock()
	if r.closed || len(r.subscribers) == 0 {
		r.mu.Unlock()
		return 0
	}
	keep := make([]*subscriber, 0, len(r.subscribers))
	var evicted []*subscriber
	for _, s := range r.subscribers {
		if s.lastReadAt.Load() < cutoff {
			evicted = append(evicted, s)
		} else {
			keep = append(keep, s)
		}
	}
	r.subscribers = keep
	r.mu.Unlock()
	for _, s := range evicted {
		s.closeChan()
	}
	return len(evicted)
}
