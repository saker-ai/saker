package checkpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/pipeline"
)

func TestCheckpointMemoryStoreRoundTrip(t *testing.T) {
	store := NewMemoryStore()
	entry := Entry{
		SessionID: "sess-1",
		Remaining: &pipeline.Step{Name: "finalize", Tool: "finalizer"},
		Input: pipeline.Input{
			Artifacts: []artifact.ArtifactRef{
				artifact.NewGeneratedRef("art_1", artifact.ArtifactKindText),
			},
		},
		Result: pipeline.Result{Output: "prepared"},
	}

	id, err := store.Save(context.Background(), entry)
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if id == "" {
		t.Fatal("expected generated checkpoint id")
	}

	loaded, err := store.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if loaded.SessionID != "sess-1" {
		t.Fatalf("expected session id to round-trip, got %+v", loaded)
	}
	if loaded.Remaining == nil || loaded.Remaining.Name != "finalize" {
		t.Fatalf("expected remaining step to round-trip, got %+v", loaded.Remaining)
	}
	if len(loaded.Input.Artifacts) != 1 || loaded.Input.Artifacts[0].ArtifactID != "art_1" {
		t.Fatalf("expected input artifacts to round-trip, got %+v", loaded.Input)
	}
	if loaded.Result.Output != "prepared" {
		t.Fatalf("expected snapshot result to round-trip, got %+v", loaded.Result)
	}
}

func TestCheckpointMemoryStoreDelete(t *testing.T) {
	store := NewMemoryStore()
	id, err := store.Save(context.Background(), Entry{SessionID: "sess-2"})
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if err := store.Delete(context.Background(), id); err != nil {
		t.Fatalf("delete checkpoint: %v", err)
	}
	if _, err := store.Load(context.Background(), id); err == nil {
		t.Fatal("expected missing checkpoint after delete")
	}
}

func TestCheckpointFileStoreRoundTrip(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "checkpoints.json"))
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	entry := Entry{
		SessionID: "sess-file",
		Remaining: &pipeline.Step{Name: "resume", Tool: "runner"},
		Result:    pipeline.Result{Output: "paused"},
	}

	id, err := store.Save(context.Background(), entry)
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	reloaded, err := NewFileStore(store.path)
	if err != nil {
		t.Fatalf("reload file store: %v", err)
	}
	got, err := reloaded.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if got.SessionID != "sess-file" || got.Result.Output != "paused" {
		t.Fatalf("unexpected file-backed checkpoint: %+v", got)
	}
}

func TestMemoryStoreNilSaveReturnsError(t *testing.T) {
	var store *MemoryStore // nil pointer
	_, err := store.Save(context.Background(), Entry{SessionID: "x"})
	if !errors.Is(err, ErrStoreNil) {
		t.Fatalf("nil MemoryStore.Save should return ErrStoreNil, got %v", err)
	}
}

func TestMemoryStoreNilLoadReturnsError(t *testing.T) {
	var store *MemoryStore
	_, err := store.Load(context.Background(), "any-id")
	if !errors.Is(err, ErrStoreNil) {
		t.Fatalf("nil MemoryStore.Load should return ErrStoreNil, got %v", err)
	}
}

func TestMemoryStoreNilDeleteReturnsError(t *testing.T) {
	var store *MemoryStore
	err := store.Delete(context.Background(), "any-id")
	if !errors.Is(err, ErrStoreNil) {
		t.Fatalf("nil MemoryStore.Delete should return ErrStoreNil, got %v", err)
	}
}

func TestCloneEntryInputMutationIsolation(t *testing.T) {
	store := NewMemoryStore()
	original := Entry{
		SessionID: "sess-clone",
		Input: pipeline.Input{
			Artifacts: []artifact.ArtifactRef{
				artifact.NewGeneratedRef("a1", artifact.ArtifactKindImage),
				artifact.NewGeneratedRef("a2", artifact.ArtifactKindImage),
			},
			Collections: map[string][]artifact.ArtifactRef{
				"frames": {
					artifact.NewGeneratedRef("f1", artifact.ArtifactKindImage),
				},
			},
		},
		Result: pipeline.Result{
			Output: "original-output",
			Artifacts: []artifact.ArtifactRef{
				artifact.NewGeneratedRef("out1", artifact.ArtifactKindText),
			},
			Items: []pipeline.Result{
				{Output: "item1"},
				{Output: "item2"},
			},
		},
	}

	id, err := store.Save(context.Background(), original)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := store.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Mutate loaded.Input.Artifacts — should NOT affect store
	loaded.Input.Artifacts[0] = artifact.NewGeneratedRef("mutated", artifact.ArtifactKindText)
	loaded.Input.Collections["frames"][0] = artifact.NewGeneratedRef("mutated-frame", artifact.ArtifactKindText)

	// Mutate loaded.Result — should NOT affect store
	loaded.Result.Artifacts[0] = artifact.NewGeneratedRef("mutated-out", artifact.ArtifactKindText)
	loaded.Result.Items[0].Output = "mutated-item"

	reloaded, err := store.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if reloaded.Input.Artifacts[0].ArtifactID == "mutated" {
		t.Fatal("Input.Artifacts mutation leaked back to store — deep copy broken")
	}
	if reloaded.Input.Collections["frames"][0].ArtifactID == "mutated-frame" {
		t.Fatal("Input.Collections mutation leaked back to store — deep copy broken")
	}
	if reloaded.Result.Artifacts[0].ArtifactID == "mutated-out" {
		t.Fatal("Result.Artifacts mutation leaked back to store — deep copy broken")
	}
	if reloaded.Result.Items[0].Output == "mutated-item" {
		t.Fatal("Result.Items mutation leaked back to store — deep copy broken")
	}
}

func TestCloneEntrySaveMutationIsolation(t *testing.T) {
	store := NewMemoryStore()
	entry := Entry{
		SessionID: "sess-save-isolation",
		Input: pipeline.Input{
			Artifacts: []artifact.ArtifactRef{
				artifact.NewGeneratedRef("a1", artifact.ArtifactKindImage),
			},
		},
	}

	id, err := store.Save(context.Background(), entry)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// Mutate the original entry AFTER save — should NOT affect store
	entry.Input.Artifacts[0] = artifact.NewGeneratedRef("mutated-after-save", artifact.ArtifactKindText)

	loaded, err := store.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Input.Artifacts[0].ArtifactID == "mutated-after-save" {
		t.Fatal("post-save mutation of original leaked into store — deep copy on save broken")
	}
}

func TestFileStoreAtomicWriteNoCorruption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}

	// Save a valid entry
	id, err := store.Save(context.Background(), Entry{SessionID: "valid-data"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// Simulate a crash that left a .tmp file with garbage
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte("corrupted partial write"), 0o600); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	// Reload — the valid data file should be intact
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reload after .tmp corruption: %v", err)
	}
	entry, err := reloaded.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load after .tmp corruption: %v", err)
	}
	if entry.SessionID != "valid-data" {
		t.Fatalf("data corrupted after .tmp leftover: got %q", entry.SessionID)
	}

	// Clean up — .tmp should NOT have overwritten the real file
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read data file: %v", err)
	}
	if string(data) == "corrupted partial write" {
		t.Fatal("tmp file overwrote real data — atomic write not working")
	}
}

func TestFileStoreTmpCleanedAfterSuccessfulWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}

	_, err = store.Save(context.Background(), Entry{SessionID: "check-tmp"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// After a successful write, .tmp should NOT exist (rename removes it)
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("expected .tmp to be cleaned up after successful write, but it exists")
	}
}
