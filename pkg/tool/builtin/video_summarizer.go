package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/tool"
)

const videoSummarizerDescription = `Aggregates per-frame analysis results into a coherent video-level understanding.

Receives the collected frame analyses (from a FanIn step or pipeline items) and
uses an LLM to synthesize them into a structured video summary including:
key events, timeline of events, entities, and overall narrative.

This tool requires a model.Model to be injected at construction time.`

var videoSummarizerSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"task": map[string]any{
			"type":        "string",
			"description": "Summarization task (default: summarize the video content)",
		},
	},
}

// VideoSummarizerTool aggregates frame analyses into a video-level summary.
type VideoSummarizerTool struct {
	Model       model.Model
	DefaultTask string
}

// NewVideoSummarizerTool creates a summarizer with the given model.
func NewVideoSummarizerTool(m model.Model) *VideoSummarizerTool {
	return &VideoSummarizerTool{Model: m}
}

func (v *VideoSummarizerTool) Name() string             { return "video_summarizer" }
func (v *VideoSummarizerTool) Description() string      { return videoSummarizerDescription }
func (v *VideoSummarizerTool) Schema() *tool.JSONSchema { return videoSummarizerSchema }

func (v *VideoSummarizerTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if v.Model == nil {
		return nil, errors.New("video_summarizer: model not configured")
	}

	task := v.DefaultTask
	if t, ok := params["task"].(string); ok && t != "" {
		task = t
	}
	if task == "" {
		task = "Based on the following per-frame analyses of a video, provide a comprehensive video understanding summary. Include: overall narrative, key events with approximate timestamps, notable entities/objects, and any important observations."
	}

	// Collect frame analyses from pipeline items or structured data
	analyses := collectFrameAnalyses(params)
	if len(analyses) == 0 {
		return nil, errors.New("video_summarizer: no frame analyses provided")
	}

	// Build prompt with all frame analyses
	var sb strings.Builder
	sb.WriteString(task)
	sb.WriteString("\n\nNote: Individual frame analyses may contain inconsistencies (different names, scores, etc.) due to visual ambiguity. Cross-reference and resolve conflicts when synthesizing the summary.\n\n")
	for i, a := range analyses {
		fmt.Fprintf(&sb, "=== Frame %d ===\n%s\n\n", i, a)
	}

	req := model.Request{
		Messages: []model.Message{
			{
				Role:    "user",
				Content: sb.String(),
			},
		},
		MaxTokens: 2048,
	}

	resp, err := v.Model.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("model summarization: %w", err)
	}

	summary := resp.Message.TextContent()

	return &tool.ToolResult{
		Success: true,
		Output:  summary,
		Artifacts: []artifact.ArtifactRef{
			{
				Source:     artifact.ArtifactSourceGenerated,
				ArtifactID: "video_summary",
				Kind:       artifact.ArtifactKindJSON,
			},
		},
		Structured: map[string]any{
			"summary":     summary,
			"frame_count": len(analyses),
		},
	}, nil
}

// collectFrameAnalyses extracts frame analysis strings from pipeline params.
func collectFrameAnalyses(params map[string]any) []string {
	var analyses []string

	// From FanIn structured data (frame_analyses collection)
	if raw, ok := params["artifacts"]; ok {
		switch refs := raw.(type) {
		case []artifact.ArtifactRef:
			for _, ref := range refs {
				// Prefer reading the artifact file content over using the ID
				if ref.Path != "" {
					if data, err := os.ReadFile(ref.Path); err == nil && len(data) > 0 {
						analyses = append(analyses, string(data))
						continue
					}
				}
				if ref.ArtifactID != "" {
					analyses = append(analyses, ref.ArtifactID)
				}
			}
		}
	}

	// From pipeline items (the output of each FanOut child)
	if items, ok := params["items"]; ok {
		switch v := items.(type) {
		case []string:
			analyses = append(analyses, v...)
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					analyses = append(analyses, s)
				}
			}
		}
	}

	// From structured FanIn result
	if structured, ok := params["structured"]; ok {
		if m, ok := structured.(map[string]any); ok {
			if fa, ok := m["frame_analyses"]; ok {
				switch v := fa.(type) {
				case []string:
					analyses = append(analyses, v...)
				case []any:
					for _, item := range v {
						if s, ok := item.(string); ok {
							analyses = append(analyses, s)
						}
					}
				}
			}
		}
	}

	return analyses
}
