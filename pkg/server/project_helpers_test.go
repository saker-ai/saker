package server

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/cinience/saker/pkg/project"
)

// newTestProjectStore opens a fresh sqlite-backed project store under t.TempDir.
// Each test gets its own DB so parallel runs don't collide.
func newTestProjectStore(t *testing.T) *project.Store {
	t.Helper()
	s, err := project.Open(project.Config{DSN: filepath.Join(t.TempDir(), "app.db")})
	if err != nil {
		t.Fatalf("open project store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// newProjectTestHandler constructs a minimal Handler suitable for project /
// invite / scope tests — no api.Runtime, no SessionStore, just the project
// store and the fields the dispatch path actually reads.
func newProjectTestHandler(t *testing.T) *Handler {
	t.Helper()
	store := newTestProjectStore(t)
	h := &Handler{
		dataDir:  t.TempDir(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		projects: store,
	}
	return h
}

// seedProjectUser inserts a User row in the project store and returns it.
func seedProjectUser(t *testing.T, s *project.Store, username string) *project.User {
	t.Helper()
	u, err := s.EnsureUserFromAuth(context.Background(), project.UserSourceLocal, username, username, username, "")
	if err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	return u
}

// withUser injects username + role into the request context the same way the
// auth middleware does. role is the global role ("admin" or "user").
func withUser(ctx context.Context, username, role string) context.Context {
	ctx = context.WithValue(ctx, userContextKey, username)
	ctx = context.WithValue(ctx, roleContextKey, role)
	return ctx
}

// rpcRequest builds a Request with the given method, id, and params map.
func rpcRequest(method string, id any, params map[string]any) Request {
	if params == nil {
		params = map[string]any{}
	}
	return Request{JSONRPC: "2.0", ID: id, Method: method, Params: params}
}
