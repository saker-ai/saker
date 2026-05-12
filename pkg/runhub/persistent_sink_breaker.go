package runhub

import (
	"sync"
	"time"
)

// breakerState is the discrete operational state of sinkBreaker.
type breakerState int32

const (
	breakerClosed   breakerState = 0 // store calls flow through
	breakerHalfOpen breakerState = 1 // single probe call permitted
	breakerOpen     breakerState = 2 // store calls suppressed until cooldown
)

func (s breakerState) String() string {
	switch s {
	case breakerClosed:
		return "closed"
	case breakerHalfOpen:
		return "half_open"
	case breakerOpen:
		return "open"
	default:
		return "unknown"
	}
}

// sinkBreaker is a tiny three-state circuit breaker that wraps the
// PersistentHub batch writer's store calls. It exists to stop a
// failing store from monopolizing CPU + log volume during a long
// outage:
//
//   - Closed   — every flush calls the store as normal. A run of
//     `threshold` consecutive failures trips it Open.
//   - Open     — flushes skip the store entirely (counted via
//     OnSinkBreakerSkipped); after `cooldown` elapses the next allow()
//     call returns true and trips the breaker HalfOpen.
//   - HalfOpen — a single probe flush is permitted. Success returns
//     to Closed (with the failure run reset); failure trips back to
//     Open and restarts the cooldown.
//
// All operations are guarded by a single mutex — this lives in the
// batch-writer goroutine's hot path but at single-flush granularity,
// not per-event, so the lock cost is negligible compared to the
// store I/O it gates.
//
// A breaker built with threshold <= 0 is permanently disabled
// (allow always returns true; recordSuccess / recordFailure are
// no-ops). This mirrors Config.SinkBreakerThreshold == 0 == "off".
type sinkBreaker struct {
	threshold int
	cooldown  time.Duration
	now       func() time.Time
	hooks     MetricsHooks

	mu          sync.Mutex
	state       breakerState
	consecFail  int
	openedAt    time.Time
	probeInUse  bool // true between allow() returning true in HalfOpen and the next record*
	enabled     bool
}

// newSinkBreaker builds a breaker. threshold <= 0 disables the
// breaker entirely (allow → true forever; record* no-ops). cooldown
// <= 0 with threshold > 0 disables auto-recovery (the breaker latches
// Open until restart) — operators can set this for fail-fast envs but
// the production default is a finite cooldown.
func newSinkBreaker(threshold int, cooldown time.Duration, hooks MetricsHooks) *sinkBreaker {
	if hooks == nil {
		hooks = NopMetricsHooks()
	}
	b := &sinkBreaker{
		threshold: threshold,
		cooldown:  cooldown,
		now:       time.Now,
		hooks:     hooks,
		state:     breakerClosed,
		enabled:   threshold > 0,
	}
	// Surface the initial state to dashboards immediately so a freshly
	// started process shows "closed" rather than "no data".
	hooks.OnSinkBreakerState(breakerClosed.String())
	return b
}

// allow returns true when the caller may execute the wrapped store
// call. When false, the caller MUST skip the store call and emit a
// skipped metric (caller's responsibility, since it knows the row
// count).
//
// In Closed state allow always returns true. In Open it returns true
// only after the cooldown has elapsed — when it does, the breaker
// transitions to HalfOpen and the caller's next recordSuccess /
// recordFailure determines whether to fully recover or relapse. In
// HalfOpen, a probe is in flight; further allow calls return false
// to keep the probe single-threaded (the batch-writer goroutine is
// the only caller, but this future-proofs against a multi-writer
// refactor).
func (b *sinkBreaker) allow() bool {
	if b == nil || !b.enabled {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case breakerClosed:
		return true
	case breakerHalfOpen:
		// A probe is already in flight — suppress further attempts
		// until that probe records its outcome.
		return !b.probeInUse
	case breakerOpen:
		if b.cooldown <= 0 {
			// Latched open until restart. Operator opt-in for fail-fast.
			return false
		}
		if b.now().Sub(b.openedAt) < b.cooldown {
			return false
		}
		// Cooldown elapsed — transition to HalfOpen and let this caller
		// run the probe.
		b.transitionLocked(breakerHalfOpen)
		b.probeInUse = true
		return true
	}
	return false
}

// recordSuccess reports a successful store call. In Closed it resets
// the consecutive-failure counter. In HalfOpen it closes the breaker.
// Open is impossible at record-time because allow() must have
// returned true to reach the call site.
func (b *sinkBreaker) recordSuccess() {
	if b == nil || !b.enabled {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecFail = 0
	if b.state == breakerHalfOpen {
		b.probeInUse = false
		b.transitionLocked(breakerClosed)
	}
}

// recordFailure reports a failed store call. In Closed, increments
// the consecutive failure counter and trips Open if the counter
// reaches threshold. In HalfOpen, immediately re-opens (the probe
// failed) and restarts the cooldown.
func (b *sinkBreaker) recordFailure() {
	if b == nil || !b.enabled {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == breakerHalfOpen {
		b.probeInUse = false
		b.openedAt = b.now()
		b.transitionLocked(breakerOpen)
		return
	}
	if b.state == breakerClosed {
		b.consecFail++
		if b.consecFail >= b.threshold {
			b.openedAt = b.now()
			b.transitionLocked(breakerOpen)
		}
	}
}

// State returns the breaker's current state. Mainly for tests and
// debugging — production code should rely on allow() / record* to
// drive behavior.
func (b *sinkBreaker) State() breakerState {
	if b == nil || !b.enabled {
		return breakerClosed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// transitionLocked changes state and emits both the gauge update and
// the transition counter. Caller MUST hold b.mu.
func (b *sinkBreaker) transitionLocked(to breakerState) {
	if b.state == to {
		return
	}
	from := b.state
	b.state = to
	b.hooks.OnSinkBreakerState(to.String())
	b.hooks.OnSinkBreakerTransition(from.String(), to.String())
}
