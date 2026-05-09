// Example 20: Realtime Video Event Detection
//
// Demonstrates frame-level realtime video processing with stateful context
// and event detection using FrameProcessor:
//
//	source → [sample every Nth frame] → [analyze with context window] → [detect events] → output
//
// Three modes:
//   - Stub mode (default): simulates 20 frames with scripted events
//   - Watch mode (--watch-dir): monitors a directory for new image files
//   - RTSP mode (--rtsp): connects to an RTSP/RTMP stream via go2rtc
//
// Usage:
//
//	# Stub mode
//	go run ./examples/20-realtime-video
//
//	# Watch directory mode
//	go run ./examples/20-realtime-video --watch-dir /tmp/camera-feed --timeout 60s
//
//	# RTSP stream mode
//	go run ./examples/20-realtime-video --rtsp rtsp://admin:pass@192.168.1.100:554/stream --timeout 60s
//
//	# With custom sample rate and events
//	go run ./examples/20-realtime-video --sample-rate 2 --events person,fire,vehicle
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/cinience/saker/pkg/tool"
)

func main() {
	watchDir := flag.String("watch-dir", "", "Directory to watch for new image files")
	rtspURL := flag.String("rtsp", "", "RTSP/RTMP stream URL (e.g. rtsp://host/stream)")
	timeout := flag.Duration("timeout", 10*time.Second, "Timeout for processing")
	sampleRate := flag.Int("sample-rate", 1, "Process every Nth frame")
	windowSize := flag.Int("window", 5, "Context window size (recent frames)")
	eventsFlag := flag.String("events", "person,fire,vehicle", "Comma-separated event keywords to detect")
	cooldownSec := flag.Int("cooldown", 3, "Event cooldown in seconds")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Build stream source
	var source pipeline.StreamSource
	switch {
	case *rtspURL != "":
		fmt.Printf("RTSP stream: %s (sample rate: 1/%d, timeout: %s)\n", *rtspURL, *sampleRate, *timeout)
		source = pipeline.NewGo2RTCStreamSource(*rtspURL, pipeline.Go2RTCSourceOptions{
			SampleRate: *sampleRate,
		})
	case *watchDir != "":
		if info, err := os.Stat(*watchDir); err != nil || !info.IsDir() {
			log.Fatalf("watch dir %q does not exist", *watchDir)
		}
		fmt.Printf("Watching: %s (sample rate: 1/%d, timeout: %s)\n", *watchDir, *sampleRate, *timeout)
		source = pipeline.NewDirectoryWatchSource(*watchDir, 200*time.Millisecond)
	default:
		fmt.Printf("Stub mode: simulating 20 frames (sample rate: 1/%d)\n", *sampleRate)
		refs := make([]artifact.ArtifactRef, 20)
		for i := range refs {
			refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("frame_%03d", i), artifact.ArtifactKindImage)
		}
		source = pipeline.NewSliceSource(refs)
	}
	defer source.Close()

	// Build event rules
	cooldown := time.Duration(*cooldownSec) * time.Second
	var rules []pipeline.EventRule
	for _, kw := range strings.Split(*eventsFlag, ",") {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		rules = append(rules, pipeline.NewKeywordEventRule(kw+"_detected", kw, cooldown))
	}

	// Build frame processor
	fp := &pipeline.FrameProcessor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, refs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				id := "unknown"
				if len(refs) > 0 {
					id = refs[0].ArtifactID
				}
				// Simulate analysis with scripted scenarios
				analysis := stubAnalyze(id)
				return &tool.ToolResult{Output: analysis}, nil
			},
		},
		Config: pipeline.FrameProcessorConfig{
			Step:          pipeline.Step{Name: "analyze-frame", Tool: "vision"},
			SampleRate:    *sampleRate,
			ContextWindow: *windowSize,
			EventRules:    rules,
			FrameInterval: 200 * time.Millisecond,
			OnEvent: func(ev pipeline.Event) {
				fmt.Printf("  ** EVENT [%s] at %s: %s (confidence: %.0f%%)\n",
					ev.Type, fmtDuration(ev.Timestamp), ev.Detail, ev.Confidence*100)
			},
		},
	}

	// Run
	fmt.Println()
	results := fp.Run(ctx, source)

	processed, skipped, events := 0, 0, 0
	for r := range results {
		if r.Skipped {
			skipped++
			continue
		}
		processed++
		events += len(r.Events)

		analysis := r.Analysis
		if len(analysis) > 70 {
			analysis = analysis[:70] + "..."
		}
		marker := " "
		if len(r.Events) > 0 {
			marker = "!"
		}
		fmt.Printf("%s [%s] frame %3d: %s\n", marker, fmtDuration(r.Timestamp), r.FrameIndex, analysis)
	}

	fmt.Printf("\nDone: %d processed, %d skipped, %d events detected\n", processed, skipped, events)
}

// stubAnalyze returns scripted analysis results for demo purposes.
func stubAnalyze(frameID string) string {
	var idx int
	fmt.Sscanf(frameID, "frame_%d", &idx)

	switch {
	case idx == 3:
		return "Person entering the monitored zone from the left side. Adult male, walking."
	case idx == 5:
		return "Empty corridor. No movement detected. Normal lighting."
	case idx == 8:
		return "Vehicle detected: white sedan parking in restricted area."
	case idx == 12:
		return "Fire detected: smoke visible near the east wall. Orange glow observed."
	case idx == 15:
		return "Multiple persons gathered near exit. Normal behavior."
	case idx == 18:
		return "Person running across the courtyard. Possible emergency evacuation."
	default:
		return "Normal scene. No significant activity detected."
	}
}

func fmtDuration(d time.Duration) string {
	ms := d.Milliseconds()
	s := ms / 1000
	return fmt.Sprintf("%02d:%02d.%d", s/60, s%60, (ms%1000)/100)
}
