package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/saker-ai/saker/pkg/media"
	"github.com/saker-ai/saker/pkg/media/clip"
	"github.com/saker-ai/saker/pkg/media/describe"
	"github.com/saker-ai/saker/pkg/media/embedding"
	"github.com/saker-ai/saker/pkg/media/vecstore"
	"github.com/saker-ai/saker/pkg/tool"
)

const mediaSearchDescription = `Search indexed video footage using natural language queries.

Uses a dual-engine approach:
- Vector engine: semantic similarity via Gemini embeddings (high recall)
- VLM engine: text matching against multi-track descriptions (high precision)
- Hybrid: combines both using Reciprocal Rank Fusion (default)

Optionally trims matching video segments to output files.
Requires media_index to have been run first.`

var mediaSearchSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "Natural language search query",
		},
		"max_results": map[string]any{
			"type":        "integer",
			"description": "Maximum number of results (default: 5)",
		},
		"engine": map[string]any{
			"type":        "string",
			"enum":        []string{"vector", "vlm", "hybrid"},
			"description": "Search engine to use (default: hybrid)",
		},
		"trim": map[string]any{
			"type":        "boolean",
			"description": "Extract matching video clips to files (default: false)",
		},
		"output_dir": map[string]any{
			"type":        "string",
			"description": "Directory for trimmed clips (default: ./media_results)",
		},
	},
	Required: []string{"query"},
}

// MediaSearchTool searches indexed video footage.
type MediaSearchTool struct {
	DataDir string // base directory for index data (default: .saker/media)
}

// NewMediaSearchTool creates a media search tool.
func NewMediaSearchTool(opts ...func(*MediaSearchTool)) *MediaSearchTool {
	t := &MediaSearchTool{}
	for _, o := range opts {
		o(t)
	}
	return t
}

func (t *MediaSearchTool) Name() string             { return "media_search" }
func (t *MediaSearchTool) Description() string      { return mediaSearchDescription }
func (t *MediaSearchTool) Schema() *tool.JSONSchema { return mediaSearchSchema }

func (t *MediaSearchTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}

	query, _ := params["query"].(string)
	if query == "" {
		return nil, errors.New("query is required")
	}

	maxResults := 5
	if n, ok := params["max_results"].(float64); ok && n > 0 {
		maxResults = int(n)
	}

	engine := media.EngineHybrid
	if e, ok := params["engine"].(string); ok && e != "" {
		engine = media.SearchEngine(e)
	}

	doTrim := false
	if v, ok := params["trim"].(bool); ok {
		doTrim = v
	}

	outputDir := "./media_results"
	if d, ok := params["output_dir"].(string); ok && d != "" {
		outputDir = d
	}

	dataDir := t.dataDir()

	// Setup embedder for vector search
	emb, err := embedding.NewEmbedder(embedding.Config{Backend: "gemini"})
	if err != nil && engine != media.EngineVLM {
		return nil, fmt.Errorf("create embedder: %w", err)
	}

	// Setup vector store
	vs, err := vecstore.NewChromemStore(vecstore.ChromemOptions{
		PersistDir:     filepath.Join(dataDir, "vectors"),
		CollectionName: "media_chunks",
		Compress:       true,
	})
	if err != nil {
		return nil, fmt.Errorf("open vector store: %w", err)
	}

	// Setup description store — scan all .jsonl files in dataDir
	descStore := describe.NewMultiStore(dataDir)

	searcher := &media.Searcher{
		Embedder:  emb,
		VecStore:  vs,
		DescStore: descStore,
	}

	opts := media.SearchOptions{
		MaxResults:   maxResults,
		Engine:       engine,
		VectorWeight: 0.5,
		VLMWeight:    0.5,
	}

	results, err := searcher.Search(ctx, query, opts)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	// Format output
	var output strings.Builder
	fmt.Fprintf(&output, "Found %d results for: %q\n\n", len(results), query)
	for i, r := range results {
		fmt.Fprintf(&output, "%d. %s [%.1fs - %.1fs] (score: %.4f, engine: %s)\n",
			i+1, filepath.Base(r.Segment.SourceFile),
			r.Segment.StartTime, r.Segment.EndTime,
			r.Score, r.Engine)
	}

	// Optionally trim clips
	var trimmedPaths []string
	if doTrim && len(results) > 0 {
		requests := make([]clip.TrimRequest, len(results))
		for i, r := range results {
			requests[i] = clip.TrimRequest{
				SourceFile: r.Segment.SourceFile,
				StartTime:  r.Segment.StartTime,
				EndTime:    r.Segment.EndTime,
			}
		}
		trimmedPaths, err = clip.TrimTopRequests(ctx, requests, outputDir, len(results))
		if err != nil {
			fmt.Fprintf(&output, "\nWarning: trim failed: %v\n", err)
		} else {
			fmt.Fprintf(&output, "\nTrimmed %d clips to %s:\n", len(trimmedPaths), outputDir)
			for _, p := range trimmedPaths {
				fmt.Fprintf(&output, "  - %s\n", p)
			}
		}
	}

	structured := map[string]any{
		"query":        query,
		"result_count": len(results),
		"engine":       string(engine),
	}
	if len(trimmedPaths) > 0 {
		structured["trimmed_clips"] = trimmedPaths
	}

	return &tool.ToolResult{
		Success:    true,
		Output:     output.String(),
		Structured: structured,
	}, nil
}

func (t *MediaSearchTool) dataDir() string {
	if t.DataDir != "" {
		return t.DataDir
	}
	return filepath.Join(".saker", "media")
}
