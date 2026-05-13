package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cinience/saker/pkg/project"
	"github.com/stretchr/testify/require"
)

func TestPathsFor_LegacyFallback(t *testing.T) {
	h := &Handler{dataDir: "/tmp/legacy"}
	paths := h.pathsFor(context.Background())

	require.Equal(t, "/tmp/legacy", paths.Root)
	require.Equal(t, filepath.Join("/tmp/legacy", "canvas"), paths.CanvasDir)
	require.Equal(t, filepath.Join("/tmp/legacy", "memory"), paths.MemoryDir)
	require.Equal(t, "/tmp/legacy", paths.ConfigRoot)
}

func TestPathsFor_FromScope(t *testing.T) {
	h := &Handler{dataDir: "/tmp/legacy"}
	scope := project.Scope{
		UserID:    "u1",
		ProjectID: "p1",
		Paths: project.ProjectPaths{
			Root:       "/data/projects/p1",
			CanvasDir:  "/data/projects/p1/canvas",
			MemoryDir:  "/data/projects/p1/memory",
			ConfigRoot: "/data/projects/p1/config",
		},
	}
	ctx := project.WithScope(context.Background(), scope)
	paths := h.pathsFor(ctx)
	require.Equal(t, scope.Paths, paths)
}

func TestScopeFor_PresentAndAbsent(t *testing.T) {
	h := &Handler{}
	_, ok := h.scopeFor(context.Background())
	require.False(t, ok)

	scope := project.Scope{UserID: "u", ProjectID: "p"}
	ctx := project.WithScope(context.Background(), scope)
	got, ok := h.scopeFor(ctx)
	require.True(t, ok)
	require.Equal(t, "u", got.UserID)
	require.Equal(t, "p", got.ProjectID)
}
