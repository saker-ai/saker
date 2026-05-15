package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/saker-ai/saker/pkg/project"
)

// TestSessionsFor_LegacyFallback verifies that the per-project SessionStore
// registry returns the legacy h.sessions when there is no scope in ctx.
func TestSessionsFor_LegacyFallback(t *testing.T) {
	t.Parallel()
	legacy, err := NewSessionStore()
	if err != nil {
		t.Fatalf("legacy session store: %v", err)
	}
	h := &Handler{
		dataDir:  t.TempDir(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: legacy,
	}
	got := h.sessionsFor(context.Background())
	if got != legacy {
		t.Fatalf("expected legacy store when no scope in ctx")
	}
}

// TestSessionsFor_PerProjectIsolation drives two distinct project scopes
// through the registry and confirms they receive distinct SessionStore
// instances. This is the key invariant that makes the multi-tenant routing
// safe — leaking a SessionStore across projects would mean cross-project
// thread visibility.
func TestSessionsFor_PerProjectIsolation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	h := &Handler{
		dataDir: root,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	scopeA := project.Scope{ProjectID: "pA", Paths: project.BuildPaths(root, "pA")}
	scopeB := project.Scope{ProjectID: "pB", Paths: project.BuildPaths(root, "pB")}
	ctxA := project.WithScope(context.Background(), scopeA)
	ctxB := project.WithScope(context.Background(), scopeB)

	a1 := h.sessionsFor(ctxA)
	b1 := h.sessionsFor(ctxB)
	if a1 == nil || b1 == nil {
		t.Fatal("expected non-nil per-project session stores")
	}
	if a1 == b1 {
		t.Fatal("expected distinct session stores per project")
	}
	// Same scope twice should hit the cache.
	a2 := h.sessionsFor(ctxA)
	if a1 != a2 {
		t.Fatal("expected cached session store on repeat lookup")
	}
}

// TestCanvasExecutorFor_PerProjectIsolation mirrors the SessionStore test for
// the canvas Executor registry. Different projects must not share an Executor
// because Tracker run IDs are per-instance and DataDir routing depends on
// scope.Paths.
func TestCanvasExecutorFor_PerProjectIsolation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	h := &Handler{
		dataDir: root,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	scopeA := project.Scope{ProjectID: "pA", Paths: project.BuildPaths(root, "pA")}
	scopeB := project.Scope{ProjectID: "pB", Paths: project.BuildPaths(root, "pB")}
	ctxA := project.WithScope(context.Background(), scopeA)
	ctxB := project.WithScope(context.Background(), scopeB)

	a := h.canvasExecutorFor(ctxA)
	b := h.canvasExecutorFor(ctxB)
	if a == nil || b == nil {
		t.Fatal("expected non-nil per-project canvas executors")
	}
	if a == b {
		t.Fatal("expected distinct canvas executors per project")
	}
	// DataDir must reflect the per-project root.
	if a.DataDir != scopeA.Paths.Root {
		t.Fatalf("executor A DataDir = %s, want %s", a.DataDir, scopeA.Paths.Root)
	}
	if b.DataDir != scopeB.Paths.Root {
		t.Fatalf("executor B DataDir = %s, want %s", b.DataDir, scopeB.Paths.Root)
	}
	// Cache hit on repeat.
	if a2 := h.canvasExecutorFor(ctxA); a2 != a {
		t.Fatal("expected cached canvas executor on repeat lookup")
	}
	// Stop trackers so the test doesn't leak goroutines.
	t.Cleanup(func() {
		if a.Tracker != nil {
			a.Tracker.Stop()
		}
		if b.Tracker != nil {
			b.Tracker.Stop()
		}
	})
}
