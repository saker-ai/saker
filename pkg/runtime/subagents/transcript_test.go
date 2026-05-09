package subagents

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryTranscriptStore(t *testing.T) {
	store := NewMemoryTranscriptStore()
	now := time.Now()

	t1 := Transcript{
		AgentID:   "agent-1",
		SessionID: "session-a",
		Profile:   "general-purpose",
		StartedAt: now,
		Status:    StatusCompleted,
		Messages: []TranscriptMessage{
			{Role: "user", Content: "hello", Time: now},
			{Role: "assistant", Content: "hi", Time: now.Add(time.Second)},
		},
	}
	t2 := Transcript{
		AgentID:   "agent-2",
		SessionID: "session-a",
		Profile:   "explore",
		StartedAt: now.Add(time.Minute),
		Status:    StatusRunning,
	}
	t3 := Transcript{
		AgentID:   "agent-3",
		SessionID: "session-b",
		Profile:   "plan",
		StartedAt: now.Add(2 * time.Minute),
		Status:    StatusCompleted,
	}

	for _, tr := range []Transcript{t1, t2, t3} {
		if err := store.Save(tr); err != nil {
			t.Fatalf("Save(%s): %v", tr.AgentID, err)
		}
	}

	// Load
	loaded, err := store.Load("agent-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AgentID != "agent-1" {
		t.Errorf("expected agent-1, got %s", loaded.AgentID)
	}
	if len(loaded.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(loaded.Messages))
	}

	// Load not found
	if _, err := store.Load("nonexistent"); err == nil {
		t.Error("expected error for nonexistent transcript")
	}

	// List by session
	list, err := store.List("session-a")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 transcripts for session-a, got %d", len(list))
	}
	// Should be sorted by start time
	if list[0].AgentID != "agent-1" || list[1].AgentID != "agent-2" {
		t.Errorf("unexpected order: %s, %s", list[0].AgentID, list[1].AgentID)
	}

	// List empty session
	list, err = store.List("nonexistent")
	if err != nil {
		t.Fatalf("List empty: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 transcripts, got %d", len(list))
	}

	// Overwrite
	t1.Status = StatusFailed
	if err := store.Save(t1); err != nil {
		t.Fatalf("Save overwrite: %v", err)
	}
	loaded2, _ := store.Load("agent-1")
	if loaded2.Status != StatusFailed {
		t.Errorf("expected failed status after overwrite, got %s", loaded2.Status)
	}
}

func TestFileTranscriptStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "transcripts")

	store, err := NewFileTranscriptStore(dir)
	if err != nil {
		t.Fatalf("NewFileTranscriptStore: %v", err)
	}

	now := time.Now().Truncate(time.Millisecond) // JSON loses sub-ms precision

	tr := Transcript{
		AgentID:   "agent-file-1",
		SessionID: "session-x",
		Profile:   "fork",
		StartedAt: now,
		Status:    StatusCompleted,
		Messages: []TranscriptMessage{
			{Role: "user", Content: "do something", Time: now},
		},
		Usage: map[string]any{"input_tokens": 100},
	}

	if err := store.Save(tr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists
	path := filepath.Join(dir, "agent-file-1.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist: %v", err)
	}

	// Load
	loaded, err := store.Load("agent-file-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AgentID != "agent-file-1" {
		t.Errorf("expected agent-file-1, got %s", loaded.AgentID)
	}
	if loaded.SessionID != "session-x" {
		t.Errorf("expected session-x, got %s", loaded.SessionID)
	}
	if len(loaded.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(loaded.Messages))
	}
	if loaded.Status != StatusCompleted {
		t.Errorf("expected completed, got %s", loaded.Status)
	}

	// Load not found
	if _, err := store.Load("nonexistent"); err == nil {
		t.Error("expected error for nonexistent")
	}

	// List
	list, err := store.List("session-x")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1, got %d", len(list))
	}

	// List different session
	list2, _ := store.List("other")
	if len(list2) != 0 {
		t.Errorf("expected 0, got %d", len(list2))
	}
}
