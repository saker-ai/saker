package server

import (
	"context"
	"path/filepath"

	"github.com/cinience/saker/pkg/project"
)

// pathsFor returns the per-project paths for the request's scope. When no
// scope is present in ctx (no project store wired in, or the method is
// whitelisted) we fall back to the legacy single-project layout rooted at
// h.dataDir. This keeps embedded library callers working without forcing
// them to opt into the multi-tenant data layout.
func (h *Handler) pathsFor(ctx context.Context) project.ProjectPaths {
	if scope, ok := project.FromContext(ctx); ok {
		return scope.Paths
	}
	return project.ProjectPaths{
		Root:        h.dataDir,
		SessionsDir: filepath.Join(h.dataDir, "sessions"),
		CanvasDir:   filepath.Join(h.dataDir, "canvas"),
		MemoryDir:   filepath.Join(h.dataDir, "memory"),
		HistoryDir:  filepath.Join(h.dataDir, "history"),
		ConfigRoot:  h.dataDir,
	}
}

// scopeFor returns the request's project scope when present. Callers that
// only need the user/project IDs (e.g., to namespace a session ID) can use
// this helper instead of unpacking the scope themselves.
func (h *Handler) scopeFor(ctx context.Context) (project.Scope, bool) {
	return project.FromContext(ctx)
}
