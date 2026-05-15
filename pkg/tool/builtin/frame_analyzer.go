package toolbuiltin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/tool"
)

const frameAnalyzerDescription = `Analyzes a single image frame using a vision-capable LLM.

Reads the image from the artifact's local path, encodes it as base64, and sends it
to the model with a configurable analysis prompt. Returns a structured description
of the frame content including scene, objects, text, and other observations.

This tool requires a model.Model to be injected at construction time.`

var frameAnalyzerSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"task": map[string]any{
			"type":        "string",
			"description": "Analysis task description (default: describe the frame in detail)",
		},
		"frame_path": map[string]any{
			"type":        "string",
			"description": "Path to the image file to analyze (alternative to passing artifacts)",
		},
	},
}

// FrameAnalyzerTool sends a single image frame to a vision LLM for analysis.
type FrameAnalyzerTool struct {
	Model       model.Model
	DefaultTask string
}

// NewFrameAnalyzerTool creates a frame analyzer with the given model.
func NewFrameAnalyzerTool(m model.Model) *FrameAnalyzerTool {
	return &FrameAnalyzerTool{Model: m}
}

func (f *FrameAnalyzerTool) Name() string             { return "frame_analyzer" }
func (f *FrameAnalyzerTool) Description() string      { return frameAnalyzerDescription }
func (f *FrameAnalyzerTool) Schema() *tool.JSONSchema { return frameAnalyzerSchema }

func (f *FrameAnalyzerTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if f.Model == nil {
		return nil, errors.New("frame_analyzer: model not configured")
	}

	task := f.DefaultTask
	if t, ok := params["task"].(string); ok && t != "" {
		task = t
	}
	if task == "" {
		task = "Describe this video frame in detail. Include: scene description, visible objects, any text, people and their actions, lighting/mood, and any notable elements."
	}

	// Extract artifact ref from pipeline params
	refs, imagePath, err := extractFramePath(params)
	if err != nil {
		return nil, err
	}

	// Resolve HTTP URLs to local temp files.
	localPath, err := resolveMediaPath(ctx, imagePath)
	if err != nil {
		return nil, fmt.Errorf("resolve frame path: %w", err)
	}

	// Read image file
	data, err := os.ReadFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("read frame: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("frame file is empty")
	}

	// Detect media type
	mediaType := detectImageMediaType(imagePath, data)

	// Build multimodal request
	b64 := base64.StdEncoding.EncodeToString(data)
	req := model.Request{
		Messages: []model.Message{
			{
				Role: "user",
				ContentBlocks: []model.ContentBlock{
					{
						Type:      model.ContentBlockImage,
						MediaType: mediaType,
						Data:      b64,
					},
					{
						Type: model.ContentBlockText,
						Text: task,
					},
				},
			},
		},
		MaxTokens: 1024,
	}

	resp, err := f.Model.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("model analysis: %w", err)
	}

	analysis := resp.Message.TextContent()

	// Detect when model cannot actually process images (no vision support).
	if isInvalidFrameAnalysis(analysis) {
		return &tool.ToolResult{
			Output: "frame_analyzer: model cannot process image content (vision not supported)",
		}, nil
	}

	// Build artifact ID from source frame
	artifactID := "analysis"
	if len(refs) > 0 && refs[0].ArtifactID != "" {
		artifactID = "analysis_" + refs[0].ArtifactID
	}

	return &tool.ToolResult{
		Success: true,
		Output:  analysis,
		Artifacts: []artifact.ArtifactRef{
			{
				Source:     artifact.ArtifactSourceGenerated,
				ArtifactID: artifactID,
				Kind:       artifact.ArtifactKindJSON,
			},
		},
		Structured: map[string]any{
			"analysis":   analysis,
			"frame_path": imagePath,
		},
	}, nil
}

// extractFramePath gets the image file path from pipeline artifact params.
func extractFramePath(params map[string]any) ([]artifact.ArtifactRef, string, error) {
	// Pipeline executor passes artifacts as []artifact.ArtifactRef
	if raw, ok := params["artifacts"]; ok {
		switch refs := raw.(type) {
		case []artifact.ArtifactRef:
			if len(refs) > 0 && refs[0].Path != "" {
				return refs, refs[0].Path, nil
			}
		case []any:
			for _, item := range refs {
				if ref, ok := item.(artifact.ArtifactRef); ok && ref.Path != "" {
					return []artifact.ArtifactRef{ref}, ref.Path, nil
				}
				// JSON-unmarshalled map
				if m, ok := item.(map[string]any); ok {
					path, _ := m["path"].(string)
					if path != "" {
						ref := artifact.ArtifactRef{Path: path}
						if id, ok := m["artifact_id"].(string); ok {
							ref.ArtifactID = id
						}
						return []artifact.ArtifactRef{ref}, path, nil
					}
				}
			}
		}
	}

	// Fallback: direct path param
	if path, ok := params["frame_path"].(string); ok && path != "" {
		return nil, path, nil
	}

	return nil, "", errors.New("frame_analyzer: no image artifact or frame_path provided")
}

// isInvalidFrameAnalysis detects when a model returns template/placeholder text
// instead of actually analyzing the image, which indicates the model lacks vision.
func isInvalidFrameAnalysis(text string) bool {
	lower := strings.ToLower(text)
	// Patterns indicating the model cannot see the image.
	invalidPatterns := []string{
		"i can't view",
		"i cannot view",
		"can't directly view",
		"cannot directly view",
		"i can't see",
		"i cannot see",
		"provide a description",
		"provide detailed descriptions",
		"i'll need you to",
		"i'll guide you",
		"i need more information",
		"please provide",
		"since i can't",
		"since i cannot",
		"i don't have access to the actual",
		"i'm unable to view",
	}
	matches := 0
	for _, p := range invalidPatterns {
		if strings.Contains(lower, p) {
			matches++
		}
	}
	// Two or more pattern matches is a strong signal.
	if matches >= 2 {
		return true
	}
	// A single match combined with very long output (template text) is also invalid.
	if matches >= 1 && len(text) > 1500 {
		return true
	}
	return false
}

func detectImageMediaType(path string, data []byte) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	default:
		if len(data) > 0 {
			return http.DetectContentType(data)
		}
		return "image/jpeg"
	}
}
