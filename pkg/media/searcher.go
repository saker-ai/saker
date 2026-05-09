package media

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"golang.org/x/sync/errgroup"

	"github.com/cinience/saker/pkg/media/describe"
	"github.com/cinience/saker/pkg/media/embedding"
	"github.com/cinience/saker/pkg/media/vecstore"
)

// Searcher performs dual-engine media search combining vector similarity
// and VLM description matching with Reciprocal Rank Fusion.
type Searcher struct {
	Embedder  embedding.Embedder
	VecStore  *vecstore.ChromemStore
	DescStore describe.Searcher
}

// Search executes a dual-engine search query.
func (s *Searcher) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if opts.MaxResults <= 0 {
		opts.MaxResults = 5
	}

	switch opts.Engine {
	case EngineVector:
		return s.vectorSearch(ctx, query, opts.MaxResults)
	case EngineVLM:
		return s.vlmSearch(query, opts.MaxResults)
	case EngineHybrid, "":
		return s.hybridSearch(ctx, query, opts)
	default:
		return nil, fmt.Errorf("unknown engine: %s", opts.Engine)
	}
}

func (s *Searcher) vectorSearch(ctx context.Context, query string, n int) ([]SearchResult, error) {
	if s.Embedder == nil || s.VecStore == nil {
		return nil, fmt.Errorf("vector engine not configured")
	}

	queryVec, err := s.Embedder.EmbedQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	hits, err := s.VecStore.Search(ctx, queryVec, n)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	results := make([]SearchResult, len(hits))
	for i, h := range hits {
		results[i] = SearchResult{
			Segment: hitToSegment(h),
			Score:   h.Score,
			Engine:  EngineVector,
			Metadata: map[string]any{
				"vec_id": h.ID,
			},
		}
	}
	return results, nil
}

func (s *Searcher) vlmSearch(query string, n int) ([]SearchResult, error) {
	if s.DescStore == nil {
		return nil, fmt.Errorf("VLM engine not configured")
	}
	descResults, err := s.DescStore.Search(query, n)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, len(descResults))
	for i, dr := range descResults {
		results[i] = SearchResult{
			Segment: MediaSegment{
				SourceFile: dr.Segment.SourceFile,
				StartTime:  dr.Segment.StartTime,
				EndTime:    dr.Segment.EndTime,
				Duration:   dr.Segment.Duration,
				ChunkPath:  dr.Segment.ChunkPath,
			},
			Score:    dr.Score,
			Engine:   EngineVLM,
			Metadata: dr.Metadata,
		}
	}
	return results, nil
}

// hybridSearch combines vector and VLM results using Reciprocal Rank Fusion.
func (s *Searcher) hybridSearch(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	// Fetch more candidates than needed for better fusion
	candidateN := opts.MaxResults * 3

	var vecResults, vlmResults []SearchResult
	var vecErr, vlmErr error

	// Run both engines concurrently.
	g, gctx := errgroup.WithContext(ctx)
	if s.Embedder != nil && s.VecStore != nil {
		g.Go(func() error {
			vecResults, vecErr = s.vectorSearch(gctx, query, candidateN)
			return nil // don't abort the other engine
		})
	}
	if s.DescStore != nil {
		g.Go(func() error {
			vlmResults, vlmErr = s.vlmSearch(query, candidateN)
			return nil
		})
	}
	_ = g.Wait()

	// If both failed, return error
	if vecErr != nil && vlmErr != nil {
		return nil, fmt.Errorf("both engines failed: vector=%v, vlm=%v", vecErr, vlmErr)
	}

	// If only one engine available, return its results
	if len(vecResults) == 0 && vecErr != nil {
		return vlmResults, nil
	}
	if len(vlmResults) == 0 && vlmErr != nil {
		return vecResults, nil
	}

	// RRF fusion
	const k = 60.0 // RRF constant

	vecWeight := opts.VectorWeight
	vlmWeight := opts.VLMWeight
	if vecWeight <= 0 {
		vecWeight = 0.5
	}
	if vlmWeight <= 0 {
		vlmWeight = 0.5
	}

	type fusedEntry struct {
		result SearchResult
		score  float64
	}

	// Key by segment identity (source_file + start_time)
	fused := make(map[string]*fusedEntry)

	for rank, r := range vecResults {
		key := segmentKey(r.Segment)
		if e, ok := fused[key]; ok {
			e.score += vecWeight / (k + float64(rank+1))
		} else {
			r.Engine = EngineHybrid
			fused[key] = &fusedEntry{result: r, score: vecWeight / (k + float64(rank+1))}
		}
	}

	for rank, r := range vlmResults {
		key := segmentKey(r.Segment)
		if e, ok := fused[key]; ok {
			e.score += vlmWeight / (k + float64(rank+1))
			// Merge metadata
			if e.result.Metadata == nil {
				e.result.Metadata = make(map[string]any)
			}
			for mk, mv := range r.Metadata {
				e.result.Metadata[mk] = mv
			}
		} else {
			r.Engine = EngineHybrid
			fused[key] = &fusedEntry{result: r, score: vlmWeight / (k + float64(rank+1))}
		}
	}

	results := make([]SearchResult, 0, len(fused))
	for _, e := range fused {
		e.result.Score = e.score
		results = append(results, e.result)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > opts.MaxResults {
		results = results[:opts.MaxResults]
	}

	return results, nil
}

func segmentKey(seg MediaSegment) string {
	return fmt.Sprintf("%s:%.3f", seg.SourceFile, seg.StartTime)
}

func hitToSegment(h vecstore.Hit) MediaSegment {
	seg := MediaSegment{}
	if h.Metadata != nil {
		seg.SourceFile = h.Metadata["source_file"]
		if v, err := strconv.ParseFloat(h.Metadata["start_time"], 64); err == nil {
			seg.StartTime = v
		}
		if v, err := strconv.ParseFloat(h.Metadata["end_time"], 64); err == nil {
			seg.EndTime = v
		}
		seg.Duration = seg.EndTime - seg.StartTime
	}
	return seg
}
