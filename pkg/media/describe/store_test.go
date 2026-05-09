package describe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_AppendAndSearch(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "desc.jsonl")
	store := NewStore(storePath)

	ann1 := &Annotation{
		Segment: Segment{
			SourceFile: "video1.mp4",
			StartTime:  0,
			EndTime:    30,
		},
		Visual:     "A red car driving on a highway",
		Scene:      "outdoor, daytime, urban",
		Action:     "car moving fast",
		Entity:     "red sedan, highway",
		SearchTags: []string{"red car", "highway", "driving", "红色汽车"},
	}

	ann2 := &Annotation{
		Segment: Segment{
			SourceFile: "video1.mp4",
			StartTime:  30,
			EndTime:    60,
		},
		Visual:     "A person walking a dog in a park",
		Scene:      "outdoor, daytime, park",
		Action:     "walking",
		Entity:     "person, golden retriever",
		SearchTags: []string{"dog", "park", "walking", "遛狗"},
	}

	if err := store.Append(ann1); err != nil {
		t.Fatalf("append ann1: %v", err)
	}
	if err := store.Append(ann2); err != nil {
		t.Fatalf("append ann2: %v", err)
	}

	// Search for "red car"
	results, err := store.Search("red car", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'red car'")
	}

	// First result should be the car segment
	if results[0].Segment.SourceFile != "video1.mp4" {
		t.Errorf("expected video1.mp4, got %s", results[0].Segment.SourceFile)
	}
	if results[0].Segment.StartTime != 0 {
		t.Errorf("expected start_time 0, got %f", results[0].Segment.StartTime)
	}

	// Search for "dog"
	results, err = store.Search("dog", 5)
	if err != nil {
		t.Fatalf("search dog: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'dog'")
	}
	if results[0].Segment.StartTime != 30 {
		t.Errorf("expected start_time 30 for dog result, got %f", results[0].Segment.StartTime)
	}
}

func TestStore_Count(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(filepath.Join(tmpDir, "desc.jsonl"))

	if store.Count() != 0 {
		t.Error("expected 0 count for empty store")
	}

	_ = store.Append(&Annotation{Segment: Segment{SourceFile: "a.mp4"}})
	_ = store.Append(&Annotation{Segment: Segment{SourceFile: "b.mp4"}})

	if store.Count() != 2 {
		t.Errorf("expected 2, got %d", store.Count())
	}
}

func TestStore_SearchNonexistent(t *testing.T) {
	store := NewStore("/nonexistent/path.jsonl")
	results, err := store.Search("anything", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestStore_SearchNoMatch(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(filepath.Join(tmpDir, "desc.jsonl"))

	_ = store.Append(&Annotation{
		Segment:    Segment{SourceFile: "a.mp4"},
		Visual:     "sunset over mountains",
		SearchTags: []string{"sunset", "mountains"},
	})

	results, err := store.Search("airplane", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for unrelated query, got %d", len(results))
	}
}

func TestStore_PersistsToFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "desc.jsonl")
	store := NewStore(path)

	_ = store.Append(&Annotation{
		Segment:    Segment{SourceFile: "a.mp4"},
		Visual:     "test content",
		SearchTags: []string{"test"},
	})

	// Verify file exists
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("file is empty")
	}

	// Create new store instance pointing to same file
	store2 := NewStore(path)
	results, err := store2.Search("test", 5)
	if err != nil {
		t.Fatalf("search from new instance: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result from persisted store, got %d", len(results))
	}
}
