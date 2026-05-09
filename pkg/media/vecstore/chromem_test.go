package vecstore

import (
	"context"
	"testing"
)

func TestChromemStore_AddAndSearch(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()

	// Add documents with pre-computed embeddings
	vec1 := []float32{1.0, 0.0, 0.0}
	vec2 := []float32{0.0, 1.0, 0.0}
	vec3 := []float32{0.7, 0.7, 0.0}

	if err := store.Add(ctx, "doc1", vec1, map[string]string{"source_file": "a.mp4"}); err != nil {
		t.Fatalf("add doc1: %v", err)
	}
	if err := store.Add(ctx, "doc2", vec2, map[string]string{"source_file": "b.mp4"}); err != nil {
		t.Fatalf("add doc2: %v", err)
	}
	if err := store.Add(ctx, "doc3", vec3, map[string]string{"source_file": "c.mp4"}); err != nil {
		t.Fatalf("add doc3: %v", err)
	}

	// Search with a query vector close to vec1
	query := []float32{0.9, 0.1, 0.0}
	hits, err := store.Search(ctx, query, 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}

	// First hit should be doc1 (most similar to query)
	if hits[0].ID != "doc1" {
		t.Errorf("expected first hit doc1, got %s", hits[0].ID)
	}
	if hits[0].Score <= 0 {
		t.Errorf("expected positive score, got %f", hits[0].Score)
	}
	if hits[0].Metadata["source_file"] != "a.mp4" {
		t.Errorf("expected source_file a.mp4, got %s", hits[0].Metadata["source_file"])
	}
}

func TestChromemStore_Stats(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	stats := store.Stats()
	if stats.DocumentCount != 0 {
		t.Errorf("expected 0 documents, got %d", stats.DocumentCount)
	}
	if stats.Backend != "chromem-go" {
		t.Errorf("expected backend chromem-go, got %s", stats.Backend)
	}

	ctx := context.Background()
	_ = store.Add(ctx, "x", []float32{1, 0, 0}, nil)

	stats = store.Stats()
	if stats.DocumentCount != 1 {
		t.Errorf("expected 1 document, got %d", stats.DocumentCount)
	}
}

func TestChromemStore_Remove(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()
	_ = store.Add(ctx, "a", []float32{1, 0}, nil)
	_ = store.Add(ctx, "b", []float32{0, 1}, nil)

	if store.Stats().DocumentCount != 2 {
		t.Fatal("expected 2 documents")
	}

	if err := store.Remove(ctx, "a"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if store.Stats().DocumentCount != 1 {
		t.Errorf("expected 1 document after remove, got %d", store.Stats().DocumentCount)
	}
}

func TestChromemStore_SearchEmpty(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	hits, err := store.Search(context.Background(), []float32{1, 0, 0}, 5)
	if err != nil {
		t.Fatalf("search empty: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits from empty store, got %d", len(hits))
	}
}
