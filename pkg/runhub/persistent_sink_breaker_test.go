package runhub

import (
	"testing"
	"time"
)

// TestSinkBreaker_Disabled asserts that threshold <= 0 means the
// breaker is permanently disabled — allow always returns true and
// record* are no-ops, so the wrapped store is called every time.
func TestSinkBreaker_Disabled(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	b := newSinkBreaker(0, time.Second, hooks)

	if !b.allow() {
		t.Fatal("disabled breaker must always allow")
	}
	for i := 0; i < 100; i++ {
		b.recordFailure()
	}
	if !b.allow() {
		t.Fatal("disabled breaker stayed closed despite failures")
	}
	if got := b.State(); got != breakerClosed {
		t.Fatalf("disabled breaker state = %v, want %v", got, breakerClosed)
	}
	// Disabled breaker still emits the initial state hook so dashboards
	// show "closed" from boot, but no transitions should fire.
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if hooks.breakerStateLast != "closed" {
		t.Errorf("initial state hook didn't fire: got %q want closed", hooks.breakerStateLast)
	}
	if len(hooks.breakerTransitions) != 0 {
		t.Errorf("disabled breaker fired transitions: %v", hooks.breakerTransitions)
	}
}

// TestSinkBreaker_TripsOnConsecutiveFailures asserts that exactly
// `threshold` consecutive failures flip Closed → Open, and that a
// success in between resets the counter.
func TestSinkBreaker_TripsOnConsecutiveFailures(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	b := newSinkBreaker(3, time.Hour, hooks)

	// 2 failures shouldn't trip yet.
	b.recordFailure()
	b.recordFailure()
	if b.State() != breakerClosed {
		t.Fatalf("state after 2 failures = %v, want %v", b.State(), breakerClosed)
	}

	// One success resets the counter — even with 5 more failures we
	// only count the post-reset run.
	b.recordSuccess()
	for i := 0; i < 2; i++ {
		b.recordFailure()
	}
	if b.State() != breakerClosed {
		t.Fatalf("state after success+2 failures = %v, want %v (counter reset by success)", b.State(), breakerClosed)
	}

	// Third post-success failure crosses the threshold.
	b.recordFailure()
	if b.State() != breakerOpen {
		t.Fatalf("state after threshold failures = %v, want %v", b.State(), breakerOpen)
	}
	if !b.allow() == false {
		// allow() should return false in Open with a 1h cooldown.
	}
	if b.allow() {
		t.Fatal("Open breaker allowed call before cooldown elapsed")
	}

	// Verify the transition was recorded.
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if len(hooks.breakerTransitions) != 1 || hooks.breakerTransitions[0] != "closed->open" {
		t.Errorf("expected one closed->open transition, got %v", hooks.breakerTransitions)
	}
}

// TestSinkBreaker_HalfOpenRecovers asserts the Open → HalfOpen → Closed
// recovery path. Uses an injected `now` clock so we don't rely on
// wall-clock sleep.
func TestSinkBreaker_HalfOpenRecovers(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	b := newSinkBreaker(2, 100*time.Millisecond, hooks)

	// Inject a controllable clock.
	t0 := time.Now()
	clock := t0
	b.now = func() time.Time { return clock }

	// Trip Open.
	b.recordFailure()
	b.recordFailure()
	if b.State() != breakerOpen {
		t.Fatalf("expected Open after 2 failures, got %v", b.State())
	}
	if b.allow() {
		t.Fatal("Open breaker allowed call before cooldown elapsed")
	}

	// Advance clock past cooldown — allow() should now flip to HalfOpen.
	clock = t0.Add(150 * time.Millisecond)
	if !b.allow() {
		t.Fatal("expected allow to return true after cooldown")
	}
	if b.State() != breakerHalfOpen {
		t.Fatalf("expected HalfOpen after cooldown allow, got %v", b.State())
	}
	// Second allow() during HalfOpen probe-in-flight must return false
	// to keep the probe single-threaded.
	if b.allow() {
		t.Fatal("expected allow to return false while HalfOpen probe in flight")
	}

	// Probe succeeds — breaker closes.
	b.recordSuccess()
	if b.State() != breakerClosed {
		t.Fatalf("expected Closed after successful HalfOpen probe, got %v", b.State())
	}

	// Subsequent failure run starts fresh from 0.
	b.recordFailure()
	if b.State() != breakerClosed {
		t.Fatalf("counter should have reset post-recovery, got %v", b.State())
	}

	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	want := []string{"closed->open", "open->half_open", "half_open->closed"}
	if len(hooks.breakerTransitions) != len(want) {
		t.Fatalf("transitions = %v, want %v", hooks.breakerTransitions, want)
	}
	for i, v := range want {
		if hooks.breakerTransitions[i] != v {
			t.Fatalf("transition[%d] = %q, want %q", i, hooks.breakerTransitions[i], v)
		}
	}
}

// TestSinkBreaker_HalfOpenRelapses asserts that a probe failure in
// HalfOpen flips back to Open (with a fresh cooldown), not Closed.
func TestSinkBreaker_HalfOpenRelapses(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	b := newSinkBreaker(1, 50*time.Millisecond, hooks)

	t0 := time.Now()
	clock := t0
	b.now = func() time.Time { return clock }

	b.recordFailure() // trips immediately (threshold=1)
	if b.State() != breakerOpen {
		t.Fatalf("expected Open, got %v", b.State())
	}

	clock = t0.Add(100 * time.Millisecond)
	if !b.allow() {
		t.Fatal("expected allow after cooldown")
	}
	if b.State() != breakerHalfOpen {
		t.Fatalf("expected HalfOpen, got %v", b.State())
	}

	// Probe FAILS — should re-open and restart cooldown.
	b.recordFailure()
	if b.State() != breakerOpen {
		t.Fatalf("expected Open after probe failure, got %v", b.State())
	}
	// Without advancing clock, allow() still says no (cooldown reset).
	if b.allow() {
		t.Fatal("Open after relapse should not allow within fresh cooldown")
	}

	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	want := []string{"closed->open", "open->half_open", "half_open->open"}
	if len(hooks.breakerTransitions) != len(want) {
		t.Fatalf("transitions = %v, want %v", hooks.breakerTransitions, want)
	}
	for i, v := range want {
		if hooks.breakerTransitions[i] != v {
			t.Fatalf("transition[%d] = %q, want %q", i, hooks.breakerTransitions[i], v)
		}
	}
}

// TestSinkBreaker_LatchedOpen asserts that cooldown=0 with a non-zero
// threshold means the breaker stays Open until process restart — no
// auto-recovery. This is the operator opt-in fail-fast posture.
func TestSinkBreaker_LatchedOpen(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	b := newSinkBreaker(1, 0, hooks)
	b.recordFailure()
	if b.State() != breakerOpen {
		t.Fatalf("expected Open, got %v", b.State())
	}
	for i := 0; i < 10; i++ {
		if b.allow() {
			t.Fatalf("latched-open breaker allowed call on iter %d", i)
		}
	}
}
