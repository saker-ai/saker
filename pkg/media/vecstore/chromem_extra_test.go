package vecstore

import (
	"context"
	"strings"
	"testing"
)

func TestChromemStore_AddBatch(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{Dimensions: 3})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()
	ids := []string{"b1", "b2", "b3"}
	vectors := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.5, 0.5, 0.0},
	}
	metadatas := []map[string]string{
		{"source_file": "batch1.mp4"},
		{"source_file": "batch2.mp4"},
		{"source_file": "batch3.mp4", "content": "batch content"},
	}

	if err := store.AddBatch(ctx, ids, vectors, metadatas); err != nil {
		t.Fatalf("AddBatch: %v", err)
	}

	if store.Stats().DocumentCount != 3 {
		t.Errorf("expected 3 documents after AddBatch, got %d", store.Stats().DocumentCount)
	}

	hits, err := store.Search(ctx, []float32{1.0, 0.0, 0.0}, 3)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 3 {
		t.Errorf("expected 3 hits, got %d", len(hits))
	}
}

func TestChromemStore_AddBatchWithContent(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{Dimensions: 2})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()
	ids := []string{"c1"}
	vectors := [][]float32{{1.0, 0.0}}
	metadatas := []map[string]string{
		{"content": "custom content text"},
	}

	if err := store.AddBatch(ctx, ids, vectors, metadatas); err != nil {
		t.Fatalf("AddBatch with content: %v", err)
	}
}

func TestChromemStore_DimensionMismatch(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{Dimensions: 3})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()

	if err := store.Add(ctx, "d1", []float32{1, 0, 0}, nil); err != nil {
		t.Fatalf("first add: %v", err)
	}

	err = store.Add(ctx, "d2", []float32{1, 0, 0, 0}, nil)
	if err == nil {
		t.Error("expected dimension mismatch error")
	}
	if !strings.Contains(err.Error(), "dimension mismatch") {
		t.Errorf("error = %q, want 'dimension mismatch'", err.Error())
	}
}

func TestChromemStore_AutoDetectDimensions(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{Dimensions: 0})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()

	if err := store.Add(ctx, "a1", []float32{1, 0, 0, 0}, nil); err != nil {
		t.Fatalf("auto-detect add: %v", err)
	}

	if err := store.Add(ctx, "a2", []float32{0, 1, 0, 0}, nil); err != nil {
		t.Fatalf("same dims add: %v", err)
	}

	err = store.Add(ctx, "a3", []float32{1, 0}, nil)
	if err == nil {
		t.Error("expected dimension mismatch after auto-detect")
	}
}

func TestChromemStore_SearchNDefaults(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{Dimensions: 2})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()
	_ = store.Add(ctx, "s1", []float32{1, 0}, nil)
	_ = store.Add(ctx, "s2", []float32{0, 1}, nil)

	hits, err := store.Search(ctx, []float32{1, 0}, 0)
	if err != nil {
		t.Fatalf("search n=0: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("expected 2 hits with n=0, got %d", len(hits))
	}
}

func TestChromemStore_SearchNClamped(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{Dimensions: 2})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()
	_ = store.Add(ctx, "c1", []float32{1, 0}, nil)
	_ = store.Add(ctx, "c2", []float32{0, 1}, nil)

	hits, err := store.Search(ctx, []float32{1, 0}, 100)
	if err != nil {
		t.Fatalf("search n=100: %v", err)
	}
	if len(hits) > 2 {
		t.Errorf("expected at most 2 hits with n=100, got %d", len(hits))
	}
}

func TestChromemStore_PersistentStorage(t *testing.T) {
	dir := t.TempDir()
	store, err := NewChromemStore(ChromemOptions{
		PersistDir:     dir,
		CollectionName: "test_persist",
		Dimensions:     3,
	})
	if err != nil {
		t.Fatalf("create persistent store: %v", err)
	}

	ctx := context.Background()
	_ = store.Add(ctx, "p1", []float32{1, 0, 0}, map[string]string{"key": "value"})

	stats := store.Stats()
	if stats.DocumentCount != 1 {
		t.Errorf("expected 1 document, got %d", stats.DocumentCount)
	}
	if stats.Backend != "chromem-go" {
		t.Errorf("expected backend chromem-go, got %s", stats.Backend)
	}
}

func TestChromemStore_NilMetadata(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{Dimensions: 2})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()
	if err := store.Add(ctx, "nil-meta", []float32{1, 0}, nil); err != nil {
		t.Fatalf("add with nil metadata: %v", err)
	}

	hits, err := store.Search(ctx, []float32{1, 0}, 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].ID != "nil-meta" {
		t.Errorf("ID = %q, want nil-meta", hits[0].ID)
	}
}

func TestHitStruct(t *testing.T) {
	t.Parallel()
	hit := Hit{
		ID:       "test-id",
		Score:    0.95,
		Metadata: map[string]string{"key": "val"},
	}
	if hit.ID != "test-id" {
		t.Errorf("Hit.ID = %q, want test-id", hit.ID)
	}
	if hit.Score != 0.95 {
		t.Errorf("Hit.Score = %v, want 0.95", hit.Score)
	}
}

func TestStoreStatsStruct(t *testing.T) {
	t.Parallel()
	stats := StoreStats{
		DocumentCount: 5,
		Backend:       "chromem-go",
	}
	if stats.DocumentCount != 5 {
		t.Errorf("StoreStats.DocumentCount = %d, want 5", stats.DocumentCount)
	}
	if stats.Backend != "chromem-go" {
		t.Errorf("StoreStats.Backend = %q, want chromem-go", stats.Backend)
	}
}

func TestChromemStore_AddBatchDimensionMismatch(t *testing.T) {
	store, err := NewChromemStore(ChromemOptions{Dimensions: 3})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()
	ids := []string{"b1", "b2"}
	vectors := [][]float32{
		{1, 0, 0},
		{1, 0, 0, 0}, // wrong dimensions
	}

	err = store.AddBatch(ctx, ids, vectors, nil)
	if err == nil {
		t.Error("expected dimension mismatch error in AddBatch")
	}
	if !strings.Contains(err.Error(), "vector 1") {
		t.Errorf("error should reference vector index, got %q", err.Error())
	}
}