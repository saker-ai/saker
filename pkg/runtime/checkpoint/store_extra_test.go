package checkpoint

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/pipeline"
)

func TestMemoryStorePresetID(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	entry := Entry{
		ID:        "my-custom-id",
		SessionID: "sess-custom",
		Remaining: &pipeline.Step{Name: "finalize", Tool: "finalizer"},
	}

	id, err := store.Save(context.Background(), entry)
	if err != nil {
		t.Fatalf("save with preset ID: %v", err)
	}
	if id != "my-custom-id" {
		t.Errorf("expected preset ID preserved, got %q", id)
	}
}

func TestMemoryStoreAutoGenerateID(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	entry := Entry{
		SessionID: "sess-auto",
	}

	id, err := store.Save(context.Background(), entry)
	if err != nil {
		t.Fatalf("save with auto ID: %v", err)
	}
	if id == "" {
		t.Error("expected auto-generated non-empty ID")
	}
}

func TestMemoryStoreAutoCreatedAt(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	entry := Entry{
		SessionID: "sess-time",
	}

	id, _ := store.Save(context.Background(), entry)
	got, err := store.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt for auto-generated timestamp")
	}
}

func TestMemoryStorePresetCreatedAt(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	presetTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	entry := Entry{
		SessionID: "sess-preset-time",
		CreatedAt: presetTime,
	}

	id, _ := store.Save(context.Background(), entry)
	got, err := store.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.CreatedAt != presetTime {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, presetTime)
	}
}

func TestMemoryStoreLoadNotFound(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	_, err := store.Load(context.Background(), "nonexistent-id")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStoreDeleteNonexistent(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	// Deleting a nonexistent ID should not error.
	if err := store.Delete(context.Background(), "nonexistent-id"); err != nil {
		t.Errorf("delete nonexistent: %v", err)
	}
}

func TestFileStoreDelete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}

	ctx := context.Background()
	id, err := store.Save(ctx, Entry{SessionID: "sess-delete"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err = store.Load(ctx, id)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestFileStoreSaveAutoID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}

	ctx := context.Background()
	id, err := store.Save(ctx, Entry{SessionID: "sess-auto-id"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if id == "" {
		t.Error("expected auto-generated ID")
	}

	got, err := store.Load(ctx, id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.SessionID != "sess-auto-id" {
		t.Errorf("SessionID = %q, want sess-auto-id", got.SessionID)
	}
}

func TestFileStoreNilSaveReturnsError(t *testing.T) {
	t.Parallel()
	var store *FileStore
	_, err := store.Save(context.Background(), Entry{SessionID: "x"})
	if !errors.Is(err, ErrStoreNil) {
		t.Errorf("nil FileStore.Save should return ErrStoreNil, got %v", err)
	}
}

func TestFileStoreNilLoadReturnsError(t *testing.T) {
	t.Parallel()
	var store *FileStore
	_, err := store.Load(context.Background(), "any-id")
	if !errors.Is(err, ErrStoreNil) {
		t.Errorf("nil FileStore.Load should return ErrStoreNil, got %v", err)
	}
}

func TestFileStoreNilDeleteReturnsError(t *testing.T) {
	t.Parallel()
	var store *FileStore
	err := store.Delete(context.Background(), "any-id")
	if !errors.Is(err, ErrStoreNil) {
		t.Errorf("nil FileStore.Delete should return ErrStoreNil, got %v", err)
	}
}

func TestErrSentinelValues(t *testing.T) {
	t.Parallel()
	if ErrNotFound.Error() != "checkpoint: not found" {
		t.Errorf("ErrNotFound = %q, want %q", ErrNotFound.Error(), "checkpoint: not found")
	}
	if ErrStoreNil.Error() != "checkpoint: store is nil" {
		t.Errorf("ErrStoreNil = %q, want %q", ErrStoreNil.Error(), "checkpoint: store is nil")
	}
}

func TestCloneEntryWithRemaining(t *testing.T) {
	t.Parallel()
	entry := Entry{
		SessionID: "sess-clone",
		Remaining: &pipeline.Step{Name: "step", Tool: "tool"},
	}
	cloned := cloneEntry(entry)
	if cloned.SessionID != "sess-clone" {
		t.Errorf("cloned SessionID = %q, want sess-clone", cloned.SessionID)
	}
	if cloned.Remaining == nil {
		t.Error("expected Remaining to be cloned")
	}
	if cloned.Remaining.Name != "step" {
		t.Errorf("cloned Remaining.Name = %q, want step", cloned.Remaining.Name)
	}

	// Mutate clone should not affect original.
	cloned.Remaining.Name = "mutated"
	if entry.Remaining.Name != "step" {
		t.Error("mutation leaked back to original Remaining")
	}
}

func TestCloneEntryWithoutRemaining(t *testing.T) {
	t.Parallel()
	entry := Entry{
		SessionID: "sess-no-remaining",
	}
	cloned := cloneEntry(entry)
	if cloned.Remaining != nil {
		t.Error("expected nil Remaining in clone when original has nil")
	}
}