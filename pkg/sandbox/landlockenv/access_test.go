package landlockenv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
)

func makeSession(guestCwd string, roPaths, rwPaths []string) *sandboxenv.PreparedSession {
	return &sandboxenv.PreparedSession{
		SessionID:   "test",
		GuestCwd:    guestCwd,
		SandboxType: "landlock",
		Meta: map[string]any{
			"ro_paths": roPaths,
			"rw_paths": rwPaths,
		},
	}
}

func TestCheckReadAccess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		ro      []string
		rw      []string
		wantErr bool
	}{
		{"ro path allowed", "/project/src/main.go", []string{"/project"}, nil, false},
		{"rw path allowed for read", "/data/out.txt", nil, []string{"/data"}, false},
		{"outside both denied", "/etc/passwd", []string{"/project"}, []string{"/data"}, true},
		{"exact ro root", "/project", []string{"/project"}, nil, false},
		{"traversal denied", "/project/../etc/passwd", []string{"/project"}, nil, true},
		{"empty paths denied", "/anything", nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ps := makeSession("/project", tt.ro, tt.rw)
			err := checkReadAccess(ps, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkReadAccess(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, errAccessDenied) {
				t.Errorf("expected errAccessDenied, got %v", err)
			}
		})
	}
}

func TestCheckWriteAccess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		ro      []string
		rw      []string
		wantErr bool
	}{
		{"rw path allowed", "/data/out.txt", nil, []string{"/data"}, false},
		{"ro only denied", "/project/src/main.go", []string{"/project"}, nil, true},
		{"outside both denied", "/etc/shadow", []string{"/project"}, []string{"/data"}, true},
		{"exact rw root", "/data", nil, []string{"/data"}, false},
		{"traversal denied", "/data/../etc/shadow", nil, []string{"/data"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ps := makeSession("/project", tt.ro, tt.rw)
			err := checkWriteAccess(ps, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkWriteAccess(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, errAccessDenied) {
				t.Errorf("expected errAccessDenied, got %v", err)
			}
		})
	}
}

func TestAbsPath_Relative(t *testing.T) {
	t.Parallel()
	ps := makeSession("/home/user/project", nil, nil)
	got := absPath(ps, "src/main.go")
	want := "/home/user/project/src/main.go"
	if got != want {
		t.Errorf("absPath relative: got %q, want %q", got, want)
	}
}

func TestAbsPath_Absolute(t *testing.T) {
	t.Parallel()
	ps := makeSession("/home/user/project", nil, nil)
	got := absPath(ps, "/etc/passwd")
	want := "/etc/passwd"
	if got != want {
		t.Errorf("absPath absolute: got %q, want %q", got, want)
	}
}

func TestReadFile_Denied(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := t.TempDir()
	env := New(dir, &sandboxenv.LandlockOptions{Enabled: true})

	// Create a file outside the allowed paths.
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	ps := makeSession(dir, []string{dir}, []string{dir})
	_, err := env.ReadFile(context.Background(), ps, outsideFile)
	if err == nil {
		t.Fatal("expected ReadFile to be denied for path outside allowed roots")
	}
	if !errors.Is(err, errAccessDenied) {
		t.Errorf("expected errAccessDenied, got %v", err)
	}
}

func TestWriteFile_Denied(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	roDir := t.TempDir()
	env := New(dir, &sandboxenv.LandlockOptions{Enabled: true})

	ps := makeSession(dir, []string{dir, roDir}, []string{dir})
	// Writing to read-only dir should be denied.
	err := env.WriteFile(context.Background(), ps, filepath.Join(roDir, "test.txt"), []byte("data"))
	if err == nil {
		t.Fatal("expected WriteFile to be denied for read-only path")
	}
	if !errors.Is(err, errAccessDenied) {
		t.Errorf("expected errAccessDenied, got %v", err)
	}
}

func TestEditFile_Denied(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	roDir := t.TempDir()
	env := New(dir, &sandboxenv.LandlockOptions{Enabled: true})

	// Create a file in the read-only dir.
	roFile := filepath.Join(roDir, "readonly.txt")
	if err := os.WriteFile(roFile, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	ps := makeSession(dir, []string{dir, roDir}, []string{dir})
	err := env.EditFile(context.Background(), ps, sandboxenv.EditRequest{
		Path:    roFile,
		OldText: "original",
		NewText: "modified",
	})
	if err == nil {
		t.Fatal("expected EditFile to be denied for read-only path")
	}
	if !errors.Is(err, errAccessDenied) {
		t.Errorf("expected errAccessDenied, got %v", err)
	}
}

func TestGlob_DeniedRoot(t *testing.T) {
	t.Parallel()
	outsideDir := t.TempDir()
	env := New(outsideDir, &sandboxenv.LandlockOptions{Enabled: true})

	// Session only allows /allowed, but env.projectRoot is outsideDir.
	ps := makeSession("/allowed", []string{"/allowed"}, nil)
	_, err := env.Glob(context.Background(), ps, "*.go")
	if err == nil {
		t.Fatal("expected Glob to be denied when projectRoot is outside allowed paths")
	}
	if !errors.Is(err, errAccessDenied) {
		t.Errorf("expected errAccessDenied, got %v", err)
	}
}

func TestGrep_DeniedRoot(t *testing.T) {
	t.Parallel()
	outsideDir := t.TempDir()
	env := New(outsideDir, &sandboxenv.LandlockOptions{Enabled: true})

	ps := makeSession("/allowed", []string{"/allowed"}, nil)
	_, err := env.Grep(context.Background(), ps, sandboxenv.GrepRequest{
		Pattern: "foo",
		Path:    outsideDir,
	})
	if err == nil {
		t.Fatal("expected Grep to be denied for path outside allowed roots")
	}
	if !errors.Is(err, errAccessDenied) {
		t.Errorf("expected errAccessDenied, got %v", err)
	}
}

func TestSymlinkBypass_Denied(t *testing.T) {
	t.Parallel()
	allowedDir := t.TempDir()
	secretDir := t.TempDir()

	// Create a secret file outside the allowed directory.
	secretFile := filepath.Join(secretDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink inside the allowed directory pointing to the secret.
	symlink := filepath.Join(allowedDir, "escape")
	if err := os.Symlink(secretFile, symlink); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	ps := makeSession(allowedDir, []string{allowedDir}, []string{allowedDir})

	// Reading via symlink should be denied because it resolves outside allowed paths.
	if err := checkReadAccess(ps, symlink); err == nil {
		t.Error("expected symlink read to be denied")
	}
	// Writing via symlink should also be denied.
	if err := checkWriteAccess(ps, symlink); err == nil {
		t.Error("expected symlink write to be denied")
	}
}

func TestNilSession_Denied(t *testing.T) {
	t.Parallel()
	if err := checkReadAccess(nil, "/any/path"); err == nil {
		t.Error("expected error for nil session")
	}
	if err := checkWriteAccess(nil, "/any/path"); err == nil {
		t.Error("expected error for nil session")
	}
}
