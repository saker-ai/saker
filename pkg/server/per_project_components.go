package server

import (
	"context"
	"strings"
	"sync"

	"github.com/cinience/saker/pkg/apps"
	"github.com/cinience/saker/pkg/canvas"
	"github.com/cinience/saker/pkg/project"
)

// canvasRunPin is a small state machine that closes the race between
// "RunAsync returns runId" (registrar wants to record release) and
// "canvas/run-finished" (notifier wants to drain release). Whichever side
// runs first records its presence via mu+completed; the second side sees
// completed and runs the release immediately.
type canvasRunPin struct {
	mu        sync.Mutex
	release   func()
	completed bool
}

// initPerProjectRegistries lazily constructs the SessionStore and canvas
// Executor registries the first time the multi-tenant path is hit. We don't
// build them in newHandler so single-project deployments pay no cost. Caller
// must hold h.mu.
func (h *Handler) initPerProjectRegistries() {
	if h.sessionRegistry == nil {
		h.sessionRegistry = project.NewComponentRegistry(
			func(scope project.Scope) (*SessionStore, error) {
				store, err := NewSessionStore()
				if err != nil {
					return nil, err
				}
				if h.runtime != nil {
					cs := h.runtime.ConversationStore()
					store.AttachConvTee(newConvTee(cs, scope.ProjectID, h.logger))
					if cs != nil {
						_ = store.LoadFromConversation(cs, scope.ProjectID)
					}
				}
				return store, nil
			},
			project.WithCloser[*SessionStore](func(s *SessionStore) {
				// SessionStore has no explicit Close; persist is synchronous so
				// dropping the reference is safe. Hook left in place so a future
				// flush-on-evict implementation has somewhere to live.
				_ = s
			}),
			project.WithOnEvict[*SessionStore](func(projectID string, reason project.EvictReason) {
				// Surface evictions in logs so operators can correlate
				// memory drops with project activity. Idle is the most
				// frequent reason; explicit signals project deletion;
				// close fires only at server shutdown.
				h.logger.Info("project component evicted",
					"component", "session_store",
					"project_id", projectID,
					"reason", string(reason))
			}),
		)
	}
	if h.canvasExecRegistry == nil {
		h.canvasExecRegistry = project.NewComponentRegistry(
			func(scope project.Scope) (*canvas.Executor, error) {
				exec := &canvas.Executor{
					Runtime: canvasRuntimeAdapter{rt: h.runtime},
					DataDir: scope.Paths.Root,
					Tracker: canvas.NewRunTracker(),
					Notify: func(threadID, method string, params map[string]any) {
						// Drain run pins on terminal events so the registry
						// can evict the executor once truly idle. Pins live
						// in handler state because the runID is created by
						// Tracker.Create, which we don't see at factory time.
						if method == "canvas/run-finished" {
							if runID, _ := params["runId"].(string); runID != "" {
								h.markCanvasRunFinished(runID)
							}
							// Apps temp-thread GC: when the runner injected
							// inputs into a cloned canvas and saved it as
							// app-run-{uuid}.json, drain the file now that
							// the executor has reached a terminal state.
							if strings.HasPrefix(threadID, "app-run-") {
								h.drainAppTempThread(threadID)
							}
						}
						h.notifySubscribers(threadID, method, params)
					},
					Logger: h.logger,
				}
				return exec, nil
			},
			project.WithCloser[*canvas.Executor](func(e *canvas.Executor) {
				if e != nil && e.Tracker != nil {
					e.Tracker.Stop()
				}
			}),
			project.WithOnEvict[*canvas.Executor](func(projectID string, reason project.EvictReason) {
				h.logger.Info("project component evicted",
					"component", "canvas_executor",
					"project_id", projectID,
					"reason", string(reason))
			}),
		)
	}
}

// sessionsFor returns the SessionStore that should service this request.
// When the request carries a project.Scope (multi-tenant path is active),
// it returns a per-project SessionStore from the registry. Otherwise it
// returns the legacy h.sessions instance — preserving every existing
// single-project call site without modification.
func (h *Handler) sessionsFor(ctx context.Context) *SessionStore {
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.sessions
	}
	h.mu.Lock()
	h.initPerProjectRegistries()
	reg := h.sessionRegistry
	h.mu.Unlock()
	store, err := reg.Get(scope)
	if err != nil {
		// Building the per-project store failed (e.g., mkdir denied). Fall
		// back to legacy so the request still has a working store rather
		// than crashing — the error surfaces in logs but not as a panic.
		h.logger.Error("per-project session store init failed",
			"project_id", scope.ProjectID, "error", err)
		return h.sessions
	}
	return store
}

// canvasExecutorFor returns the canvas Executor for this request, scoped
// per project when the scope is present, or the lazily-initialised legacy
// executor (ensureCanvasExecutor) otherwise.
func (h *Handler) canvasExecutorFor(ctx context.Context) *canvas.Executor {
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.ensureCanvasExecutor()
	}
	h.mu.Lock()
	h.initPerProjectRegistries()
	reg := h.canvasExecRegistry
	h.mu.Unlock()
	exec, err := reg.Get(scope)
	if err != nil {
		h.logger.Error("per-project canvas executor init failed",
			"project_id", scope.ProjectID, "error", err)
		return h.ensureCanvasExecutor()
	}
	return exec
}

// registerCanvasRunPin records the release callback for a freshly started
// canvas run. The mirror call is markCanvasRunFinished, fired by the
// Notify intercept on canvas/run-finished. The pair handles either ordering:
// if the run finished before this caller could store the pin (tiny canvases
// can complete inside a few microseconds), markCanvasRunFinished will have
// already left a tombstone and we release immediately.
func (h *Handler) registerCanvasRunPin(runID string, release func()) {
	if runID == "" {
		release()
		return
	}
	pin := &canvasRunPin{release: release}
	v, loaded := h.canvasRunPins.LoadOrStore(runID, pin)
	if !loaded {
		return
	}
	existing, ok := v.(*canvasRunPin)
	if !ok {
		release()
		return
	}
	existing.mu.Lock()
	if existing.completed {
		// Notify already fired — drain ourselves.
		h.canvasRunPins.Delete(runID)
		existing.mu.Unlock()
		release()
		return
	}
	existing.release = release
	existing.mu.Unlock()
}

// markCanvasRunFinished is invoked from the executor Notify hook when a run
// reaches a terminal state. If a pin is already registered we drain it; if
// the registrar hasn't reached registerCanvasRunPin yet, we leave a
// tombstone so the registrar releases instead.
func (h *Handler) markCanvasRunFinished(runID string) {
	if runID == "" {
		return
	}
	pin := &canvasRunPin{completed: true}
	v, loaded := h.canvasRunPins.LoadOrStore(runID, pin)
	if !loaded {
		// Tombstone now stored; registrar will see completed and release.
		return
	}
	existing, ok := v.(*canvasRunPin)
	if !ok {
		return
	}
	existing.mu.Lock()
	existing.completed = true
	release := existing.release
	existing.release = nil
	existing.mu.Unlock()
	if release != nil {
		h.canvasRunPins.Delete(runID)
		release()
	}
}

// appsStoreFor returns the apps.Store rooted at the request's per-project
// data directory (or the legacy single-project root when no scope is
// present). Store is stateless — a fresh value type wrapping the root
// path is cheap, so we don't bother with a registry/cache.
func (h *Handler) appsStoreFor(ctx context.Context) *apps.Store {
	return apps.New(h.pathsFor(ctx).Root)
}

// appsRunnerFor composes the per-request apps.Runner: it bundles the
// scope-resolved apps.Store, the scope-resolved canvas.Executor, and the
// scope's data root (which the runner uses to canvas.Save the cloned
// temp thread before dispatch). All three pointers are cheap to combine
// per call; no caching needed.
//
// OnTempThread wires h.recordAppTempThread so the Notify hook
// (canvas/run-finished) can drain the temp file once the run terminates.
func (h *Handler) appsRunnerFor(ctx context.Context) *apps.Runner {
	r := apps.NewRunner(h.appsStoreFor(ctx), h.canvasExecutorFor(ctx), h.pathsFor(ctx).Root)
	r.OnTempThread = h.recordAppTempThread
	return r
}

// canvasExecutorAcquireFor is the long-running variant of canvasExecutorFor.
// It pins the per-project executor in the registry until the returned release
// is called. Callers that kick off a RunAsync should pair Acquire with a
// release recorded against the resulting runId so canvas/run-finished can
// drain it. When no project scope is present, the legacy executor is returned
// with a no-op release (legacy executor is never in the registry).
func (h *Handler) canvasExecutorAcquireFor(ctx context.Context) (*canvas.Executor, func()) {
	noop := func() {}
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.ensureCanvasExecutor(), noop
	}
	h.mu.Lock()
	h.initPerProjectRegistries()
	reg := h.canvasExecRegistry
	h.mu.Unlock()
	exec, release, err := reg.Acquire(scope)
	if err != nil {
		h.logger.Error("per-project canvas executor acquire failed",
			"project_id", scope.ProjectID, "error", err)
		return h.ensureCanvasExecutor(), noop
	}
	return exec, release
}
