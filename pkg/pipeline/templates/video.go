// Package templates provides pre-built pipeline step constructors for common workflows.
package templates

import (
	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/pipeline"
)

// VideoUnderstandingOptions configures the video understanding pipeline.
type VideoUnderstandingOptions struct {
	// Strategy is the frame sampling strategy: "uniform", "keyframe", or "interval".
	Strategy string
	// FrameCount is the number of frames (uniform) or interval seconds (interval).
	FrameCount int
	// Concurrency controls parallel frame analysis (default: 4).
	Concurrency int
	// MaxDimension limits the longest edge of extracted frames in pixels.
	MaxDimension int
	// Task is the analysis prompt passed to frame_analyzer and video_summarizer.
	Task string
}

// VideoUnderstanding constructs a pipeline step that:
//  1. Extracts frames from a video file using video_sampler
//  2. Analyzes each frame in parallel using frame_analyzer (FanOut)
//  3. Aggregates results using FanIn
//  4. Summarizes all frame analyses into a video-level understanding
func VideoUnderstanding(videoPath string, opts VideoUnderstandingOptions) pipeline.Step {
	if opts.Strategy == "" {
		opts.Strategy = "uniform"
	}
	if opts.FrameCount <= 0 {
		opts.FrameCount = 8
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}

	samplerWith := map[string]any{
		"video_path": videoPath,
		"strategy":   opts.Strategy,
		"count":      opts.FrameCount,
	}
	if opts.MaxDimension > 0 {
		samplerWith["max_dimension"] = opts.MaxDimension
	}

	analyzerWith := map[string]any{}
	if opts.Task != "" {
		analyzerWith["task"] = opts.Task
	}

	summarizerWith := map[string]any{}
	if opts.Task != "" {
		summarizerWith["task"] = opts.Task
	}

	return pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{
					Name: "sample-frames",
					Tool: "video_sampler",
					Input: []artifact.ArtifactRef{
						artifact.NewLocalFileRef(videoPath, artifact.ArtifactKindVideo),
					},
					With: samplerWith,
				},
				{
					FanOut: &pipeline.FanOut{
						Collection:  "frames",
						Concurrency: opts.Concurrency,
						Step: pipeline.Step{
							Name: "analyze-frame",
							Tool: "frame_analyzer",
							With: analyzerWith,
						},
					},
				},
				{
					FanIn: &pipeline.FanIn{
						Strategy: "ordered",
						Into:     "frame_analyses",
					},
				},
				{
					Name: "summarize",
					Tool: "video_summarizer",
					With: summarizerWith,
				},
			},
		},
	}
}
