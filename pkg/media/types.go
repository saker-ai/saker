// Package media implements dual-engine media search combining vector embedding
// retrieval with VLM description-based ranking.
package media

// MediaSegment represents a chunk of a source video file.
type MediaSegment struct {
	SourceFile string  `json:"source_file"`
	StartTime  float64 `json:"start_time"`
	EndTime    float64 `json:"end_time"`
	Duration   float64 `json:"duration"`
	ChunkPath  string  `json:"chunk_path,omitempty"`
}

// SearchEngine identifies which search engine produced a result.
type SearchEngine string

const (
	EngineVector SearchEngine = "vector"
	EngineVLM    SearchEngine = "vlm"
	EngineHybrid SearchEngine = "hybrid"
)

// SearchResult represents a single search hit from either engine.
type SearchResult struct {
	Segment  MediaSegment   `json:"segment"`
	Score    float64        `json:"score"`
	Engine   SearchEngine   `json:"engine"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// IndexStats reports the state of the media index.
type IndexStats struct {
	TotalSegments int    `json:"total_segments"`
	TotalFiles    int    `json:"total_files"`
	BackendInfo   string `json:"backend_info"`
	VecStoreSize  int    `json:"vec_store_size"`
	DescStoreSize int    `json:"desc_store_size"`
}

// SearchOptions configures a search query.
type SearchOptions struct {
	MaxResults int          `json:"max_results"`
	Engine     SearchEngine `json:"engine"`
	// VectorWeight and VLMWeight control hybrid fusion (default 0.5/0.5).
	VectorWeight float64 `json:"vector_weight"`
	VLMWeight    float64 `json:"vlm_weight"`
}

// DefaultSearchOptions returns sensible defaults.
func DefaultSearchOptions() SearchOptions {
	return SearchOptions{
		MaxResults:   5,
		Engine:       EngineHybrid,
		VectorWeight: 0.5,
		VLMWeight:    0.5,
	}
}
