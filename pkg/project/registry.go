package project

import (
	"sync"
	"sync/atomic"
	"time"
)

// DefaultRegistryTTL is the idle timeout for cached per-project components.
// Entries unused for longer than this are evicted on the next sweep.
const DefaultRegistryTTL = 5 * time.Minute

// EvictReason describes why a registry entry was removed. Surfaced via the
// OnEvict callback so observability can distinguish idle-driven sweeps from
// explicit teardown (project deletion) and shutdown.
type EvictReason string

const (
	// EvictReasonIdle means the sweep loop found the entry exceeded the TTL.
	EvictReasonIdle EvictReason = "idle"
	// EvictReasonExplicit is used by Evict — typically project deletion.
	EvictReasonExplicit EvictReason = "explicit"
	// EvictReasonClose is used during registry shutdown.
	EvictReasonClose EvictReason = "close"
)

// ComponentRegistry is a per-project cache for stateful components such as
// SessionStore or canvas.Executor that must survive across requests for the
// same project but should not multiply across all projects forever.
//
// Lookups are O(1) via map under RWMutex. A background goroutine sweeps for
// idle entries every TTL/4 ticks and evicts them, optionally calling the
// configured closer to release resources (e.g., flush pending writes).
//
// The registry is parameterised by component type so callers retrieve the
// concrete type without casting:
//
//	sessions := project.NewComponentRegistry(func(scope project.Scope) (*SessionStore, error) { ... })
//	defer sessions.Close()
//	store, err := sessions.Get(scope)
type ComponentRegistry[T any] struct {
	factory func(scope Scope) (T, error)
	closer  func(T)
	onEvict func(projectID string, reason EvictReason)
	ttl     time.Duration

	mu      sync.RWMutex
	entries map[string]*registryEntry[T]

	stop chan struct{}
	once sync.Once
}

type registryEntry[T any] struct {
	value    T
	lastUsed atomic.Int64 // unix nano
	refs     atomic.Int64 // outstanding Acquire holders; sweep skips when > 0
}

// RegistryOption configures a ComponentRegistry.
type RegistryOption[T any] func(*ComponentRegistry[T])

// WithTTL overrides the default idle timeout.
func WithTTL[T any](ttl time.Duration) RegistryOption[T] {
	return func(r *ComponentRegistry[T]) { r.ttl = ttl }
}

// WithCloser registers a cleanup function called when an entry is evicted.
// Use this to flush in-memory state or release file locks.
func WithCloser[T any](fn func(T)) RegistryOption[T] {
	return func(r *ComponentRegistry[T]) { r.closer = fn }
}

// WithOnEvict registers an observer fired on every eviction (sweep, Evict,
// Close). The callback receives the project ID and the reason. Useful for
// metrics / structured logging — runs synchronously after the entry is
// removed from the map but before (and independently of) the closer.
func WithOnEvict[T any](fn func(projectID string, reason EvictReason)) RegistryOption[T] {
	return func(r *ComponentRegistry[T]) { r.onEvict = fn }
}

// NewComponentRegistry constructs a registry. The factory is invoked the
// first time a projectID is requested and on every miss after eviction.
func NewComponentRegistry[T any](factory func(scope Scope) (T, error), opts ...RegistryOption[T]) *ComponentRegistry[T] {
	r := &ComponentRegistry[T]{
		factory: factory,
		ttl:     DefaultRegistryTTL,
		entries: make(map[string]*registryEntry[T]),
		stop:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}
	go r.sweepLoop()
	return r
}

// Get returns the cached component for scope.ProjectID, creating it via the
// factory on first call (or after eviction). Touches lastUsed on hits.
//
// Use Get for short-lived lookups where the returned value is not held past
// the call. For long-running operations (canvas execution, streaming RPCs),
// prefer Acquire so sweep cannot evict the entry while it is in use.
func (r *ComponentRegistry[T]) Get(scope Scope) (T, error) {
	v, release, err := r.Acquire(scope)
	if err != nil {
		return v, err
	}
	release()
	return v, nil
}

// Acquire returns the cached component plus a release function that the
// caller MUST invoke (typically `defer release()`). While unreleased entries
// are held, the sweeper skips them so a long-running operation cannot have
// its component evicted out from under it. The release callback is
// idempotent — calling it more than once is a no-op.
func (r *ComponentRegistry[T]) Acquire(scope Scope) (T, func(), error) {
	noop := func() {}
	if scope.ProjectID == "" {
		var zero T
		return zero, noop, ErrScopeMissing
	}
	e, err := r.getOrBuild(scope)
	if err != nil {
		var zero T
		return zero, noop, err
	}
	e.refs.Add(1)
	e.lastUsed.Store(time.Now().UnixNano())
	var released atomic.Bool
	return e.value, func() {
		if released.CompareAndSwap(false, true) {
			e.refs.Add(-1)
			e.lastUsed.Store(time.Now().UnixNano())
		}
	}, nil
}

// getOrBuild looks up or constructs the entry for scope.ProjectID. Caller
// must not hold r.mu when invoking it; the function manages locking itself.
func (r *ComponentRegistry[T]) getOrBuild(scope Scope) (*registryEntry[T], error) {
	r.mu.RLock()
	if e, ok := r.entries[scope.ProjectID]; ok {
		r.mu.RUnlock()
		return e, nil
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[scope.ProjectID]; ok {
		return e, nil
	}
	v, err := r.factory(scope)
	if err != nil {
		return nil, err
	}
	e := &registryEntry[T]{value: v}
	e.lastUsed.Store(time.Now().UnixNano())
	r.entries[scope.ProjectID] = e
	return e, nil
}

// Evict removes a single project's cached entry, calling closer if set.
// Useful for explicit teardown (e.g., after the project is deleted).
func (r *ComponentRegistry[T]) Evict(projectID string) {
	r.mu.Lock()
	e, ok := r.entries[projectID]
	if ok {
		delete(r.entries, projectID)
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	if r.onEvict != nil {
		r.onEvict(projectID, EvictReasonExplicit)
	}
	if r.closer != nil {
		r.closer(e.value)
	}
}

// Len returns the current number of cached entries (for tests/metrics).
func (r *ComponentRegistry[T]) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// Close stops the sweep goroutine and evicts every entry. Safe to call
// multiple times.
func (r *ComponentRegistry[T]) Close() {
	r.once.Do(func() {
		close(r.stop)
	})
	type victim struct {
		projectID string
		value     T
	}
	r.mu.Lock()
	victims := make([]victim, 0, len(r.entries))
	for k, e := range r.entries {
		victims = append(victims, victim{projectID: k, value: e.value})
		delete(r.entries, k)
	}
	r.mu.Unlock()
	for _, v := range victims {
		if r.onEvict != nil {
			r.onEvict(v.projectID, EvictReasonClose)
		}
		if r.closer != nil {
			r.closer(v.value)
		}
	}
}

func (r *ComponentRegistry[T]) sweepLoop() {
	interval := r.ttl / 4
	if interval < time.Second {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case now := <-t.C:
			r.sweep(now)
		}
	}
}

func (r *ComponentRegistry[T]) sweep(now time.Time) {
	cutoff := now.Add(-r.ttl).UnixNano()
	type victim struct {
		projectID string
		value     T
	}
	r.mu.Lock()
	var victims []victim
	for k, e := range r.entries {
		// Skip pinned entries — a long-running RPC still holds them. The
		// next sweep tick will revisit after the holder releases.
		if e.refs.Load() > 0 {
			continue
		}
		if e.lastUsed.Load() < cutoff {
			victims = append(victims, victim{projectID: k, value: e.value})
			delete(r.entries, k)
		}
	}
	r.mu.Unlock()
	for _, v := range victims {
		if r.onEvict != nil {
			r.onEvict(v.projectID, EvictReasonIdle)
		}
		if r.closer != nil {
			r.closer(v.value)
		}
	}
}
