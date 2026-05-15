package pathmap

import (
	"path/filepath"
	"strings"
	"testing"

	sandboxenv "github.com/saker-ai/saker/pkg/sandbox/env"
)

func TestNewNilMounts(t *testing.T) {
	m, err := New(nil)
	if err != nil {
		t.Fatalf("New(nil): %v", err)
	}
	if m == nil {
		t.Fatal("expected mapper")
	}
	if len(m.VisibleRoots()) != 0 {
		t.Errorf("expected zero visible roots")
	}
}

func TestNewEmptyMounts(t *testing.T) {
	m, err := New([]sandboxenv.MountSpec{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m == nil {
		t.Fatal("expected mapper")
	}
}

func TestNewRejectsEmptyHost(t *testing.T) {
	_, err := New([]sandboxenv.MountSpec{{HostPath: " ", GuestPath: "/workspace"}})
	if err == nil {
		t.Fatal("expected host path required")
	}
}

func TestNewRejectsRelativeGuest(t *testing.T) {
	_, err := New([]sandboxenv.MountSpec{{HostPath: "/host", GuestPath: "relative/path"}})
	if err == nil {
		t.Fatal("expected absolute guest path error")
	}
}

func TestNewRejectsEmptyGuest(t *testing.T) {
	_, err := New([]sandboxenv.MountSpec{{HostPath: "/host", GuestPath: " "}})
	if err == nil {
		t.Fatal("expected guest path required")
	}
}

func TestGuestToHostNilMapper(t *testing.T) {
	var m *Mapper
	_, _, err := m.GuestToHost("/x")
	if err == nil {
		t.Fatal("expected error for nil mapper")
	}
}

func TestGuestToHostInvalidPath(t *testing.T) {
	m, err := New([]sandboxenv.MountSpec{{HostPath: t.TempDir(), GuestPath: "/workspace"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = m.GuestToHost("relative")
	if err == nil {
		t.Fatal("expected error for relative guest path")
	}
	_, _, err = m.GuestToHost("")
	if err == nil {
		t.Fatal("expected error for empty guest path")
	}
}

func TestGuestToHostExactMatch(t *testing.T) {
	host := t.TempDir()
	m, _ := New([]sandboxenv.MountSpec{{HostPath: host, GuestPath: "/workspace"}})
	got, _, err := m.GuestToHost("/workspace")
	if err != nil {
		t.Fatalf("GuestToHost: %v", err)
	}
	if got != filepath.Clean(host) {
		t.Errorf("got %q, want %q", got, filepath.Clean(host))
	}
}

func TestGuestToHostPrefersDeepestMount(t *testing.T) {
	hostA := t.TempDir()
	hostB := t.TempDir()
	m, err := New([]sandboxenv.MountSpec{
		{HostPath: hostA, GuestPath: "/workspace"},
		{HostPath: hostB, GuestPath: "/data"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, mount, err := m.GuestToHost("/data/x.txt")
	if err != nil {
		t.Fatalf("GuestToHost: %v", err)
	}
	if got != filepath.Join(hostB, "x.txt") {
		t.Errorf("got %q, want %q", got, filepath.Join(hostB, "x.txt"))
	}
	if mount.HostPath != hostB {
		t.Errorf("wrong mount selected: %v", mount)
	}
}

func TestHostToGuestHappyPath(t *testing.T) {
	host := t.TempDir()
	m, _ := New([]sandboxenv.MountSpec{{HostPath: host, GuestPath: "/workspace"}})
	got, mount, err := m.HostToGuest(filepath.Join(host, "sub", "file.txt"))
	if err != nil {
		t.Fatalf("HostToGuest: %v", err)
	}
	if got != "/workspace/sub/file.txt" {
		t.Errorf("got %q", got)
	}
	if mount.GuestPath != "/workspace" {
		t.Errorf("mount: %+v", mount)
	}
}

func TestHostToGuestNilMapper(t *testing.T) {
	var m *Mapper
	_, _, err := m.HostToGuest("/x")
	if err == nil {
		t.Fatal("expected error for nil mapper")
	}
}

func TestHostToGuestExactMatch(t *testing.T) {
	host := t.TempDir()
	m, _ := New([]sandboxenv.MountSpec{{HostPath: host, GuestPath: "/workspace"}})
	got, _, err := m.HostToGuest(host)
	if err != nil {
		t.Fatalf("HostToGuest: %v", err)
	}
	if got != "/workspace" {
		t.Errorf("got %q, want /workspace", got)
	}
}

func TestHostToGuestUnmounted(t *testing.T) {
	m, _ := New([]sandboxenv.MountSpec{{HostPath: "/host/a", GuestPath: "/workspace"}})
	_, _, err := m.HostToGuest("/somewhere/else")
	if err == nil {
		t.Fatal("expected unmounted error")
	}
	if !strings.Contains(err.Error(), "not mounted") {
		t.Errorf("err: %v", err)
	}
}

func TestVisibleRootsNilMapper(t *testing.T) {
	var m *Mapper
	if got := m.VisibleRoots(); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestNormalizeGuestPathDotDot(t *testing.T) {
	// Embedded ".." in the middle is normalized away by Clean.
	got, err := normalizeGuestPath("/a/../b")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "/b" {
		t.Errorf("got %q, want /b", got)
	}
	// "/.." resolves to "/" — accepted (root mount).
	got, err = normalizeGuestPath("/..")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "/" {
		t.Errorf("got %q, want /", got)
	}
}

func TestNormalizeGuestPathTraversalRejected(t *testing.T) {
	// "/.." is the actual ".." after Clean.
	_, err := normalizeGuestPath("..")
	if err == nil {
		t.Fatal("expected error for relative dotdot")
	}
}

func TestWithinPathTrailingSeparator(t *testing.T) {
	if !withinPath("/a/b/c", "/a/b") {
		t.Error("nested path should be within parent")
	}
	if !withinPath("/a/b/", "/a/b") {
		t.Error("equal paths after Clean should be within")
	}
	if withinPath("/a/bc", "/a/b") {
		t.Error("/a/bc should not be within /a/b")
	}
	if !withinPath("/a/b", "/a/b") {
		t.Error("equal paths should be within")
	}
}
