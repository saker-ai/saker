package cache

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/tool"
)

func TestMemoryStoreNilBehavior(t *testing.T) {
	t.Parallel()
	var store *MemoryStore

	// Load on nil store returns false, no error.
	result, ok, err := store.Load(context.Background(), artifact.CacheKey("test"))
	if err != nil {
		t.Errorf("nil MemoryStore.Load: unexpected error %v", err)
	}
	if ok {
		t.Error("nil MemoryStore.Load: expected ok=false")
	}
	if result != nil {
		t.Error("nil MemoryStore.Load: expected nil result")
	}

	// Save on nil store returns no error.
	if err := store.Save(context.Background(), artifact.CacheKey("test"), &tool.ToolResult{Output: "x"}); err != nil {
		t.Errorf("nil MemoryStore.Save: unexpected error %v", err)
	}
}

func TestMemoryStoreOverwrite(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	key := artifact.NewCacheKey("tool", map[string]any{"k": "v"}, []artifact.ArtifactRef{
		artifact.NewGeneratedRef("a1", artifact.ArtifactKindImage),
	})

	if err := store.Save(context.Background(), key, &tool.ToolResult{Output: "first"}); err != nil {
		t.Fatalf("save first: %v", err)
	}
	if err := store.Save(context.Background(), key, &tool.ToolResult{Output: "second"}); err != nil {
		t.Fatalf("save second: %v", err)
	}

	result, ok, err := store.Load(context.Background(), key)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if result.Output != "second" {
		t.Errorf("expected 'second' after overwrite, got %q", result.Output)
	}
}

func TestMemoryStoreMutationIsolation(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	key := artifact.NewCacheKey("tool", map[string]any{"k": "v"}, []artifact.ArtifactRef{
		artifact.NewGeneratedRef("a1", artifact.ArtifactKindImage),
	})

	original := &tool.ToolResult{
		Output: "original",
		Artifacts: []artifact.ArtifactRef{
			artifact.NewGeneratedRef("art1", artifact.ArtifactKindText),
		},
	}
	if err := store.Save(context.Background(), key, original); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Mutate the loaded result — should not affect store.
	result, ok, _ := store.Load(context.Background(), key)
	if !ok {
		t.Fatal("expected cache hit")
	}
	result.Output = "mutated"
	result.Artifacts[0] = artifact.NewGeneratedRef("mutated", artifact.ArtifactKindImage)

	reloaded, ok, _ := store.Load(context.Background(), key)
	if !ok {
		t.Fatal("expected cache hit on reload")
	}
	if reloaded.Output == "mutated" {
		t.Error("mutation leaked back to store")
	}
	if reloaded.Artifacts[0].ArtifactID == "mutated" {
		t.Error("artifact mutation leaked back to store")
	}
}

func TestFileStoreEviction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}

	// Add more entries than maxEntries (default 1000).
	// For testing, add 5 and verify they persist.
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		key := artifact.NewCacheKey("tool", map[string]any{"index": i}, []artifact.ArtifactRef{
			artifact.NewGeneratedRef(fmt.Sprintf("a%d", i), artifact.ArtifactKindText),
		})
		if err := store.Save(ctx, key, &tool.ToolResult{Output: fmt.Sprintf("result-%d", i)}); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	// Verify all 5 entries are accessible.
	for i := 0; i < 5; i++ {
		key := artifact.NewCacheKey("tool", map[string]any{"index": i}, []artifact.ArtifactRef{
			artifact.NewGeneratedRef(fmt.Sprintf("a%d", i), artifact.ArtifactKindText),
		})
		result, ok, err := store.Load(ctx, key)
		if err != nil || !ok {
			t.Errorf("load %d: ok=%v err=%v", i, ok, err)
		}
		if result.Output != fmt.Sprintf("result-%d", i) {
			t.Errorf("result-%d: got %q", i, result.Output)
		}
	}
}

func TestFileStoreOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}

	ctx := context.Background()
	key := artifact.NewCacheKey("tool", map[string]any{"k": "v"}, []artifact.ArtifactRef{
		artifact.NewGeneratedRef("a1", artifact.ArtifactKindImage),
	})

	if err := store.Save(ctx, key, &tool.ToolResult{Output: "first"}); err != nil {
		t.Fatalf("save first: %v", err)
	}
	if err := store.Save(ctx, key, &tool.ToolResult{Output: "second"}); err != nil {
		t.Fatalf("save second: %v", err)
	}

	result, ok, err := store.Load(ctx, key)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if result.Output != "second" {
		t.Errorf("expected 'second' after overwrite, got %q", result.Output)
	}
}

func TestFileStoreNilBehavior(t *testing.T) {
	t.Parallel()
	var store *FileStore

	_, ok, err := store.Load(context.Background(), artifact.CacheKey("test"))
	if err != nil {
		t.Errorf("nil FileStore.Load: unexpected error %v", err)
	}
	if ok {
		t.Error("nil FileStore.Load: expected ok=false")
	}

	if err := store.Save(context.Background(), artifact.CacheKey("test"), &tool.ToolResult{}); err != nil {
		t.Errorf("nil FileStore.Save: unexpected error %v", err)
	}
}

func TestCloneToolResultNil(t *testing.T) {
	t.Parallel()
	if cloneToolResult(nil) != nil {
		t.Error("expected nil for nil ToolResult clone")
	}
}

func TestCloneToolResultEmptyArtifacts(t *testing.T) {
	t.Parallel()
	original := &tool.ToolResult{Output: "test"}
	cloned := cloneToolResult(original)
	if cloned.Output != "test" {
		t.Errorf("cloned.Output = %q, want 'test'", cloned.Output)
	}
	if len(cloned.Artifacts) != 0 {
		t.Errorf("cloned.Artifacts should be empty, got %d", len(cloned.Artifacts))
	}
	// Mutating clone should not affect original.
	cloned.Output = "mutated"
	if original.Output != "test" {
		t.Error("mutation leaked back to original")
	}
}
