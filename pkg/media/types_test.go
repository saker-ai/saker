package media

import (
	"testing"
)

func TestDefaultSearchOptions(t *testing.T) {
	t.Parallel()
	opts := DefaultSearchOptions()
	if opts.MaxResults != 5 {
		t.Errorf("DefaultSearchOptions().MaxResults = %d, want 5", opts.MaxResults)
	}
	if opts.Engine != EngineHybrid {
		t.Errorf("DefaultSearchOptions().Engine = %q, want %q", opts.Engine, EngineHybrid)
	}
	if opts.VectorWeight != 0.5 {
		t.Errorf("DefaultSearchOptions().VectorWeight = %v, want 0.5", opts.VectorWeight)
	}
	if opts.VLMWeight != 0.5 {
		t.Errorf("DefaultSearchOptions().VLMWeight = %v, want 0.5", opts.VLMWeight)
	}
}

func TestSearchEngineConstants(t *testing.T) {
	t.Parallel()
	if EngineVector != "vector" {
		t.Errorf("EngineVector = %q, want %q", EngineVector, "vector")
	}
	if EngineVLM != "vlm" {
		t.Errorf("EngineVLM = %q, want %q", EngineVLM, "vlm")
	}
	if EngineHybrid != "hybrid" {
		t.Errorf("EngineHybrid = %q, want %q", EngineHybrid, "hybrid")
	}
}

func TestErrorSentinelValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want string
	}{
		{ErrNoFFmpeg, "media: ffmpeg not found in PATH"},
		{ErrChunkTooSmall, "media: chunk too small to embed"},
		{ErrIndexNotFound, "media: index not found"},
		{ErrNoResults, "media: no results found"},
		{ErrUnsupportedFormat, "media: unsupported video format"},
		{ErrAPIKeyMissing, "media: API key not configured"},
		{ErrQuotaExceeded, "media: API quota exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if tt.err.Error() != tt.want {
				t.Errorf("error message = %q, want %q", tt.err.Error(), tt.want)
			}
		})
	}
}

func TestMediaSegmentJSONTags(t *testing.T) {
	t.Parallel()
	seg := MediaSegment{
		SourceFile: "video.mp4",
		StartTime:  10.5,
		EndTime:    40.5,
		Duration:   30.0,
		ChunkPath:  "/tmp/chunk_0001.mp4",
	}
	if seg.SourceFile != "video.mp4" {
		t.Errorf("MediaSegment.SourceFile = %q, want %q", seg.SourceFile, "video.mp4")
	}
	if seg.Duration != seg.EndTime-seg.StartTime {
		t.Errorf("MediaSegment.Duration = %v, want %v (EndTime - StartTime)", seg.Duration, seg.EndTime-seg.StartTime)
	}
}

func TestSearchResultJSONTags(t *testing.T) {
	t.Parallel()
	sr := SearchResult{
		Segment:  MediaSegment{SourceFile: "test.mp4"},
		Score:    0.95,
		Engine:   EngineVector,
		Metadata: map[string]any{"vec_id": "abc123"},
	}
	if sr.Engine != EngineVector {
		t.Errorf("SearchResult.Engine = %q, want %q", sr.Engine, EngineVector)
	}
	if sr.Score != 0.95 {
		t.Errorf("SearchResult.Score = %v, want 0.95", sr.Score)
	}
}

func TestIndexStatsFields(t *testing.T) {
	t.Parallel()
	stats := IndexStats{
		TotalSegments: 10,
		TotalFiles:    2,
		BackendInfo:   "chromem-go",
		VecStoreSize:  10,
		DescStoreSize: 8,
	}
	if stats.TotalSegments != 10 {
		t.Errorf("IndexStats.TotalSegments = %d, want 10", stats.TotalSegments)
	}
	if stats.BackendInfo != "chromem-go" {
		t.Errorf("IndexStats.BackendInfo = %q, want %q", stats.BackendInfo, "chromem-go")
	}
}
