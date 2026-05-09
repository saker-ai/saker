package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/tool"
)

func TestCacheMemoryStoreHitAndMiss(t *testing.T) {
	store := NewMemoryStore()
	key := artifact.NewCacheKey("caption", map[string]any{"prompt": "describe"}, []artifact.ArtifactRef{
		artifact.NewGeneratedRef("art_1", artifact.ArtifactKindImage),
	})
	want := &tool.ToolResult{Output: "cached"}

	if _, ok, err := store.Load(context.Background(), key); err != nil || ok {
		t.Fatalf("expected initial cache miss, got ok=%v err=%v", ok, err)
	}
	if err := store.Save(context.Background(), key, want); err != nil {
		t.Fatalf("save cache entry: %v", err)
	}
	got, ok, err := store.Load(context.Background(), key)
	if err != nil || !ok {
		t.Fatalf("expected cache hit, got ok=%v err=%v", ok, err)
	}
	if got == nil || got.Output != "cached" {
		t.Fatalf("expected cached result to round-trip, got %+v", got)
	}
}

func TestCacheFileStoreRoundTrip(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "cache.json"))
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	key := artifact.NewCacheKey("caption", map[string]any{"prompt": "persist"}, []artifact.ArtifactRef{
		artifact.NewGeneratedRef("art_file", artifact.ArtifactKindImage),
	})
	if err := store.Save(context.Background(), key, &tool.ToolResult{Output: "persisted"}); err != nil {
		t.Fatalf("save cache entry: %v", err)
	}

	reloaded, err := NewFileStore(store.path)
	if err != nil {
		t.Fatalf("reload file store: %v", err)
	}
	got, ok, err := reloaded.Load(context.Background(), key)
	if err != nil || !ok {
		t.Fatalf("expected file-backed cache hit, got ok=%v err=%v", ok, err)
	}
	if got == nil || got.Output != "persisted" {
		t.Fatalf("unexpected file-backed cache entry: %+v", got)
	}
}

func TestCacheFileStoreTmpCleanedAfterWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}

	key := artifact.NewCacheKey("tool", map[string]any{"k": "v"}, []artifact.ArtifactRef{
		artifact.NewGeneratedRef("a1", artifact.ArtifactKindImage),
	})
	if err := store.Save(context.Background(), key, &tool.ToolResult{Output: "cached"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatal("expected .tmp to be cleaned up after successful cache write")
	}
}

func TestCacheFileStoreAtomicWriteNoCorruption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	key := artifact.NewCacheKey("tool", map[string]any{"k": "v"}, []artifact.ArtifactRef{
		artifact.NewGeneratedRef("a1", artifact.ArtifactKindImage),
	})
	if err := store.Save(context.Background(), key, &tool.ToolResult{Output: "original"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Leave a corrupted .tmp file
	os.WriteFile(path+".tmp", []byte("garbage"), 0o600)

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok, err := reloaded.Load(context.Background(), key)
	if err != nil || !ok {
		t.Fatalf("expected cache hit after reload, got ok=%v err=%v", ok, err)
	}
	if got.Output != "original" {
		t.Fatalf("data corrupted: got %q", got.Output)
	}
}
