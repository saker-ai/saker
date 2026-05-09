package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- SessionStore CRUD Tests ---

func TestSessionStore_CreateThread(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Test Thread")
	if thread.ID == "" {
		t.Error("thread.ID should not be empty")
	}
	if thread.Title != "Test Thread" {
		t.Errorf("thread.Title = %q, want %q", thread.Title, "Test Thread")
	}
	if thread.CreatedAt.IsZero() {
		t.Error("thread.CreatedAt should not be zero")
	}
	if thread.UpdatedAt.IsZero() {
		t.Error("thread.UpdatedAt should not be zero")
	}
	if thread.CreatedAt != thread.UpdatedAt {
		t.Error("CreatedAt and UpdatedAt should match on creation")
	}

	// Thread should appear in ListThreads.
	threads := s.ListThreads()
	if len(threads) != 1 {
		t.Fatalf("ListThreads returned %d threads, want 1", len(threads))
	}
	if threads[0].ID != thread.ID {
		t.Errorf("ListThreads[0].ID = %q, want %q", threads[0].ID, thread.ID)
	}
}

func TestSessionStore_CreateThread_EmptyTitle(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("")
	if thread.ID == "" {
		t.Error("thread.ID should not be empty even with empty title")
	}
	if thread.Title != "" {
		t.Errorf("thread.Title = %q, want empty", thread.Title)
	}
}

func TestSessionStore_ListThreads(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	// Empty store returns empty slice.
	threads := s.ListThreads()
	if len(threads) != 0 {
		t.Errorf("empty store: ListThreads returned %d, want 0", len(threads))
	}

	t1 := s.CreateThread("First")
	t2 := s.CreateThread("Second")
	t3 := s.CreateThread("Third")

	threads = s.ListThreads()
	if len(threads) != 3 {
		t.Fatalf("ListThreads returned %d, want 3", len(threads))
	}
	// Threads should be in creation order.
	if threads[0].ID != t1.ID || threads[1].ID != t2.ID || threads[2].ID != t3.ID {
		t.Error("threads not in creation order")
	}

	// ListThreads returns a copy — modifying it should not affect the store.
	threads[0].Title = "Mutated"
	fresh := s.ListThreads()
	if fresh[0].Title != "First" {
		t.Error("ListThreads did not return a copy; mutation leaked into store")
	}
}

func TestSessionStore_GetThread(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Findable")

	got, ok := s.GetThread(thread.ID)
	if !ok {
		t.Errorf("GetThread(%q) returned ok=false, want true", thread.ID)
	}
	if got.ID != thread.ID {
		t.Errorf("GetThread ID = %q, want %q", got.ID, thread.ID)
	}
	if got.Title != thread.Title {
		t.Errorf("GetThread Title = %q, want %q", got.Title, thread.Title)
	}

	// Non-existent ID.
	_, ok = s.GetThread("nonexistent-id")
	if ok {
		t.Error("GetThread with nonexistent ID should return ok=false")
	}
}

func TestSessionStore_UpdateThreadTitle(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Old Title")
	origUpdatedAt := thread.UpdatedAt

	ok := s.UpdateThreadTitle(thread.ID, "New Title")
	if !ok {
		t.Error("UpdateThreadTitle returned false for existing thread")
	}

	got, _ := s.GetThread(thread.ID)
	if got.Title != "New Title" {
		t.Errorf("title after update = %q, want %q", got.Title, "New Title")
	}
	if !got.UpdatedAt.After(origUpdatedAt) {
		t.Error("UpdatedAt should advance after title update")
	}

	// Non-existent thread.
	ok = s.UpdateThreadTitle("nonexistent-id", "Whatever")
	if ok {
		t.Error("UpdateThreadTitle should return false for nonexistent ID")
	}
}

func TestSessionStore_DeleteThread(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	t1 := s.CreateThread("Keep")
	t2 := s.CreateThread("Delete")

	// Add an item to t2 so we can verify items are cleaned up.
	s.AppendItem(t2.ID, "user", "hello", "turn1")

	ok := s.DeleteThread(t2.ID)
	if !ok {
		t.Error("DeleteThread returned false for existing thread")
	}

	threads := s.ListThreads()
	if len(threads) != 1 {
		t.Fatalf("ListThreads after delete: %d threads, want 1", len(threads))
	}
	if threads[0].ID != t1.ID {
		t.Errorf("remaining thread ID = %q, want %q", threads[0].ID, t1.ID)
	}

	// Items for the deleted thread should be gone.
	items := s.GetItems(t2.ID)
	if len(items) != 0 {
		t.Errorf("GetItems for deleted thread: %d items, want 0", len(items))
	}

	// GetThread should no longer find it.
	_, found := s.GetThread(t2.ID)
	if found {
		t.Error("GetThread should return false for deleted thread")
	}

	// Delete non-existent.
	ok = s.DeleteThread("nonexistent-id")
	if ok {
		t.Error("DeleteThread should return false for nonexistent ID")
	}
}

func TestSessionStore_DeleteThread_AllThreads(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	t1 := s.CreateThread("A")
	t2 := s.CreateThread("B")

	s.DeleteThread(t1.ID)
	s.DeleteThread(t2.ID)

	threads := s.ListThreads()
	if len(threads) != 0 {
		t.Errorf("after deleting all threads: %d remain, want 0", len(threads))
	}
}

// --- Item Operations Tests ---

func TestSessionStore_AppendItem(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Chat")
	origUpdatedAt := thread.UpdatedAt

	item := s.AppendItem(thread.ID, "user", "Hello world", "turn-1")
	if item.ID == "" {
		t.Error("item.ID should not be empty")
	}
	if item.ThreadID != thread.ID {
		t.Errorf("item.ThreadID = %q, want %q", item.ThreadID, thread.ID)
	}
	if item.TurnID != "turn-1" {
		t.Errorf("item.TurnID = %q, want %q", item.TurnID, "turn-1")
	}
	if item.Role != "user" {
		t.Errorf("item.Role = %q, want %q", item.Role, "user")
	}
	if item.Content != "Hello world" {
		t.Errorf("item.Content = %q, want %q", item.Content, "Hello world")
	}
	if item.CreatedAt.IsZero() {
		t.Error("item.CreatedAt should not be zero")
	}
	if len(item.Artifacts) != 0 {
		t.Errorf("item.Artifacts = %d, want 0", len(item.Artifacts))
	}

	// Thread UpdatedAt should advance.
	got, _ := s.GetThread(thread.ID)
	if !got.UpdatedAt.After(origUpdatedAt) {
		t.Error("thread UpdatedAt should advance after AppendItem")
	}

	// Item should appear in GetItems.
	items := s.GetItems(thread.ID)
	if len(items) != 1 {
		t.Fatalf("GetItems returned %d items, want 1", len(items))
	}
	if items[0].ID != item.ID {
		t.Errorf("GetItems[0].ID = %q, want %q", items[0].ID, item.ID)
	}
}

func TestSessionStore_AppendItem_Multiple(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Chat")

	i1 := s.AppendItem(thread.ID, "user", "Hi", "turn-1")
	i2 := s.AppendItem(thread.ID, "assistant", "Hello!", "turn-1")
	i3 := s.AppendItem(thread.ID, "user", "How are you?", "turn-2")

	items := s.GetItems(thread.ID)
	if len(items) != 3 {
		t.Fatalf("GetItems returned %d, want 3", len(items))
	}
	// Items should be in append order.
	if items[0].ID != i1.ID || items[1].ID != i2.ID || items[2].ID != i3.ID {
		t.Error("items not in append order")
	}
	// GetItems returns a copy.
	items[0].Content = "mutated"
	fresh := s.GetItems(thread.ID)
	if fresh[0].Content != "Hi" {
		t.Error("GetItems mutation leaked into store")
	}
}

func TestSessionStore_GetItem(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Chat")
	item := s.AppendItem(thread.ID, "user", "Hello", "turn-1")

	got, ok := s.GetItem(item.ID)
	if !ok {
		t.Errorf("GetItem(%q) returned ok=false", item.ID)
	}
	if got.ID != item.ID {
		t.Errorf("GetItem ID = %q, want %q", got.ID, item.ID)
	}
	if got.Content != "Hello" {
		t.Errorf("GetItem Content = %q, want %q", got.Content, "Hello")
	}

	// Non-existent item.
	_, ok = s.GetItem("nonexistent-item-id")
	if ok {
		t.Error("GetItem should return false for nonexistent ID")
	}
}

func TestSessionStore_GetItem_CrossThread(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	t1 := s.CreateThread("Thread1")
	t2 := s.CreateThread("Thread2")

	item1 := s.AppendItem(t1.ID, "user", "From T1", "turn-1")
	item2 := s.AppendItem(t2.ID, "assistant", "From T2", "turn-1")

	// GetItem should find items across threads.
	got, ok := s.GetItem(item1.ID)
	if !ok || got.ThreadID != t1.ID {
		t.Errorf("GetItem(item1) threadID=%q, want %q", got.ThreadID, t1.ID)
	}
	got, ok = s.GetItem(item2.ID)
	if !ok || got.ThreadID != t2.ID {
		t.Errorf("GetItem(item2) threadID=%q, want %q", got.ThreadID, t2.ID)
	}
}

func TestSessionStore_GetItems_EmptyThread(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Empty")
	items := s.GetItems(thread.ID)
	if items == nil {
		t.Error("GetItems on empty thread should return empty slice, not nil")
	}
	if len(items) != 0 {
		t.Errorf("GetItems on empty thread: %d items, want 0", len(items))
	}
}

func TestSessionStore_GetItems_NonexistentThread(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	items := s.GetItems("nonexistent-thread-id")
	if len(items) != 0 {
		t.Errorf("GetItems for nonexistent thread: %d items, want 0", len(items))
	}
}

// --- Artifact Tests ---

func TestSessionStore_AppendItemWithArtifacts(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Media Chat")
	artifacts := []Artifact{
		{Type: "image", URL: "/api/files/img1.png", Name: "tool-a"},
		{Type: "video", URL: "/api/files/vid1.mp4", Name: "tool-b"},
	}

	item := s.AppendItemWithArtifacts(thread.ID, "assistant", "Here is media", "turn-1", artifacts)
	if item.Role != "assistant" {
		t.Errorf("item.Role = %q, want %q", item.Role, "assistant")
	}
	if item.Content != "Here is media" {
		t.Errorf("item.Content = %q, want %q", item.Content, "Here is media")
	}
	if len(item.Artifacts) != 2 {
		t.Fatalf("item.Artifacts len = %d, want 2", len(item.Artifacts))
	}
	if item.Artifacts[0].Type != "image" || item.Artifacts[0].URL != "/api/files/img1.png" {
		t.Errorf("artifact[0] = %+v, mismatch", item.Artifacts[0])
	}
	if item.Artifacts[1].Type != "video" || item.Artifacts[1].URL != "/api/files/vid1.mp4" {
		t.Errorf("artifact[1] = %+v, mismatch", item.Artifacts[1])
	}
	// ToolName should be empty for AppendItemWithArtifacts.
	if item.ToolName != "" {
		t.Errorf("item.ToolName = %q, want empty", item.ToolName)
	}

	// Verify artifacts persist through GetItems.
	items := s.GetItems(thread.ID)
	if len(items[0].Artifacts) != 2 {
		t.Errorf("GetItems artifacts len = %d, want 2", len(items[0].Artifacts))
	}
}

func TestSessionStore_AppendToolItem(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Tool Chat")
	artifacts := []Artifact{
		{Type: "image", URL: "/api/files/tool-img.png", Name: "render"},
	}

	item := s.AppendToolItem(thread.ID, "render_tool", "Rendered output", "turn-1", artifacts)
	if item.Role != "tool" {
		t.Errorf("item.Role = %q, want %q", item.Role, "tool")
	}
	if item.ToolName != "render_tool" {
		t.Errorf("item.ToolName = %q, want %q", item.ToolName, "render_tool")
	}
	if item.Content != "Rendered output" {
		t.Errorf("item.Content = %q, want %q", item.Content, "Rendered output")
	}
	if len(item.Artifacts) != 1 {
		t.Fatalf("item.Artifacts len = %d, want 1", len(item.Artifacts))
	}
	if item.Artifacts[0].URL != "/api/files/tool-img.png" {
		t.Errorf("artifact URL = %q, want %q", item.Artifacts[0].URL, "/api/files/tool-img.png")
	}
}

func TestSessionStore_UpdateItemArtifact(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Artifact Chat")
	s.AppendItemWithArtifacts(thread.ID, "assistant", "content", "turn-1", []Artifact{
		{Type: "image", URL: "/api/files/original.png", Name: "tool-x"},
		{Type: "image", URL: "/api/files/other.png", Name: "tool-y"},
	})

	item, _ := s.GetItem(s.GetItems(thread.ID)[0].ID)

	// Update the URL of the first artifact.
	ok := s.UpdateItemArtifact(item.ID, "/api/files/original.png", "/api/files/replaced.png")
	if !ok {
		t.Error("UpdateItemArtifact should return true for matching URL")
	}

	// Verify the replacement took effect.
	got, _ := s.GetItem(item.ID)
	if got.Artifacts[0].URL != "/api/files/replaced.png" {
		t.Errorf("artifact[0].URL = %q, want %q", got.Artifacts[0].URL, "/api/files/replaced.png")
	}
	// Second artifact should be untouched.
	if got.Artifacts[1].URL != "/api/files/other.png" {
		t.Errorf("artifact[1].URL = %q, want %q", got.Artifacts[1].URL, "/api/files/other.png")
	}
}

func TestSessionStore_UpdateItemArtifact_WrongURL(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Artifact Chat")
	s.AppendItemWithArtifacts(thread.ID, "assistant", "content", "turn-1", []Artifact{
		{Type: "image", URL: "/api/files/a.png", Name: "tool"},
	})

	item, _ := s.GetItem(s.GetItems(thread.ID)[0].ID)

	ok := s.UpdateItemArtifact(item.ID, "/api/files/nonexistent.png", "/api/files/new.png")
	if ok {
		t.Error("UpdateItemArtifact should return false when oldURL does not match")
	}

	// Original artifact should be unchanged.
	got, _ := s.GetItem(item.ID)
	if got.Artifacts[0].URL != "/api/files/a.png" {
		t.Errorf("artifact URL should remain unchanged")
	}
}

func TestSessionStore_UpdateItemArtifact_NonexistentItem(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	ok := s.UpdateItemArtifact("nonexistent-item-id", "/old", "/new")
	if ok {
		t.Error("UpdateItemArtifact should return false for nonexistent item")
	}
}

// --- Persistence Tests ---

func TestSessionStore_Persistence_RoundTrip(t *testing.T) {
	dataDir := t.TempDir()

	s, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Persisted Thread")
	s.AppendItem(thread.ID, "user", "Hello", "turn-1")
	s.AppendItem(thread.ID, "assistant", "Hi there", "turn-1")
	s.AppendItemWithArtifacts(thread.ID, "tool", "image result", "turn-2", []Artifact{
		{Type: "image", URL: "/api/files/img.png", Name: "render"},
	})

	// Verify the JSON file was written.
	path := filepath.Join(dataDir, thread.ID+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("persisted file not found: %s", path)
	}

	// Load a fresh store from the same dataDir and verify data round-trips.
	s2, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore (second load): %v", err)
	}

	threads := s2.ListThreads()
	if len(threads) != 1 {
		t.Fatalf("loaded threads: %d, want 1", len(threads))
	}
	if threads[0].ID != thread.ID {
		t.Errorf("loaded thread ID = %q, want %q", threads[0].ID, thread.ID)
	}
	if threads[0].Title != "Persisted Thread" {
		t.Errorf("loaded thread Title = %q, want %q", threads[0].Title, "Persisted Thread")
	}

	items := s2.GetItems(thread.ID)
	if len(items) != 3 {
		t.Fatalf("loaded items: %d, want 3", len(items))
	}
	if items[0].Content != "Hello" {
		t.Errorf("loaded item[0].Content = %q, want %q", items[0].Content, "Hello")
	}
	if items[2].Artifacts[0].URL != "/api/files/img.png" {
		t.Errorf("loaded artifact URL = %q, want %q", items[2].Artifacts[0].URL, "/api/files/img.png")
	}
}

func TestSessionStore_Persistence_UpdateTitle(t *testing.T) {
	dataDir := t.TempDir()

	s, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Original")
	s.UpdateThreadTitle(thread.ID, "Updated")

	s2, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore (second load): %v", err)
	}
	got, _ := s2.GetThread(thread.ID)
	if got.Title != "Updated" {
		t.Errorf("persisted title = %q, want %q", got.Title, "Updated")
	}
}

func TestSessionStore_Persistence_DeleteThread(t *testing.T) {
	dataDir := t.TempDir()

	s, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("To Delete")
	s.AppendItem(thread.ID, "user", "bye", "turn-1")

	path := filepath.Join(dataDir, thread.ID+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("persisted file not found before delete")
	}

	s.DeleteThread(thread.ID)

	// The JSON file should be removed.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("persisted file still exists after DeleteThread: %s", path)
	}

	// Reload — thread should not appear.
	s2, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore (second load): %v", err)
	}
	if len(s2.ListThreads()) != 0 {
		t.Error("deleted thread should not appear after reload")
	}
}

func TestSessionStore_Persistence_EmptyDataDir(t *testing.T) {
	dataDir := t.TempDir()

	s, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore on empty dir: %v", err)
	}
	if len(s.ListThreads()) != 0 {
		t.Error("empty dataDir should yield no threads")
	}
}

func TestSessionStore_Persistence_CorruptFileSkipped(t *testing.T) {
	dataDir := t.TempDir()

	// Write a corrupt JSON file.
	corruptPath := filepath.Join(dataDir, "corrupt.json")
	if err := os.WriteFile(corruptPath, []byte("{bad json!!"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	// Write a valid thread file alongside it.
	s, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	s.CreateThread("Valid Thread")

	// Reload — only the valid thread should appear; corrupt file is silently skipped.
	s2, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore (second load): %v", err)
	}
	threads := s2.ListThreads()
	if len(threads) != 1 {
		t.Fatalf("expected 1 valid thread, got %d", len(threads))
	}
	if threads[0].Title != "Valid Thread" {
		t.Errorf("thread Title = %q, want %q", threads[0].Title, "Valid Thread")
	}
}

func TestSessionStore_Persistence_NonJSONFilesIgnored(t *testing.T) {
	dataDir := t.TempDir()

	// Write a non-.json file that should be ignored.
	if err := os.WriteFile(filepath.Join(dataDir, "notes.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatalf("write txt file: %v", err)
	}

	s, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	if len(s.ListThreads()) != 0 {
		t.Error("non-.json files should be ignored during load")
	}
}

func TestSessionStore_Persistence_NonexistentDir(t *testing.T) {
	// NewSessionStore should create the directory if it does not exist.
	dataDir := filepath.Join(t.TempDir(), "subdir", "sessions")
	s, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore with nested non-existent dir: %v", err)
	}
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		t.Errorf("dataDir was not created: %s", dataDir)
	}
	s.CreateThread("Works")
	if len(s.ListThreads()) != 1 {
		t.Error("store should work with auto-created dataDir")
	}
}

func TestSessionStore_NoDataDir(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore with empty dataDir: %v", err)
	}

	thread := s.CreateThread("InMemory")
	s.AppendItem(thread.ID, "user", "hello", "turn-1")

	// All operations should work without persistence.
	got, ok := s.GetThread(thread.ID)
	if !ok || got.Title != "InMemory" {
		t.Error("GetThread failed on in-memory store")
	}
	items := s.GetItems(thread.ID)
	if len(items) != 1 {
		t.Errorf("GetItems returned %d, want 1", len(items))
	}
}

// --- Timestamp Tests ---

func TestSessionStore_TimestampsAdvance(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("TS Test")
	before := thread.UpdatedAt

	time.Sleep(1 * time.Millisecond) // ensure time advances
	s.AppendItem(thread.ID, "user", "msg1", "turn-1")
	got, _ := s.GetThread(thread.ID)
	if !got.UpdatedAt.After(before) {
		t.Error("UpdatedAt should advance after AppendItem")
	}

	before = got.UpdatedAt
	time.Sleep(1 * time.Millisecond)
	s.UpdateThreadTitle(thread.ID, "New TS Title")
	got, _ = s.GetThread(thread.ID)
	if !got.UpdatedAt.After(before) {
		t.Error("UpdatedAt should advance after UpdateThreadTitle")
	}
}

// --- Persistence file content verification ---

func TestSessionStore_PersistedFileContent(t *testing.T) {
	dataDir := t.TempDir()

	s, err := NewSessionStore(dataDir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("File Check")
	s.AppendItem(thread.ID, "user", "check content", "turn-1")

	path := filepath.Join(dataDir, thread.ID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}

	var pt persistedThread
	if err := json.Unmarshal(raw, &pt); err != nil {
		t.Fatalf("unmarshal persisted file: %v", err)
	}
	if pt.Thread.ID != thread.ID {
		t.Errorf("persisted thread ID = %q, want %q", pt.Thread.ID, thread.ID)
	}
	if pt.Thread.Title != "File Check" {
		t.Errorf("persisted thread Title = %q, want %q", pt.Thread.Title, "File Check")
	}
	if len(pt.Items) != 1 {
		t.Fatalf("persisted items count = %d, want 1", len(pt.Items))
	}
	if pt.Items[0].Content != "check content" {
		t.Errorf("persisted item Content = %q, want %q", pt.Items[0].Content, "check content")
	}
}

// --- Concurrent access safety ---

func TestSessionStore_ConcurrentAppend(t *testing.T) {
	t.Parallel()
	s, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	thread := s.CreateThread("Concurrent")

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(idx int) {
			s.AppendItem(thread.ID, "user", "msg-"+string(rune('A'+idx)), "turn-1")
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	items := s.GetItems(thread.ID)
	if len(items) != 10 {
		t.Errorf("concurrent appends: got %d items, want 10", len(items))
	}
}