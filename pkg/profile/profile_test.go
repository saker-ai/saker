package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"", false},
		{"default", false},
		{"alice", false},
		{"bob-dev", false},
		{"test_123", false},
		{"a", false},
		{"-invalid", true},
		{"_invalid", true},
		{"UPPERCASE", true},
		{"has space", true},
		{"has.dot", true},
		{"a/b", true},
	}
	for _, tt := range tests {
		err := Validate(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("Validate(%q) err=%v, wantErr=%v", tt.name, err, tt.wantErr)
		}
	}
}

func TestDir(t *testing.T) {
	t.Parallel()
	root := "/project"

	if got := Dir(root, ""); got != filepath.Join(root, ".saker") {
		t.Errorf("Dir(root, \"\") = %q", got)
	}
	if got := Dir(root, "default"); got != filepath.Join(root, ".saker") {
		t.Errorf("Dir(root, \"default\") = %q", got)
	}
	if got := Dir(root, "alice"); got != filepath.Join(root, ".saker", "profiles", "alice") {
		t.Errorf("Dir(root, \"alice\") = %q", got)
	}
}

func TestCreateAndExists(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".saker"), 0o755)

	if Exists(root, "coder") {
		t.Fatal("coder should not exist yet")
	}

	if err := Create(root, "coder", CreateOptions{}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !Exists(root, "coder") {
		t.Fatal("coder should exist after Create")
	}

	// Verify subdirectories.
	for _, sub := range profileDirs {
		path := filepath.Join(Dir(root, "coder"), sub)
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Errorf("expected subdirectory %s to exist", sub)
		}
	}

	// Duplicate create should fail.
	if err := Create(root, "coder", CreateOptions{}); err == nil {
		t.Fatal("duplicate Create should fail")
	}
}

func TestCreateWithClone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".saker"), 0o755)

	// Create source profile with settings.
	Create(root, "source", CreateOptions{})
	os.WriteFile(filepath.Join(Dir(root, "source"), "settings.json"), []byte(`{"model":"claude-3"}`), 0o644)

	// Clone from source.
	if err := Create(root, "cloned", CreateOptions{CloneFrom: "source"}); err != nil {
		t.Fatalf("Create with clone: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(Dir(root, "cloned"), "settings.json"))
	if err != nil {
		t.Fatalf("read cloned settings: %v", err)
	}
	if string(data) != `{"model":"claude-3"}` {
		t.Errorf("cloned settings = %q", data)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".saker"), 0o755)

	Create(root, "temp", CreateOptions{})
	if err := Delete(root, "temp"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if Exists(root, "temp") {
		t.Fatal("temp should not exist after Delete")
	}

	// Delete default should fail.
	if err := Delete(root, "default"); err == nil {
		t.Fatal("Delete(default) should fail")
	}

	// Delete non-existent should fail.
	if err := Delete(root, "nope"); err == nil {
		t.Fatal("Delete(nope) should fail")
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	root := "/project"

	if got := Resolve(root, ""); got != "" {
		t.Errorf("Resolve(\"\") = %q, want empty", got)
	}
	if got := Resolve(root, "default"); got != "" {
		t.Errorf("Resolve(\"default\") = %q, want empty", got)
	}
	if got := Resolve(root, "alice"); got == "" {
		t.Error("Resolve(\"alice\") should not be empty")
	}
}

func TestEnsureExists(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".saker"), 0o755)

	// First call creates.
	if err := EnsureExists(root, "auto"); err != nil {
		t.Fatalf("EnsureExists: %v", err)
	}
	if !Exists(root, "auto") {
		t.Fatal("auto should exist")
	}

	// Second call is no-op.
	if err := EnsureExists(root, "auto"); err != nil {
		t.Fatalf("EnsureExists idempotent: %v", err)
	}

	// Default is always no-op.
	if err := EnsureExists(root, ""); err != nil {
		t.Fatalf("EnsureExists default: %v", err)
	}
}

func TestStickyActiveProfile(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".saker"), 0o755)

	// No active profile initially.
	if got := GetActive(root); got != "" {
		t.Errorf("GetActive() = %q, want empty", got)
	}

	// Create and set active.
	Create(root, "dev", CreateOptions{})
	if err := SetActive(root, "dev"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if got := GetActive(root); got != "dev" {
		t.Errorf("GetActive() = %q, want dev", got)
	}

	// Clear active.
	if err := SetActive(root, ""); err != nil {
		t.Fatalf("SetActive empty: %v", err)
	}
	if got := GetActive(root); got != "" {
		t.Errorf("GetActive() = %q, want empty", got)
	}

	// Set active to non-existent should fail.
	if err := SetActive(root, "nope"); err == nil {
		t.Fatal("SetActive(nope) should fail")
	}
}

func TestList(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".saker"), 0o755)

	Create(root, "alice", CreateOptions{})
	Create(root, "bob", CreateOptions{})

	profiles, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(profiles) != 3 { // default + alice + bob
		t.Fatalf("expected 3 profiles, got %d", len(profiles))
	}
	if profiles[0].Name != "default" || !profiles[0].IsDefault {
		t.Errorf("first profile should be default, got %+v", profiles[0])
	}
}

func TestDeleteClearsStickyActive(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".saker"), 0o755)

	Create(root, "ephemeral", CreateOptions{})
	SetActive(root, "ephemeral")

	if err := Delete(root, "ephemeral"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := GetActive(root); got != "" {
		t.Errorf("active should be cleared after delete, got %q", got)
	}
}
