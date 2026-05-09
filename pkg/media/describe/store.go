package describe

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// SearchResult represents a text-search hit from the description store.
type SearchResult struct {
	Segment  Segment        `json:"segment"`
	Score    float64        `json:"score"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Searcher can search annotations by text query.
type Searcher interface {
	Search(query string, maxResults int) ([]SearchResult, error)
}

// Store persists annotations as JSONL and provides text-based search.
type Store struct {
	path  string
	count int // cached line count, -1 means uninitialized
	mu    sync.RWMutex
}

// NewStore creates a JSONL-backed description store.
func NewStore(path string) *Store {
	return &Store{path: path, count: -1}
}

// MultiStore searches across all JSONL files in a directory.
type MultiStore struct {
	dir string
}

// NewMultiStore creates a store that searches all *.jsonl files in dir.
func NewMultiStore(dir string) *MultiStore {
	return &MultiStore{dir: dir}
}

// Search scans all JSONL files in the directory and returns merged results
// sorted by score.
func (ms *MultiStore) Search(query string, maxResults int) ([]SearchResult, error) {
	entries, err := os.ReadDir(ms.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var allResults []SearchResult
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		store := NewStore(filepath.Join(ms.dir, e.Name()))
		results, err := store.Search(query, 0) // 0 = no per-file limit
		if err != nil {
			slog.Warn("multistore: search failed", "file", e.Name(), "error", err)
			continue
		}
		allResults = append(allResults, results...)
	}

	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Score > allResults[j].Score
	})

	if maxResults > 0 && len(allResults) > maxResults {
		allResults = allResults[:maxResults]
	}
	return allResults, nil
}

// Append writes an annotation to the JSONL file.
func (s *Store) Append(ann *Annotation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure parent directory exists.
	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create store dir: %w", err)
		}
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(ann)
	if err != nil {
		return fmt.Errorf("marshal annotation: %w", err)
	}

	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	if s.count >= 0 {
		s.count++
	}
	return nil
}

// Search scans all annotations for text matches against the query.
// Returns results sorted by relevance score (multi-track weighted scoring).
func (s *Store) Search(query string, maxResults int) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	queryLower := strings.ToLower(query)
	queryTerms := strings.Fields(queryLower)

	var results []SearchResult
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max line
	for scanner.Scan() {
		var ann Annotation
		if err := json.Unmarshal(scanner.Bytes(), &ann); err != nil {
			continue
		}

		score := scoreAnnotation(&ann, queryTerms)
		if score > 0 {
			results = append(results, SearchResult{
				Segment: ann.Segment,
				Score:   score,
				Metadata: map[string]any{
					"visual": ann.Visual,
					"scene":  ann.Scene,
					"action": ann.Action,
				},
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}

	return results, scanner.Err()
}

// Count returns the number of stored annotations.
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.count >= 0 {
		return s.count
	}

	// First call: scan file to initialize cache.
	f, err := os.Open(s.path)
	if err != nil {
		s.count = 0
		return 0
	}
	defer f.Close()

	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		n++
	}
	s.count = n
	return n
}

// scoreAnnotation computes a weighted relevance score for an annotation
// against query terms. Track weights:
//
//	search_tags: 3.0, entity: 2.5, action: 2.0, visual: 1.5, scene: 1.0, text: 1.0, audio: 0.5
func scoreAnnotation(ann *Annotation, queryTerms []string) float64 {
	type track struct {
		text   string
		weight float64
	}

	// Pre-lowercase all track texts once to avoid repeated allocations.
	tracks := []track{
		{strings.ToLower(strings.Join(ann.SearchTags, " ")), 3.0},
		{strings.ToLower(ann.Entity), 2.5},
		{strings.ToLower(ann.Action), 2.0},
		{strings.ToLower(ann.Visual), 1.5},
		{strings.ToLower(ann.Scene), 1.0},
		{strings.ToLower(ann.Text), 1.0},
		{strings.ToLower(ann.Audio), 0.5},
	}

	var totalScore float64
	for _, term := range queryTerms {
		for _, t := range tracks {
			count := strings.Count(t.text, term)
			if count > 0 {
				totalScore += t.weight * math.Log1p(float64(count))
			}
		}
	}

	return totalScore
}
