package vecstore

import (
	"context"
	"fmt"
	"sync"

	chromem "github.com/philippgille/chromem-go"
)

// ChromemStore wraps chromem-go as an embedded vector database.
type ChromemStore struct {
	db         *chromem.DB
	collection *chromem.Collection
	dims       int // expected vector dimensions; 0 means auto-detect from first Add
	mu         sync.RWMutex
}

// ChromemOptions configures the chromem-go store.
type ChromemOptions struct {
	// PersistDir is the directory for persistent storage.
	// If empty, uses in-memory storage.
	PersistDir string
	// CollectionName is the name of the vector collection (default "media_chunks").
	CollectionName string
	// Compress enables gzip compression for persistent storage.
	Compress bool
	// Dimensions is the expected embedding dimensionality.
	// If 0, auto-detected from the first Add call.
	Dimensions int
}

// NewChromemStore creates a new chromem-go backed vector store.
func NewChromemStore(opts ChromemOptions) (*ChromemStore, error) {
	var db *chromem.DB
	var err error

	if opts.PersistDir != "" {
		db, err = chromem.NewPersistentDB(opts.PersistDir, opts.Compress)
	} else {
		db = chromem.NewDB()
	}
	if err != nil {
		return nil, fmt.Errorf("create chromem db: %w", err)
	}

	collName := opts.CollectionName
	if collName == "" {
		collName = "media_chunks"
	}

	// Use a no-op embedding function since we provide pre-computed embeddings.
	noopEmbed := func(_ context.Context, _ string) ([]float32, error) {
		return nil, fmt.Errorf("embedding should be pre-computed")
	}

	collection, err := db.GetOrCreateCollection(collName, nil, noopEmbed)
	if err != nil {
		return nil, fmt.Errorf("create collection: %w", err)
	}

	return &ChromemStore{
		db:         db,
		collection: collection,
		dims:       opts.Dimensions,
	}, nil
}

// Add inserts a document with a pre-computed embedding vector.
func (s *ChromemStore) Add(ctx context.Context, id string, vector []float32, metadata map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDims(len(vector)); err != nil {
		return err
	}

	content := id // use ID as content placeholder
	if v, ok := metadata["content"]; ok {
		content = v
	}

	return s.collection.Add(ctx,
		[]string{id},
		[][]float32{vector},
		[]map[string]string{metadata},
		[]string{content},
	)
}

// AddBatch inserts multiple documents with pre-computed embeddings.
func (s *ChromemStore) AddBatch(ctx context.Context, ids []string, vectors [][]float32, metadatas []map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, v := range vectors {
		if err := s.checkDims(len(v)); err != nil {
			return fmt.Errorf("vector %d: %w", i, err)
		}
	}

	contents := make([]string, len(ids))
	for i, id := range ids {
		contents[i] = id
		if metadatas != nil && i < len(metadatas) {
			if v, ok := metadatas[i]["content"]; ok {
				contents[i] = v
			}
		}
	}

	return s.collection.Add(ctx, ids, vectors, metadatas, contents)
}

// Search finds the n most similar documents to the query vector.
func (s *ChromemStore) Search(ctx context.Context, query []float32, n int) ([]Hit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.collection.Count() == 0 {
		return nil, nil
	}

	if n <= 0 {
		n = 5
	}
	// Clamp n to collection size
	if count := s.collection.Count(); n > count {
		n = count
	}

	results, err := s.collection.QueryEmbedding(ctx, query, n, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	hits := make([]Hit, len(results))
	for i, r := range results {
		hits[i] = Hit{
			ID:       r.ID,
			Score:    float64(r.Similarity),
			Metadata: r.Metadata,
		}
	}

	return hits, nil
}

// Remove deletes a document by ID.
func (s *ChromemStore) Remove(ctx context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.collection.Delete(ctx, nil, nil, ids...)
}

// checkDims validates vector dimensions. Must be called under s.mu lock.
// On the first call with dims==0, auto-detects from the provided length.
func (s *ChromemStore) checkDims(n int) error {
	if s.dims == 0 {
		s.dims = n
		return nil
	}
	if n != s.dims {
		return fmt.Errorf("dimension mismatch: got %d, expected %d", n, s.dims)
	}
	return nil
}

// Stats returns store statistics.
func (s *ChromemStore) Stats() StoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StoreStats{
		DocumentCount: s.collection.Count(),
		Backend:       "chromem-go",
	}
}
