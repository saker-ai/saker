package toolbuiltin

import (
	"context"
	"errors"
	"strings"

	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/tool"
)

// analyze_video_tool.go owns the public tool surface (struct, schema,
// description, Execute entrypoint). Pipeline execution lives in
// analyze_video_pipeline.go and report/format/persistence in
// analyze_video_format.go.

// TranscribeFunc transcribes an audio file and returns the text.
// Mirrors pipeline.TranscribeFunc to avoid an import cycle.
type TranscribeFunc func(ctx context.Context, audioPath string) (string, error)

const analyzeVideoDescription = `Performs comprehensive deep analysis of a video file using chunked multi-track VLM annotation with optional audio transcription and vector embedding.

Produces structured annotations (visual/audio/text/entity/scene/action/search_tags) per segment,
a detailed markdown report, a searchable JSONL index, and optionally indexes segments into a vector
store for semantic search via media_search.

For quick single-frame analysis, use frame_analyzer instead.
For sampling frames only, use video_sampler instead.
Requires ffmpeg to be installed on the system.`

var analyzeVideoSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"video_path": map[string]any{
			"type":        "string",
			"description": "Path to the input video file",
		},
		"task": map[string]any{
			"type":        "string",
			"description": "What to analyze or answer about the video (default: comprehensive video summary)",
		},
		"concurrency": map[string]any{
			"type":        "integer",
			"description": "Number of parallel workers for segment analysis (default: 4 for streams, 8 for local files)",
		},
		"enable_embedding": map[string]any{
			"type":        "boolean",
			"description": "Enable vector embedding for semantic search via media_search (default: false, requires embedding API key)",
		},
	},
	Required: []string{"video_path"},
}

// AnalyzeVideoTool orchestrates comprehensive video analysis with multi-track annotation.
type AnalyzeVideoTool struct {
	Model      model.Model
	Transcribe TranscribeFunc // optional; nil = skip audio transcription
	StoreDir   string         // base directory for per-session JSONL and vector storage; empty = auto
}

// NewAnalyzeVideoTool creates an analyze_video tool with the given model.
// Callers should inject a TranscribeFunc via t.Transcribe for audio transcription.
// When constructed through builtinToolFactories, resolveTranscribeFunc handles this.
func NewAnalyzeVideoTool(m model.Model) *AnalyzeVideoTool {
	return &AnalyzeVideoTool{Model: m}
}

func (t *AnalyzeVideoTool) Name() string             { return "analyze_video" }
func (t *AnalyzeVideoTool) Description() string      { return analyzeVideoDescription }
func (t *AnalyzeVideoTool) Schema() *tool.JSONSchema { return analyzeVideoSchema }

func (t *AnalyzeVideoTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if t.Model == nil {
		return nil, errors.New("analyze_video: model not configured")
	}

	videoPath, _ := params["video_path"].(string)
	if videoPath == "" {
		return nil, errors.New("video_path is required")
	}

	task, _ := params["task"].(string)
	concurrency := parseConcurrency(params, videoPath)
	enableEmbedding, _ := params["enable_embedding"].(bool)

	return t.executeDeep(ctx, videoPath, task, concurrency, enableEmbedding)
}

// parseConcurrency extracts the concurrency param or picks a default.
// Local files default to 8 (more aggressive); streams/URLs default to 4.
func parseConcurrency(params map[string]any, videoPath string) int {
	if c, ok := params["concurrency"]; ok {
		switch v := c.(type) {
		case float64:
			if int(v) > 0 {
				return int(v)
			}
		case int:
			if v > 0 {
				return v
			}
		}
	}
	// Local files can be read in parallel more aggressively.
	if strings.HasPrefix(videoPath, "rtsp://") || strings.HasPrefix(videoPath, "rtmp://") || strings.HasPrefix(videoPath, "http") {
		return 4
	}
	return 8
}
