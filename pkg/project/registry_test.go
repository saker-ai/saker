package project

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistry_GetOrCreate(t *testing.T) {
	t.Parallel()
	var n atomic.Int32
	reg := NewComponentRegistry(func(scope Scope) (string, error) {
		n.Add(1)
		return "v-" + scope.ProjectID, nil
	})
	defer reg.Close()

	scope := Scope{ProjectID: "p1"}
	v1, err := reg.Get(scope)
	if err != nil {
		t.Fatalf("get1: %v", err)
	}
	v2, err := reg.Get(scope)
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if v1 != v2 {
		t.Fatalf("expected same instance, got %q vs %q", v1, v2)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("factory called %d times, want 1", got)
	}
}

func TestRegistry_DistinctProjects(t *testing.T) {
	t.Parallel()
	reg := NewComponentRegistry(func(scope Scope) (string, error) {
		return scope.ProjectID, nil
	})
	defer reg.Close()
	v1, _ := reg.Get(Scope{ProjectID: "a"})
	v2, _ := reg.Get(Scope{ProjectID: "b"})
	if v1 == v2 {
		t.Fatalf("expected distinct values, got %q == %q", v1, v2)
	}
	if got := reg.Len(); got != 2 {
		t.Fatalf("len = %d, want 2", got)
	}
}

func TestRegistry_ConcurrentSingleflight(t *testing.T) {
	t.Parallel()
	var n atomic.Int32
	reg := NewComponentRegistry(func(scope Scope) (string, error) {
		// Slow factory to maximise the race window.
		time.Sleep(20 * time.Millisecond)
		n.Add(1)
		return "v", nil
	})
	defer reg.Close()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := reg.Get(Scope{ProjectID: "shared"}); err != nil {
				t.Errorf("get: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := n.Load(); got != 1 {
		t.Fatalf("factory called %d times, want 1", got)
	}
}

func TestRegistry_Eviction(t *testing.T) {
	t.Parallel()
	var closed atomic.Int32
	reg := NewComponentRegistry(
		func(scope Scope) (string, error) { return scope.ProjectID, nil },
		WithTTL[string](80*time.Millisecond),
		WithCloser[string](func(string) { closed.Add(1) }),
	)
	defer reg.Close()
	if _, err := reg.Get(Scope{ProjectID: "p"}); err != nil {
		t.Fatalf("get: %v", err)
	}
	// Wait long enough for at least one sweep tick (TTL/4 = 20ms) past TTL.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reg.Len() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if reg.Len() != 0 {
		t.Fatalf("entry not evicted, len=%d", reg.Len())
	}
	if closed.Load() != 1 {
		t.Fatalf("closer called %d times, want 1", closed.Load())
	}
}

func TestRegistry_Close(t *testing.T) {
	t.Parallel()
	var closed atomic.Int32
	reg := NewComponentRegistry(
		func(scope Scope) (string, error) { return "", nil },
		WithCloser[string](func(string) { closed.Add(1) }),
	)
	_, _ = reg.Get(Scope{ProjectID: "a"})
	_, _ = reg.Get(Scope{ProjectID: "b"})
	reg.Close()
	if reg.Len() != 0 {
		t.Fatalf("entries left: %d", reg.Len())
	}
	if got := closed.Load(); got != 2 {
		t.Fatalf("closer called %d, want 2", got)
	}
	// Idempotent.
	reg.Close()
}

func TestRegistry_AcquireBlocksEviction(t *testing.T) {
	t.Parallel()
	var closed atomic.Int32
	reg := NewComponentRegistry(
		func(scope Scope) (string, error) { return scope.ProjectID, nil },
		WithTTL[string](80*time.Millisecond),
		WithCloser[string](func(string) { closed.Add(1) }),
	)
	defer reg.Close()

	_, release, err := reg.Acquire(Scope{ProjectID: "p"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Hold the entry past TTL — sweep must skip it because refs > 0.
	time.Sleep(250 * time.Millisecond)
	if reg.Len() != 1 {
		t.Fatalf("entry evicted while held, len=%d", reg.Len())
	}
	if closed.Load() != 0 {
		t.Fatalf("closer called %d times while held, want 0", closed.Load())
	}
	release()
	// After release, the next sweep window should reclaim it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reg.Len() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if reg.Len() != 0 {
		t.Fatalf("entry not evicted after release, len=%d", reg.Len())
	}
	if closed.Load() != 1 {
		t.Fatalf("closer called %d times, want 1", closed.Load())
	}
}

func TestRegistry_AcquireReleaseIdempotent(t *testing.T) {
	t.Parallel()
	reg := NewComponentRegistry(func(scope Scope) (string, error) { return "v", nil })
	defer reg.Close()
	_, release, err := reg.Acquire(Scope{ProjectID: "p"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Multiple release calls must not drive refcount negative.
	release()
	release()
	release()
	// Acquire again — fresh refcount should still increment from 0.
	_, release2, err := reg.Acquire(Scope{ProjectID: "p"})
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	release2()
}

func TestRegistry_EvictExplicit(t *testing.T) {
	t.Parallel()
	var closed atomic.Int32
	reg := NewComponentRegistry(
		func(scope Scope) (string, error) { return "v", nil },
		WithCloser[string](func(string) { closed.Add(1) }),
	)
	defer reg.Close()
	_, _ = reg.Get(Scope{ProjectID: "x"})
	reg.Evict("x")
	if reg.Len() != 0 {
		t.Fatalf("len = %d", reg.Len())
	}
	if closed.Load() != 1 {
		t.Fatalf("closer called %d, want 1", closed.Load())
	}
	// Re-Evict: no-op.
	reg.Evict("x")
}

// TestRegistry_OnEvictReasons exercises the WithOnEvict observer across all
// three eviction paths (sweep / explicit Evict / Close). Each path must
// surface the correct reason exactly once per evicted entry.
func TestRegistry_OnEvictReasons(t *testing.T) {
	t.Parallel()
	type observation struct {
		projectID string
		reason    EvictReason
	}
	var (
		mu      sync.Mutex
		observe []observation
	)
	record := func(projectID string, reason EvictReason) {
		mu.Lock()
		observe = append(observe, observation{projectID, reason})
		mu.Unlock()
	}
	count := func(reason EvictReason, projectID string) int {
		mu.Lock()
		defer mu.Unlock()
		var n int
		for _, o := range observe {
			if o.reason == reason && o.projectID == projectID {
				n++
			}
		}
		return n
	}

	reg := NewComponentRegistry(
		func(scope Scope) (string, error) { return scope.ProjectID, nil },
		WithTTL[string](80*time.Millisecond),
		WithOnEvict[string](record),
	)

	// Sweep path: prime "p-idle" and let TTL pass.
	if _, err := reg.Get(Scope{ProjectID: "p-idle"}); err != nil {
		t.Fatalf("get p-idle: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count(EvictReasonIdle, "p-idle") == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := count(EvictReasonIdle, "p-idle"); got != 1 {
		t.Fatalf("idle observation = %d, want 1", got)
	}

	// Explicit path: Evict "p-explicit" and assert reason.
	if _, err := reg.Get(Scope{ProjectID: "p-explicit"}); err != nil {
		t.Fatalf("get p-explicit: %v", err)
	}
	reg.Evict("p-explicit")
	if got := count(EvictReasonExplicit, "p-explicit"); got != 1 {
		t.Fatalf("explicit observation = %d, want 1", got)
	}
	// Re-Evict no-op must not double-count.
	reg.Evict("p-explicit")
	if got := count(EvictReasonExplicit, "p-explicit"); got != 1 {
		t.Fatalf("re-evict double-counted: got %d, want 1", got)
	}

	// Close path: prime "p-close" then Close.
	if _, err := reg.Get(Scope{ProjectID: "p-close"}); err != nil {
		t.Fatalf("get p-close: %v", err)
	}
	reg.Close()
	if got := count(EvictReasonClose, "p-close"); got != 1 {
		t.Fatalf("close observation = %d, want 1", got)
	}
}
