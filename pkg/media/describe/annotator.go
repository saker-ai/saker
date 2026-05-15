// Package describe provides VLM-based multi-track annotation of video segments.
package describe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/saker-ai/saker/pkg/model"
)

// Segment identifies a video segment for annotation (avoids import cycle with media).
type Segment struct {
	SourceFile string  `json:"source_file"`
	StartTime  float64 `json:"start_time"`
	EndTime    float64 `json:"end_time"`
	Duration   float64 `json:"duration"`
	ChunkPath  string  `json:"chunk_path,omitempty"`
}

// Annotation holds multi-track VLM descriptions for a video segment.
type Annotation struct {
	Segment    Segment  `json:"segment"`
	Visual     string   `json:"visual"`
	Audio      string   `json:"audio"`
	Text       string   `json:"text"`
	Entity     string   `json:"entity"`
	Scene      string   `json:"scene"`
	Action     string   `json:"action"`
	SearchTags []string `json:"search_tags"`
}

const annotationPrompt = `Analyze this video frame and provide a structured JSON response with the following fields:
{
  "visual": "Describe what you see: colors, objects, layout, lighting",
  "audio": "Infer likely audio based on visual cues (or 'unknown' if unclear)",
  "text": "Any visible text, signs, labels, or captions",
  "entity": "Identify specific people, vehicles, brands, or named objects",
  "scene": "Classify the scene type: indoor/outdoor, urban/rural, time of day",
  "action": "Describe actions and movements happening in the scene",
  "search_tags": ["tag1", "tag2", "tag3", "...up to 10 bilingual search keywords"]
}

Be concise but specific. Use both English and Chinese for search_tags when applicable.
Return ONLY the JSON object, no markdown fencing.`

// Annotator uses a vision LLM to generate multi-track annotations.
type Annotator struct {
	Model model.Model
}

// NewAnnotator creates an annotator with the given vision model.
func NewAnnotator(m model.Model) *Annotator {
	return &Annotator{Model: m}
}

// AnnotateSegment generates a multi-track annotation for a video segment
// using representative frames.
func (a *Annotator) AnnotateSegment(ctx context.Context, segment Segment, framePaths []string) (*Annotation, error) {
	if a.Model == nil {
		return nil, fmt.Errorf("describe: model not configured")
	}
	if len(framePaths) == 0 {
		return nil, fmt.Errorf("describe: no frames provided")
	}

	// Build multimodal content blocks: images + prompt
	var blocks []model.ContentBlock
	for _, path := range framePaths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if len(data) == 0 {
			continue
		}
		blocks = append(blocks, model.ContentBlock{
			Type:      model.ContentBlockImage,
			MediaType: "image/jpeg",
			Data:      base64.StdEncoding.EncodeToString(data),
		})
	}

	if len(blocks) == 0 {
		return nil, fmt.Errorf("describe: no valid frames to analyze")
	}
	if skipped := len(framePaths) - len(blocks); skipped > 0 {
		slog.Warn("describe: some frames skipped", "total", len(framePaths), "loaded", len(blocks), "skipped", skipped)
	}

	blocks = append(blocks, model.ContentBlock{
		Type: model.ContentBlockText,
		Text: annotationPrompt,
	})

	req := model.Request{
		Messages: []model.Message{
			{
				Role:          "user",
				ContentBlocks: blocks,
			},
		},
		MaxTokens: 1024,
	}

	resp, err := a.Model.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("model annotation: %w", err)
	}

	text := stripMarkdownFence(resp.Message.TextContent())

	ann := &Annotation{Segment: segment}
	if err := json.Unmarshal([]byte(text), ann); err != nil {
		// If JSON parsing fails, store as raw visual description
		ann.Visual = text
		ann.SearchTags = []string{}
	}
	ann.Segment = segment

	return ann, nil
}

// stripMarkdownFence removes markdown code fencing (```json ... ```) that VLMs
// sometimes wrap around their JSON output despite being told not to.
func stripMarkdownFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Strip opening fence line (```json, ```JSON, or just ```)
	if idx := strings.Index(s, "\n"); idx >= 0 {
		s = s[idx+1:]
	} else {
		return s // single-line fence with no content
	}
	// Strip trailing ```
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "```") {
		s = s[:len(s)-3]
	}
	return strings.TrimSpace(s)
}
