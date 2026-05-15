//go:build govm && cgo

package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/api"
	govmclient "github.com/godeps/govm/pkg/client"
)

func TestRunBuildsGovmSandboxOptions(t *testing.T) {
	root := t.TempDir()
	app := newTestApp()
	app.ValidateGovmRuntime = func(api.GovmOptions) error { return nil }

	var captured api.Options
	app.runtimeFactory = func(_ context.Context, opts api.Options) (RuntimeClient, error) {
		captured = opts
		return &fakeRuntime{
			runFn: func(_ context.Context, req api.Request) (*api.Response, error) {
				if req.SessionID == "" {
					t.Fatal("expected generated session id")
				}
				return &api.Response{Mode: api.ModeContext{EntryPoint: api.EntryPointCLI}, Result: &api.Result{Output: "ok"}}, nil
			},
		}, nil
	}

	if err := app.Run([]string{"--project", root, "--prompt", "hi", "--sandbox-backend=govm"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}

	if captured.Sandbox.Type != "govm" {
		t.Fatalf("sandbox type = %q", captured.Sandbox.Type)
	}
	if captured.Sandbox.Govm == nil || !captured.Sandbox.Govm.Enabled {
		t.Fatal("expected govm config enabled")
	}
	if captured.Sandbox.Govm.RuntimeHome != filepath.Join(root, ".govm") {
		t.Fatalf("runtime home = %q", captured.Sandbox.Govm.RuntimeHome)
	}
	if captured.Sandbox.Govm.OfflineImage != "py312-alpine" {
		t.Fatalf("offline image = %q", captured.Sandbox.Govm.OfflineImage)
	}
	if !captured.Sandbox.Govm.AutoCreateSessionWorkspace {
		t.Fatal("expected auto session workspace enabled")
	}
	if captured.Sandbox.Govm.SessionWorkspaceBase != filepath.Join(root, "workspace") {
		t.Fatalf("workspace base = %q", captured.Sandbox.Govm.SessionWorkspaceBase)
	}
	if len(captured.Sandbox.Govm.Mounts) != 1 {
		t.Fatalf("expected one project mount, got %+v", captured.Sandbox.Govm.Mounts)
	}
	mount := captured.Sandbox.Govm.Mounts[0]
	if mount.HostPath != root || mount.GuestPath != "/project" || !mount.ReadOnly {
		t.Fatalf("unexpected project mount %+v", mount)
	}
}

func TestRunGovmProjectMountOff(t *testing.T) {
	app := newTestApp()
	app.ValidateGovmRuntime = func(api.GovmOptions) error { return nil }

	var captured api.Options
	app.runtimeFactory = func(_ context.Context, opts api.Options) (RuntimeClient, error) {
		captured = opts
		return &fakeRuntime{
			runFn: func(_ context.Context, req api.Request) (*api.Response, error) {
				return &api.Response{Mode: api.ModeContext{EntryPoint: api.EntryPointCLI}, Result: &api.Result{Output: "ok"}}, nil
			},
		}, nil
	}

	if err := app.Run([]string{"--project", t.TempDir(), "--prompt", "hi", "--sandbox-backend=govm", "--sandbox-project-mount=off"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if captured.Sandbox.Govm == nil {
		t.Fatal("expected govm config")
	}
	if len(captured.Sandbox.Govm.Mounts) != 0 {
		t.Fatalf("expected no project mounts, got %+v", captured.Sandbox.Govm.Mounts)
	}
}

func TestRunRejectsUnavailableGovmRuntime(t *testing.T) {
	app := newTestApp()
	app.ValidateGovmPlatform = func() error { return nil }
	app.ValidateGovmRuntime = func(api.GovmOptions) error { return govmclient.ErrNativeUnavailable }

	err := app.Run([]string{"--prompt", "hi", "--sandbox-backend=govm"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "govm native runtime unavailable") {
		t.Fatalf("expected govm runtime error, got %v", err)
	}
}

func TestBuildSandboxOptionsResolvesAbsolutePathsForGovm(t *testing.T) {
	root := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	opts, err := buildSandboxOptions(".", "govm", "ro", "")
	if err != nil {
		t.Fatalf("build sandbox options: %v", err)
	}
	if opts.Govm == nil {
		t.Fatal("expected govm options")
	}
	if !filepath.IsAbs(opts.Govm.RuntimeHome) {
		t.Fatalf("expected absolute runtime home, got %q", opts.Govm.RuntimeHome)
	}
	if len(opts.Govm.Mounts) != 1 || !filepath.IsAbs(opts.Govm.Mounts[0].HostPath) {
		t.Fatalf("expected absolute host mount, got %+v", opts.Govm.Mounts)
	}
}
