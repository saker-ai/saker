package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewStore(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	store, err := NewStore(memDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if store.Dir() != memDir {
		t.Errorf("Dir() = %s, want %s", store.Dir(), memDir)
	}
	// Directory should be created.
	if _, err := os.Stat(memDir); os.IsNotExist(err) {
		t.Error("memory directory should be created")
	}
}

func TestSaveAndLoad(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	entry := Entry{
		Name:        "user_role",
		Description: "User is a Go developer",
		Type:        MemoryTypeUser,
		Content:     "The user is an experienced Go developer.",
	}
	if err := store.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("user_role")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Name != "user_role" {
		t.Errorf("Name = %q, want %q", loaded.Name, "user_role")
	}
	if loaded.Description != "User is a Go developer" {
		t.Errorf("Description = %q, want %q", loaded.Description, "User is a Go developer")
	}
	if loaded.Type != MemoryTypeUser {
		t.Errorf("Type = %q, want %q", loaded.Type, MemoryTypeUser)
	}
	if loaded.Content != "The user is an experienced Go developer." {
		t.Errorf("Content = %q", loaded.Content)
	}
}

func TestSave_invalidType(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	err := store.Save(Entry{Name: "test", Type: "invalid"})
	if err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestSave_emptyName(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	err := store.Save(Entry{Name: "", Type: MemoryTypeUser})
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestDelete(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	store.Save(Entry{Name: "temp", Type: MemoryTypeProject, Content: "temporary"})

	if err := store.Delete("temp"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.Load("temp")
	if err == nil {
		t.Error("expected error loading deleted entry")
	}
}

func TestDelete_idempotent(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	if err := store.Delete("nonexistent"); err != nil {
		t.Errorf("deleting nonexistent entry should not error: %v", err)
	}
}

func TestList(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	store.Save(Entry{Name: "beta", Type: MemoryTypeProject, Content: "b"})
	store.Save(Entry{Name: "alpha", Type: MemoryTypeFeedback, Content: "a"})
	store.Save(Entry{Name: "gamma", Type: MemoryTypeUser, Content: "c"})

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("List count = %d, want 3", len(entries))
	}
	// Should be sorted by type then name.
	if entries[0].Type != MemoryTypeFeedback {
		t.Errorf("first entry type = %q, want feedback", entries[0].Type)
	}
}

func TestLoadIndex(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	store.Save(Entry{Name: "test_entry", Description: "A test", Type: MemoryTypeUser, Content: "content"})

	index, err := store.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if !strings.Contains(index, "test_entry") {
		t.Errorf("index should contain entry name, got: %s", index)
	}
	if !strings.Contains(index, "A test") {
		t.Errorf("index should contain description, got: %s", index)
	}
}

func TestLoadIndex_empty(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	index, err := store.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if index != "" {
		t.Errorf("empty store index should be empty, got: %q", index)
	}
}

func TestUpdateIndex(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	store.Save(Entry{Name: "a", Description: "first", Type: MemoryTypeUser, Content: "c1"})
	store.Save(Entry{Name: "b", Description: "second", Type: MemoryTypeFeedback, Content: "c2"})

	if err := store.UpdateIndex(); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}
	index, _ := store.LoadIndex()
	lines := strings.Split(strings.TrimSpace(index), "\n")
	if len(lines) != 2 {
		t.Errorf("index lines = %d, want 2", len(lines))
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user role", "user_role"},
		{"User/Admin", "user_admin"},
		{"test:file", "test_file"},
		{"", "unnamed"},
	}
	for _, tt := range tests {
		got := sanitizeFilename(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseEntryFile_noFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.md")
	os.WriteFile(path, []byte("Just some content"), 0644)

	entry, err := parseEntryFile(path)
	if err != nil {
		t.Fatalf("parseEntryFile: %v", err)
	}
	if entry.Content != "Just some content" {
		t.Errorf("Content = %q", entry.Content)
	}
}

func TestTruncateIndex(t *testing.T) {
	// Test line truncation.
	lines := make([]string, 250)
	for i := range lines {
		lines[i] = "- entry"
	}
	content := strings.Join(lines, "\n")
	truncated := truncateIndex(content)
	resultLines := strings.Split(truncated, "\n")
	if len(resultLines) > maxEntrypointLines {
		t.Errorf("truncated lines = %d, want <= %d", len(resultLines), maxEntrypointLines)
	}
}

func TestIsValidType(t *testing.T) {
	if !IsValidType(MemoryTypeUser) {
		t.Error("user should be valid")
	}
	if IsValidType("invalid") {
		t.Error("invalid should not be valid")
	}
}

func TestSave_overwrite(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	store.Save(Entry{Name: "test", Type: MemoryTypeUser, Content: "v1"})
	store.Save(Entry{Name: "test", Type: MemoryTypeUser, Content: "v2"})

	loaded, err := store.Load("test")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Content != "v2" {
		t.Errorf("Content = %q, want v2", loaded.Content)
	}
}
