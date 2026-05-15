package toolbuiltin

import (
	"context"
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/memory"
)

func TestMemorySaveTool_basic(t *testing.T) {
	store, _ := memory.NewStore(t.TempDir())
	mt := NewMemorySaveTool(store)

	if mt.Name() != "memory_save" {
		t.Errorf("Name() = %s", mt.Name())
	}
	if mt.Schema() == nil {
		t.Fatal("Schema() is nil")
	}

	result, err := mt.Execute(context.Background(), map[string]any{
		"name":        "test_entry",
		"description": "A test entry",
		"type":        "user",
		"content":     "Test content",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("expected success, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "test_entry") {
		t.Errorf("output should contain entry name: %s", result.Output)
	}

	// Verify it was saved
	loaded, err := store.Load("test_entry")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Content != "Test content" {
		t.Errorf("Content = %q", loaded.Content)
	}
}

func TestMemorySaveTool_invalidType(t *testing.T) {
	store, _ := memory.NewStore(t.TempDir())
	mt := NewMemorySaveTool(store)

	result, err := mt.Execute(context.Background(), map[string]any{
		"name":    "test",
		"type":    "invalid",
		"content": "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected failure for invalid type")
	}
}

func TestMemorySaveTool_nilStore(t *testing.T) {
	mt := NewMemorySaveTool(nil)
	result, err := mt.Execute(context.Background(), map[string]any{
		"name": "test", "type": "user", "content": "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected failure with nil store")
	}
}

func TestMemoryReadTool_index(t *testing.T) {
	store, _ := memory.NewStore(t.TempDir())
	store.Save(memory.Entry{Name: "entry1", Description: "desc1", Type: memory.MemoryTypeUser, Content: "c1"})

	mt := NewMemoryReadTool(store)
	if mt.Name() != "memory_read" {
		t.Errorf("Name() = %s", mt.Name())
	}
	if mt.Schema() == nil {
		t.Fatal("Schema() is nil")
	}

	result, err := mt.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("expected success: %s", result.Output)
	}
	if !strings.Contains(result.Output, "entry1") {
		t.Errorf("index should contain entry name: %s", result.Output)
	}
}

func TestMemoryReadTool_specific(t *testing.T) {
	store, _ := memory.NewStore(t.TempDir())
	store.Save(memory.Entry{Name: "my_entry", Description: "test", Type: memory.MemoryTypeFeedback, Content: "feedback content"})

	mt := NewMemoryReadTool(store)
	result, err := mt.Execute(context.Background(), map[string]any{"name": "my_entry"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("expected success: %s", result.Output)
	}
	if !strings.Contains(result.Output, "feedback content") {
		t.Errorf("output should contain content: %s", result.Output)
	}
}

func TestMemoryReadTool_emptyStore(t *testing.T) {
	store, _ := memory.NewStore(t.TempDir())
	mt := NewMemoryReadTool(store)

	result, err := mt.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "No memory") {
		t.Errorf("expected empty message: %s", result.Output)
	}
}

func TestMemoryReadTool_nilStore(t *testing.T) {
	mt := NewMemoryReadTool(nil)
	result, err := mt.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected failure with nil store")
	}
}

func TestMemoryReadTool_notFound(t *testing.T) {
	store, _ := memory.NewStore(t.TempDir())
	mt := NewMemoryReadTool(store)

	result, err := mt.Execute(context.Background(), map[string]any{"name": "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected failure for nonexistent entry")
	}
}
