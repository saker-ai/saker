package landlockenv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
)

func TestPrepareSession(t *testing.T) {
	dir := t.TempDir()
	env := New(dir, &sandboxenv.LandlockOptions{
		Enabled:                    true,
		AutoCreateSessionWorkspace: true,
		SessionWorkspaceBase:       filepath.Join(dir, "ws"),
	})
	ps, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{
		SessionID:   "test-session",
		ProjectRoot: dir,
	})
	if err != nil {
		t.Fatalf("PrepareSession: %v", err)
	}
	if ps.SandboxType != "landlock" {
		t.Errorf("SandboxType: got %q, want %q", ps.SandboxType, "landlock")
	}
	if ps.SessionID != "test-session" {
		t.Errorf("SessionID: got %q, want %q", ps.SessionID, "test-session")
	}
	// Verify workspace directory was created.
	wsDir := filepath.Join(dir, "ws", "test-session")
	if _, err := os.Stat(wsDir); os.IsNotExist(err) {
		t.Errorf("workspace directory not created: %s", wsDir)
	}
	// Verify paths in meta.
	roPaths, rwPaths := pathsFromSession(ps)
	if len(roPaths) == 0 || roPaths[0] != dir {
		t.Errorf("ro_paths should include project root: %v", roPaths)
	}
	found := false
	for _, p := range rwPaths {
		if p == wsDir {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("rw_paths should include workspace: %v", rwPaths)
	}
}

func TestRunCommand_Echo(t *testing.T) {
	dir := t.TempDir()
	env := New(dir, &sandboxenv.LandlockOptions{
		Enabled:         true,
		DefaultGuestCwd: dir,
	})
	ps := &sandboxenv.PreparedSession{
		SessionID:   "test",
		GuestCwd:    dir,
		SandboxType: "landlock",
		Meta: map[string]any{
			"ro_paths": []string{dir},
			"rw_paths": []string{"/tmp"},
		},
	}
	result, err := env.RunCommand(context.Background(), ps, sandboxenv.CommandRequest{
		Command: "echo landlock-test",
		Timeout: 5000000000, // 5s
	})
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if got := strings.TrimSpace(result.Stdout); got != "landlock-test" {
		t.Errorf("stdout: got %q, want %q", got, "landlock-test")
	}
}

func TestReadWriteFile(t *testing.T) {
	dir := t.TempDir()
	env := New(dir, &sandboxenv.LandlockOptions{Enabled: true})
	ps := &sandboxenv.PreparedSession{
		SessionID:   "test",
		GuestCwd:    dir,
		SandboxType: "landlock",
		Meta: map[string]any{
			"ro_paths": []string{dir},
			"rw_paths": []string{dir},
		},
	}

	path := filepath.Join(dir, "test.txt")
	content := []byte("hello landlock")

	if err := env.WriteFile(context.Background(), ps, path, content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := env.ReadFile(context.Background(), ps, path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content: got %q, want %q", string(data), string(content))
	}
}

func TestEditFile(t *testing.T) {
	dir := t.TempDir()
	env := New(dir, &sandboxenv.LandlockOptions{Enabled: true})
	ps := &sandboxenv.PreparedSession{
		SessionID:   "test",
		GuestCwd:    dir,
		SandboxType: "landlock",
		Meta: map[string]any{
			"ro_paths": []string{dir},
			"rw_paths": []string{dir},
		},
	}

	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("foo bar baz"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := env.EditFile(context.Background(), ps, sandboxenv.EditRequest{
		Path:    path,
		OldText: "bar",
		NewText: "qux",
	}); err != nil {
		t.Fatalf("EditFile: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "foo qux baz" {
		t.Errorf("content: got %q, want %q", string(data), "foo qux baz")
	}
}

func TestRunCommandStream(t *testing.T) {
	dir := t.TempDir()
	env := New(dir, &sandboxenv.LandlockOptions{Enabled: true, DefaultGuestCwd: dir})
	ps := &sandboxenv.PreparedSession{
		SessionID:   "test",
		GuestCwd:    dir,
		SandboxType: "landlock",
		Meta: map[string]any{
			"ro_paths": []string{dir},
			"rw_paths": []string{"/tmp"},
		},
	}

	var gotStdout string
	result, err := env.RunCommandStream(context.Background(), ps, sandboxenv.CommandRequest{
		Command: "echo streaming",
		Timeout: 5000000000,
	}, sandboxenv.CommandStreamCallbacks{
		OnStdout: func(s string) { gotStdout = s },
	})
	if err != nil {
		t.Fatalf("RunCommandStream: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "streaming" {
		t.Errorf("stdout: got %q, want %q", result.Stdout, "streaming\n")
	}
	if strings.TrimSpace(gotStdout) != "streaming" {
		t.Errorf("callback stdout: got %q, want %q", gotStdout, "streaming\n")
	}
}

func TestCloseSession(t *testing.T) {
	env := New("/tmp", &sandboxenv.LandlockOptions{Enabled: true})
	if err := env.CloseSession(context.Background(), nil); err != nil {
		t.Errorf("CloseSession: %v", err)
	}
}
