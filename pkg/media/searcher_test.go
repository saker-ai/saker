package media

import (
	"context"
	"fmt"
	"testing"

	"github.com/cinience/saker/pkg/media/describe"
	"github.com/cinience/saker/pkg/media/vecstore"
)

// mockEmbedder implements embedding.Embedder for testing.
type mockEmbedder struct {
	dims     int
	queryVec []float32
	queryErr error
	videoVec []float32
	videoErr error
}

func (m *mockEmbedder) EmbedVideo(_ context.Context, _ string) ([]float32, error) {
	return m.videoVec, m.videoErr
}

func (m *mockEmbedder) EmbedQuery(_ context.Context, _ string) ([]float32, error) {
	return m.queryVec, m.queryErr
}

func (m *mockEmbedder) Dimensions() int { return m.dims }

// mockDescSearcher implements describe.Searcher for testing.
type mockDescSearcher struct {
	results []describe.SearchResult
	err     error
}

func (m *mockDescSearcher) Search(_ string, _ int) ([]describe.SearchResult, error) {
	return m.results, m.err
}

func TestSearchEngineUnknown(t *testing.T) {
	t.Parallel()
	s := &Searcher{}
	_, err := s.Search(context.Background(), "test", SearchOptions{Engine: "unknown"})
	if err == nil {
		t.Error("Search with unknown engine: expected error, got nil")
	}
	want := "unknown engine: unknown"
	if err.Error() != want {
		t.Errorf("Search unknown engine error = %q, want %q", err.Error(), want)
	}
}

func TestSearchMaxResultsDefault(t *testing.T) {
	t.Parallel()
	s := &Searcher{}
	opts := SearchOptions{MaxResults: 0, Engine: EngineVector}
	_, err := s.Search(context.Background(), "test", opts)
	// MaxResults=0 is defaulted to 5 internally, then vectorSearch runs.
	if err == nil {
		t.Error("expected error when vector engine not configured")
	}
	if err.Error() != "vector engine not configured" {
		t.Errorf("expected 'vector engine not configured', got %q", err.Error())
	}
}

func TestVectorSearchNotConfigured(t *testing.T) {
	t.Parallel()
	s := &Searcher{}
	_, err := s.vectorSearch(context.Background(), "query", 5)
	if err == nil {
		t.Error("vectorSearch with nil Embedder/VecStore: expected error")
	}
	if err.Error() != "vector engine not configured" {
		t.Errorf("vectorSearch error = %q, want %q", err.Error(), "vector engine not configured")
	}

	// Embedder set but VecStore nil.
	s2 := &Searcher{Embedder: &mockEmbedder{dims: 3}}
	_, err = s2.vectorSearch(context.Background(), "query", 5)
	if err == nil {
		t.Error("vectorSearch with nil VecStore: expected error")
	}

	// VecStore set but Embedder nil.
	vec, _ := vecstore.NewChromemStore(vecstore.ChromemOptions{Dimensions: 3})
	s3 := &Searcher{VecStore: vec}
	_, err = s3.vectorSearch(context.Background(), "query", 5)
	if err == nil {
		t.Error("vectorSearch with nil Embedder: expected error")
	}
}

func TestVLMSearchNotConfigured(t *testing.T) {
	t.Parallel()
	s := &Searcher{}
	_, err := s.vlmSearch("query", 5)
	if err == nil {
		t.Error("vlmSearch with nil DescStore: expected error")
	}
	if err.Error() != "VLM engine not configured" {
		t.Errorf("vlmSearch error = %q, want %q", err.Error(), "VLM engine not configured")
	}
}

func TestVLMSearchWithMock(t *testing.T) {
	t.Parallel()
	s := &Searcher{
		DescStore: &mockDescSearcher{
			results: []describe.SearchResult{
				{
					Segment: describe.Segment{
						SourceFile: "video.mp4",
						StartTime:  0,
						EndTime:    30,
						Duration:   30,
					},
					Score:    0.95,
					Metadata: map[string]any{"visual": "a red car"},
				},
			},
		},
	}

	results, err := s.vlmSearch("red car", 5)
	if err != nil {
		t.Fatalf("vlmSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Engine != EngineVLM {
		t.Errorf("Engine = %q, want %q", results[0].Engine, EngineVLM)
	}
	if results[0].Segment.SourceFile != "video.mp4" {
		t.Errorf("SourceFile = %q, want %q", results[0].Segment.SourceFile, "video.mp4")
	}
	if results[0].Score != 0.95 {
		t.Errorf("Score = %v, want 0.95", results[0].Score)
	}
}

func TestVLMSearchError(t *testing.T) {
	t.Parallel()
	s := &Searcher{
		DescStore: &mockDescSearcher{
			err: fmt.Errorf("search engine down"),
		},
	}
	_, err := s.vlmSearch("query", 5)
	if err == nil {
		t.Error("vlmSearch with error mock: expected error")
	}
}

func TestSegmentKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		seg  MediaSegment
		want string
	}{
		{
			seg:  MediaSegment{SourceFile: "video.mp4", StartTime: 10.5},
			want: "video.mp4:10.500",
		},
		{
			seg:  MediaSegment{SourceFile: "clip.avi", StartTime: 0},
			want: "clip.avi:0.000",
		},
		{
			seg:  MediaSegment{SourceFile: "", StartTime: 123.456},
			want: ":123.456",
		},
	}
	for _, tt := range tests {
		got := segmentKey(tt.seg)
		if got != tt.want {
			t.Errorf("segmentKey(%v) = %q, want %q", tt.seg, got, tt.want)
		}
	}
}

func TestHitToSegment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		hit  vecstore.Hit
		want MediaSegment
	}{
		{
			name: "full metadata",
			hit: vecstore.Hit{
				ID:    "doc1",
				Score: 0.95,
				Metadata: map[string]string{
					"source_file": "video.mp4",
					"start_time":  "10.500",
					"end_time":    "40.500",
				},
			},
			want: MediaSegment{
				SourceFile: "video.mp4",
				StartTime:  10.5,
				EndTime:    40.5,
				Duration:   30.0,
			},
		},
		{
			name: "nil metadata",
			hit:  vecstore.Hit{ID: "doc2", Score: 0.8, Metadata: nil},
			want: MediaSegment{},
		},
		{
			name: "missing fields",
			hit: vecstore.Hit{
				ID:    "doc3",
				Score: 0.7,
				Metadata: map[string]string{
					"source_file": "a.mp4",
				},
			},
			want: MediaSegment{SourceFile: "a.mp4"},
		},
		{
			name: "non-numeric start_time",
			hit: vecstore.Hit{
				ID:    "doc4",
				Score: 0.5,
				Metadata: map[string]string{
					"source_file": "b.mp4",
					"start_time":  "not-a-number",
					"end_time":    "60.000",
				},
			},
			want: MediaSegment{SourceFile: "b.mp4", EndTime: 60.0, Duration: 60.0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hitToSegment(tt.hit)
			if got.SourceFile != tt.want.SourceFile {
				t.Errorf("SourceFile = %q, want %q", got.SourceFile, tt.want.SourceFile)
			}
			if got.StartTime != tt.want.StartTime {
				t.Errorf("StartTime = %v, want %v", got.StartTime, tt.want.StartTime)
			}
			if got.EndTime != tt.want.EndTime {
				t.Errorf("EndTime = %v, want %v", got.EndTime, tt.want.EndTime)
			}
			if got.Duration != tt.want.Duration {
				t.Errorf("Duration = %v, want %v", got.Duration, tt.want.Duration)
			}
		})
	}
}

func TestVectorSearchWithMock(t *testing.T) {
	t.Parallel()
	vec, err := vecstore.NewChromemStore(vecstore.ChromemOptions{Dimensions: 3})
	if err != nil {
		t.Fatalf("create vecstore: %v", err)
	}

	ctx := context.Background()
	docVec := []float32{1.0, 0.0, 0.0}
	if err := vec.Add(ctx, "doc1", docVec, map[string]string{
		"source_file": "test.mp4",
		"start_time":  "0.000",
		"end_time":    "30.000",
	}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	embedder := &mockEmbedder{
		dims:     3,
		queryVec: []float32{0.9, 0.1, 0.0},
	}

	s := &Searcher{
		Embedder: embedder,
		VecStore: vec,
	}

	results, err := s.vectorSearch(ctx, "test query", 5)
	if err != nil {
		t.Fatalf("vectorSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Engine != EngineVector {
		t.Errorf("Engine = %q, want %q", results[0].Engine, EngineVector)
	}
	if results[0].Segment.SourceFile != "test.mp4" {
		t.Errorf("SourceFile = %q, want %q", results[0].Segment.SourceFile, "test.mp4")
	}
	if results[0].Metadata["vec_id"] != "doc1" {
		t.Errorf("Metadata vec_id = %v, want doc1", results[0].Metadata["vec_id"])
	}
}

func TestVectorSearchEmbedError(t *testing.T) {
	t.Parallel()
	vec, err := vecstore.NewChromemStore(vecstore.ChromemOptions{Dimensions: 3})
	if err != nil {
		t.Fatalf("create vecstore: %v", err)
	}

	embedder := &mockEmbedder{
		dims:     3,
		queryErr: fmt.Errorf("API timeout"),
	}

	s := &Searcher{
		Embedder: embedder,
		VecStore: vec,
	}

	_, err = s.vectorSearch(context.Background(), "query", 5)
	if err == nil {
		t.Error("expected error from embed failure")
	}
	if err.Error() != "embed query: API timeout" {
		t.Errorf("error = %q, want %q", err.Error(), "embed query: API timeout")
	}
}

func TestHybridSearchBothEnginesFail(t *testing.T) {
	t.Parallel()
	embedder := &mockEmbedder{
		dims:     3,
		queryErr: fmt.Errorf("embed fail"),
	}
	vec, _ := vecstore.NewChromemStore(vecstore.ChromemOptions{Dimensions: 3})

	s := &Searcher{
		Embedder: embedder,
		VecStore: vec,
		// DescStore is nil, so vlmSearch will also fail
	}

	// When neither engine is available (nil DescStore + embedder fails),
	// hybridSearch returns empty results, no error. The error condition
	// only triggers when goroutines actually run and both fail.
	results, err := s.hybridSearch(context.Background(), "query", SearchOptions{MaxResults: 5})
	if err != nil {
		t.Errorf("hybridSearch with no engines configured: unexpected error %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestHybridSearchOnlyVLM(t *testing.T) {
	t.Parallel()
	s := &Searcher{
		DescStore: &mockDescSearcher{
			results: []describe.SearchResult{
				{
					Segment: describe.Segment{
						SourceFile: "video.mp4",
						StartTime:  0,
						EndTime:    30,
						Duration:   30,
					},
					Score:    0.85,
					Metadata: map[string]any{"visual": "a dog"},
				},
			},
		},
	}

	results, err := s.hybridSearch(context.Background(), "dog", SearchOptions{
		MaxResults:   5,
		VectorWeight: 0.5,
		VLMWeight:    0.5,
	})
	if err != nil {
		t.Fatalf("hybridSearch with only VLM: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Engine != EngineHybrid {
		t.Errorf("Engine = %q, want %q", results[0].Engine, EngineHybrid)
	}
}

func TestHybridSearchOnlyVector(t *testing.T) {
	t.Parallel()
	vec, _ := vecstore.NewChromemStore(vecstore.ChromemOptions{Dimensions: 3})
	ctx := context.Background()
	if err := vec.Add(ctx, "v1", []float32{1, 0, 0}, map[string]string{
		"source_file": "vid.mp4",
		"start_time":  "5.000",
		"end_time":    "35.000",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	embedder := &mockEmbedder{
		dims:     3,
		queryVec: []float32{0.9, 0.1, 0.0},
	}

	s := &Searcher{
		Embedder: embedder,
		VecStore: vec,
		// DescStore is nil
	}

	results, err := s.hybridSearch(ctx, "query", SearchOptions{
		MaxResults:   5,
		VectorWeight: 0.5,
		VLMWeight:    0.5,
	})
	if err != nil {
		t.Fatalf("hybridSearch with only vector: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Engine != EngineHybrid {
		t.Errorf("Engine = %q, want %q", results[0].Engine, EngineHybrid)
	}
}

func TestHybridSearchBothEngines(t *testing.T) {
	t.Parallel()
	vec, _ := vecstore.NewChromemStore(vecstore.ChromemOptions{Dimensions: 3})
	ctx := context.Background()
	if err := vec.Add(ctx, "v1", []float32{1, 0, 0}, map[string]string{
		"source_file": "vid.mp4",
		"start_time":  "5.000",
		"end_time":    "35.000",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	embedder := &mockEmbedder{
		dims:     3,
		queryVec: []float32{0.9, 0.1, 0.0},
	}

	descStore := &mockDescSearcher{
		results: []describe.SearchResult{
			{
				Segment: describe.Segment{
					SourceFile: "vid.mp4",
					StartTime:  5,
					EndTime:    35,
					Duration:   30,
				},
				Score:    0.85,
				Metadata: map[string]any{"visual": "action scene"},
			},
		},
	}

	s := &Searcher{
		Embedder:  embedder,
		VecStore:  vec,
		DescStore: descStore,
	}

	results, err := s.hybridSearch(ctx, "action", SearchOptions{
		MaxResults:   5,
		VectorWeight: 0.5,
		VLMWeight:    0.5,
	})
	if err != nil {
		t.Fatalf("hybridSearch both engines: %v", err)
	}
	// Same segment appears in both engines, so fusion merges into 1 result.
	if len(results) != 1 {
		t.Fatalf("expected 1 fused result, got %d", len(results))
	}
	if results[0].Engine != EngineHybrid {
		t.Errorf("Engine = %q, want %q", results[0].Engine, EngineHybrid)
	}
}

func TestHybridSearchDefaultWeights(t *testing.T) {
	t.Parallel()
	vec, _ := vecstore.NewChromemStore(vecstore.ChromemOptions{Dimensions: 3})
	ctx := context.Background()
	if err := vec.Add(ctx, "v1", []float32{1, 0, 0}, map[string]string{
		"source_file": "vid.mp4",
		"start_time":  "0.000",
		"end_time":    "30.000",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	embedder := &mockEmbedder{
		dims:     3,
		queryVec: []float32{0.9, 0.1, 0.0},
	}

	s := &Searcher{
		Embedder: embedder,
		VecStore: vec,
	}

	// VectorWeight=0 and VLMWeight=0 should default to 0.5 each.
	results, err := s.hybridSearch(ctx, "query", SearchOptions{
		MaxResults:   5,
		VectorWeight: 0,
		VLMWeight:    0,
	})
	if err != nil {
		t.Fatalf("hybridSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// Score should be positive with default weights.
	if results[0].Score <= 0 {
		t.Errorf("expected positive fused score with default weights, got %v", results[0].Score)
	}
}
