package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/saker-ai/saker/pkg/media"
	"github.com/saker-ai/saker/pkg/media/chunk"
	"github.com/saker-ai/saker/pkg/media/describe"
	"github.com/saker-ai/saker/pkg/media/embedding"
	"github.com/saker-ai/saker/pkg/media/vecstore"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/tool"
)

const mediaIndexDescription = `Index video files for semantic search.

Splits videos into overlapping chunks, embeds them using a vector embedding API,
and stores vectors in a local chromem-go database. Optionally generates VLM-based
multi-track descriptions for hybrid search.

Supports indexing a single file or an entire directory of videos (.mp4, .mov, .avi, .mkv, .webm).
Requires ffmpeg to be installed. If no backend is specified, auto-detects from available API keys
(DASHSCOPE_API_KEY, GEMINI_API_KEY, OPENAI_API_KEY, VOYAGE_API_KEY, JINA_API_KEY).`

var mediaIndexSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "Path to a video file or directory containing videos",
		},
		"backend": map[string]any{
			"type":        "string",
			"enum":        []string{"gemini", "openai", "voyage", "jina", "aliyun"},
			"description": "Embedding backend. If omitted, auto-detects from available API keys.",
		},
		"chunk_duration": map[string]any{
			"type":        "number",
			"description": "Duration of each chunk in seconds (default: 30)",
		},
		"overlap": map[string]any{
			"type":        "number",
			"description": "Overlap between chunks in seconds (default: 5)",
		},
		"enable_vlm": map[string]any{
			"type":        "boolean",
			"description": "Enable VLM description annotation (default: false, requires vision model)",
		},
	},
	Required: []string{"path"},
}

// MediaIndexTool indexes video files for semantic search.
type MediaIndexTool struct {
	Model   model.Model // vision model for VLM annotation (optional)
	DataDir string      // base directory for index data (default: .saker/media)
}

// NewMediaIndexTool creates a media index tool.
func NewMediaIndexTool(opts ...func(*MediaIndexTool)) *MediaIndexTool {
	t := &MediaIndexTool{}
	for _, o := range opts {
		o(t)
	}
	return t
}

func (t *MediaIndexTool) Name() string             { return "media_index" }
func (t *MediaIndexTool) Description() string      { return mediaIndexDescription }
func (t *MediaIndexTool) Schema() *tool.JSONSchema { return mediaIndexSchema }

func (t *MediaIndexTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}

	path, _ := params["path"].(string)
	if path == "" {
		return nil, errors.New("path is required")
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("path not found: %w", err)
	}

	backend := ""
	if b, ok := params["backend"].(string); ok && b != "" {
		backend = b
	}

	chunkOpts := chunk.DefaultOptions()
	if d, ok := params["chunk_duration"].(float64); ok && d > 0 {
		chunkOpts.Duration = d
	}
	if o, ok := params["overlap"].(float64); ok && o > 0 {
		chunkOpts.Overlap = o
	}

	enableVLM := false
	if v, ok := params["enable_vlm"].(bool); ok {
		enableVLM = v
	}

	// Setup embedder (auto-detect backend if not specified)
	emb, err := embedding.NewEmbedder(embedding.Config{Backend: backend})
	if err != nil {
		return nil, fmt.Errorf("create embedder: %w", err)
	}
	if backend == "" {
		backend = embedding.DetectBackend()
	}

	// Setup vector store
	dataDir := t.dataDir()
	vs, err := vecstore.NewChromemStore(vecstore.ChromemOptions{
		PersistDir:     filepath.Join(dataDir, "vectors"),
		CollectionName: "media_chunks",
		Compress:       true,
	})
	if err != nil {
		return nil, fmt.Errorf("create vector store: %w", err)
	}

	// Setup VLM annotator (optional)
	var annotator *describe.Annotator
	var descStore *describe.Store
	if enableVLM && t.Model != nil {
		annotator = describe.NewAnnotator(t.Model)
		descStore = describe.NewStore(filepath.Join(dataDir, "descriptions.jsonl"))
	}

	indexer := &media.Indexer{
		Chunker:     chunkOpts,
		Embedder:    emb,
		VecStore:    vs,
		Annotator:   annotator,
		DescStore:   descStore,
		WorkDir:     filepath.Join(dataDir, "work"),
		Concurrency: 4,
	}

	var output strings.Builder

	if info.IsDir() {
		stats, err := indexer.IndexDirectory(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("index directory: %w", err)
		}
		fmt.Fprintf(&output, "Indexed %d files (%d segments) from %s\n", stats.TotalFiles, stats.TotalSegments, path)
		fmt.Fprintf(&output, "Vector store: %d documents\n", stats.VecStoreSize)
		if stats.DescStoreSize > 0 {
			fmt.Fprintf(&output, "Description store: %d annotations\n", stats.DescStoreSize)
		}
	} else {
		if err := indexer.IndexFile(ctx, path); err != nil {
			return nil, fmt.Errorf("index file: %w", err)
		}
		stats := vs.Stats()
		fmt.Fprintf(&output, "Indexed %s (%d segments stored)\n", filepath.Base(path), stats.DocumentCount)
	}

	return &tool.ToolResult{
		Success: true,
		Output:  output.String(),
		Structured: map[string]any{
			"path":    path,
			"backend": backend,
		},
	}, nil
}

func (t *MediaIndexTool) dataDir() string {
	if t.DataDir != "" {
		return t.DataDir
	}
	return filepath.Join(".saker", "media")
}
