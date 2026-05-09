package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/cinience/saker/pkg/tool"
)

const streamCaptureDescription = `Captures frames from a live video stream using go2rtc.

Connects to the specified stream URL, captures a configurable number of frames
at a given interval, and returns them as image artifacts. Use this tool when
the user asks to analyze a live video stream or camera feed.

Requires the stream to be accessible from the host. Supported URL schemes:
- rtsp://[user:pass@]host[:port]/path
- rtmp://host[:port]/app/stream
- onvif://[user:pass@]host[:port][/path] (auto-discovers RTSP stream from IP camera)
- http(s)://host/path/playlist.m3u8 (HLS live stream)`

var streamCaptureSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"url": map[string]any{
			"type":        "string",
			"description": "RTSP or RTMP stream URL",
		},
		"count": map[string]any{
			"type":        "integer",
			"description": "Number of frames to capture (default: 5)",
		},
		"interval_ms": map[string]any{
			"type":        "integer",
			"description": "Interval between captures in milliseconds (default: 1000, max: 60000)",
			"minimum":     1,
			"maximum":     60000,
		},
		"enable_audio": map[string]any{
			"type":        "boolean",
			"description": "Also capture audio from the stream as a WAV artifact",
		},
	},
	Required: []string{"url"},
}

// StreamCaptureTool captures frames from a live RTSP/RTMP stream.
type StreamCaptureTool struct{}

// NewStreamCaptureTool creates a new stream capture tool.
func NewStreamCaptureTool() *StreamCaptureTool { return &StreamCaptureTool{} }

func (s *StreamCaptureTool) Name() string             { return "stream_capture" }
func (s *StreamCaptureTool) Description() string      { return streamCaptureDescription }
func (s *StreamCaptureTool) Schema() *tool.JSONSchema { return streamCaptureSchema }

func (s *StreamCaptureTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	url, _ := params["url"].(string)
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, fmt.Errorf("stream_capture: url is required")
	}
	if !pipeline.IsStreamScheme(url) {
		return nil, fmt.Errorf("stream_capture: unsupported URL scheme (expected rtsp://, rtmp://, onvif://, or HLS .m3u8): %s", url)
	}

	count := 5
	if v, ok := toInt(params["count"]); ok && v > 0 {
		count = v
	}
	if count > 100 {
		count = 100
	}

	intervalMs := 1000
	if v, ok := toInt(params["interval_ms"]); ok && v > 0 {
		intervalMs = v
	}
	if intervalMs > 60000 {
		intervalMs = 60000
	}

	// Capture timeout covers frame extraction only; connection has its own
	// timeout (ConnectTimeout). Total wall time ≤ ConnectTimeout + captureTimeout.
	connectTimeout := 15 * time.Second
	captureTimeout := time.Duration(count*intervalMs)*time.Millisecond + 15*time.Second
	captureCtx, cancel := context.WithTimeout(ctx, connectTimeout+captureTimeout)
	defer cancel()

	src := pipeline.NewGo2RTCStreamSource(url, pipeline.Go2RTCSourceOptions{
		SampleRate:     1,
		BufferSize:     count + 1,
		ConnectTimeout: connectTimeout,
		HTTPClient:     ssrfSafeClient,
	})
	defer src.Close()

	var frames []artifact.ArtifactRef
capture:
	for i := 0; i < count; i++ {
		ref, err := src.Next(captureCtx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if len(frames) > 0 {
				break // return what we have
			}
			return nil, fmt.Errorf("stream_capture: %w", err)
		}
		frames = append(frames, ref)

		// Wait interval between captures (except after last)
		if i < count-1 && intervalMs > 0 {
			select {
			case <-captureCtx.Done():
				break capture
			case <-time.After(time.Duration(intervalMs) * time.Millisecond):
			}
		}
	}

	// Capture audio if requested.
	enableAudio, _ := params["enable_audio"].(bool)
	if enableAudio && len(frames) > 0 {
		totalDuration := time.Duration(count*intervalMs)*time.Millisecond + time.Second
		extractor := pipeline.NewAudioExtractor(url, pipeline.AudioExtractorOptions{
			Interval:       totalDuration, // single chunk covering entire capture
			ConnectTimeout: connectTimeout,
			HTTPClient:     ssrfSafeClient,
		})
		if err := extractor.Start(captureCtx); err != nil {
			slog.Warn("stream_capture: audio extraction failed, returning video only",
				"url", url, "error", err)
		} else {
			defer extractor.Close()
			if chunk, err := extractor.Next(captureCtx); err == nil {
				frames = append(frames, artifact.ArtifactRef{
					Source:     artifact.ArtifactSourceGenerated,
					Path:       chunk.Path,
					Kind:       artifact.ArtifactKindAudio,
					ArtifactID: "stream_audio",
				})
			}
		}
	}

	if len(frames) == 0 {
		return &tool.ToolResult{
			Output: "No frames captured from stream.",
		}, nil
	}

	// Build output summary
	var paths []string
	audioCount := 0
	for _, f := range frames {
		paths = append(paths, f.Path)
		if f.Kind == artifact.ArtifactKindAudio {
			audioCount++
		}
	}
	output := fmt.Sprintf("Captured %d frames from %s", len(frames)-audioCount, url)
	if audioCount > 0 {
		output += fmt.Sprintf(" (+ %d audio clip)", audioCount)
	}
	output += ":\n" + strings.Join(paths, "\n")

	return &tool.ToolResult{
		Success:   true,
		Output:    output,
		Artifacts: frames,
	}, nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case int64:
		return int(n), true
	case json_number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}

type json_number interface {
	Int64() (int64, error)
}
