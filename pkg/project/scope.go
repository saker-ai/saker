package project

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
)

// ProjectPaths is the per-project on-disk layout. All paths are absolute and
// derived from the server's BaseDataDir + the project ID. They are populated
// once when the Scope is built and stay stable for the request.
type ProjectPaths struct {
	// Root is `<base>/projects/<projectID>`.
	Root string
	// CanvasDir holds per-thread canvas document JSON.
	CanvasDir string
	// MemoryDir is the project-scoped memory directory.
	MemoryDir string
	// ConfigRoot is the per-project `.saker`-style config root used by
	// the profile package.
	ConfigRoot string
}

// BuildPaths returns the canonical ProjectPaths for (baseDataDir, projectID).
// baseDataDir is the server's data root (typically `<projectRoot>/.saker` or
// `<userHome>/.saker/server`). The function is pure — it does not touch the
// filesystem; callers ensure directories exist on first use.
func BuildPaths(baseDataDir, projectID string) ProjectPaths {
	root := filepath.Join(baseDataDir, "projects", projectID)
	return ProjectPaths{
		Root:       root,
		CanvasDir:  filepath.Join(root, "canvas"),
		MemoryDir:  filepath.Join(root, "memory"),
		ConfigRoot: filepath.Join(root, "config"),
	}
}

// Scope is the per-request projection of "who is acting on which project".
// It is built by middleware after authentication and stored in the request
// context. Handlers retrieve it via FromContext.
type Scope struct {
	UserID    string
	Username  string
	ProjectID string
	Role      Role
	Paths     ProjectPaths
}

// HasRole reports whether the scope's role meets or exceeds min.
func (s Scope) HasRole(min Role) bool { return s.Role.AtLeast(min) }

// scopeContextKey is unexported so callers must use WithScope/FromContext.
type scopeContextKey struct{}

// WithScope returns a copy of ctx with scope attached.
func WithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeContextKey{}, scope)
}

// FromContext extracts the Scope from ctx. The bool reports whether one was
// present — handlers should treat a false here as "no project bound" and
// either reject the request or fall back to a default project per their
// own policy.
func FromContext(ctx context.Context) (Scope, bool) {
	s, ok := ctx.Value(scopeContextKey{}).(Scope)
	return s, ok
}

// FromContextOrError returns the Scope from context, or an error if no scope is found.
func FromContextOrError(ctx context.Context) (Scope, error) {
	s, ok := FromContext(ctx)
	if !ok {
		return Scope{}, fmt.Errorf("project: no scope in context")
	}
	return s, nil
}

// MustFromContext is the convenience for handlers that have already been
// gated by middleware and know the scope must exist. Returns zero-value Scope
// and logs a warning if scope is missing instead of panicking.
func MustFromContext(ctx context.Context) Scope {
	s, ok := FromContext(ctx)
	if !ok {
		slog.Warn("project: MustFromContext called with no scope in context")
		return Scope{}
	}
	return s
}
