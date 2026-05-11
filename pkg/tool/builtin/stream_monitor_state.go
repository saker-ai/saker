package toolbuiltin

import (
	"context"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/cinience/saker/pkg/runtime/tasks"
	"github.com/cinience/saker/pkg/tool"
)

// stream_monitor_state.go contains the StreamMonitorTool struct, its
// constructor and configuration setters, the public Close/ListMonitors
// surface, and the data types that describe a running monitor (handle and
// stats). Action dispatch (start/stop/status) and the background runner
// goroutines live in stream_monitor_runner.go; webhook delivery and the
// SSRF-safe HTTP client live in stream_monitor_webhook.go.

const streamMonitorDescription = `Starts, stops, or checks a background video stream task.

Connects to a live stream (RTSP, RTMP, ONVIF, HLS) and continuously captures
frames, detecting events based on keyword matching. When a pipeline executor
is configured, frames are analyzed by an AI model and events are detected via
configurable rules. Detected events are logged to the associated task and
optionally pushed via webhook.

Actions:
- start: Begin a stream task (returns a task_id for tracking)
- stop:  Stop a running stream task by task_id
- status: Check stream task statistics by task_id`

var streamMonitorSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"action": map[string]any{
			"type":        "string",
			"description": "Action to perform: start, stop, or status",
			"enum":        []string{"start", "stop", "status"},
		},
		"url": map[string]any{
			"type":        "string",
			"description": "Stream URL (required for start). Supports rtsp://, rtmp://, onvif://, HLS .m3u8",
		},
		"events": map[string]any{
			"type":        "string",
			"description": "Comma-separated detection keywords (e.g. \"person,vehicle,fire\")",
		},
		"sample_rate": map[string]any{
			"type":        "integer",
			"description": "Process every Nth frame (default: 5, range: 1-100)",
			"minimum":     1,
			"maximum":     100,
		},
		"webhook_url": map[string]any{
			"type":        "string",
			"description": "Optional URL to POST event notifications to",
		},
		"task_id": map[string]any{
			"type":        "string",
			"description": "Task ID (required for stop/status)",
		},
		"subject": map[string]any{
			"type":        "string",
			"description": "Task subject for the stream task (default: \"Stream Task\")",
		},
		"enable_audio": map[string]any{
			"type":        "boolean",
			"description": "Enable audio recognition from stream (requires aigo ASR configured)",
		},
	},
	Required: []string{"action"},
}

// MonitorInfo holds the public status of a running monitor.
type MonitorInfo struct {
	TaskID      string `json:"task_id"`
	Subject     string `json:"subject"`
	StreamURL   string `json:"stream_url"`
	Running     bool   `json:"running"`
	Processed   int    `json:"processed"`
	Skipped     int    `json:"skipped"`
	Events      int    `json:"events"`
	AudioChunks int    `json:"audio_chunks,omitempty"`
	Uptime      string `json:"uptime"`
	LastError   string `json:"last_error,omitempty"`
}

// StreamMonitorTool manages background video stream monitors.
type StreamMonitorTool struct {
	taskStore tasks.Store

	mu          sync.Mutex
	eventBus    *events.Bus             // optional, for Notification events
	executor    *pipeline.Executor      // optional, for AI-driven frame analysis
	transcriber pipeline.TranscribeFunc // optional, for audio transcription
	monitors    map[string]*monitorHandle
}

// NewStreamMonitorTool creates a stream monitor tool backed by the given task store.
func NewStreamMonitorTool(taskStore tasks.Store) *StreamMonitorTool {
	return &StreamMonitorTool{
		taskStore: taskStore,
		monitors:  make(map[string]*monitorHandle),
	}
}

// SetEventBus sets the event bus for emitting Notification events.
// Must be called before starting any monitors.
func (s *StreamMonitorTool) SetEventBus(bus *events.Bus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventBus = bus
}

// SetExecutor sets the pipeline executor for AI-driven frame analysis.
// When set, monitored frames are analyzed by the configured model and
// event rules are matched against the analysis output.
// Must be called before starting any monitors.
func (s *StreamMonitorTool) SetExecutor(exec *pipeline.Executor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executor = exec
}

// SetTranscribeFunc sets the audio transcription function for ASR support.
// When set and enable_audio is true, audio from the stream is transcribed
// and injected into the frame analysis prompt.
// Must be called before starting any monitors.
func (s *StreamMonitorTool) SetTranscribeFunc(fn pipeline.TranscribeFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transcriber = fn
}

// Close stops all active monitors and waits for their goroutines to finish.
// Should be called during server shutdown.
func (s *StreamMonitorTool) Close() {
	s.mu.Lock()
	handles := make([]*monitorHandle, 0, len(s.monitors))
	for _, h := range s.monitors {
		handles = append(handles, h)
	}
	// Clear the map so cleanupOnExit won't try to update tasks again
	for id := range s.monitors {
		delete(s.monitors, id)
	}
	s.mu.Unlock()

	for _, h := range handles {
		h.cancel()
	}
	for _, h := range handles {
		<-h.done
	}

	// Update task status — cleanupOnExit skips updates for monitors removed
	// from the map, so Close() must finalize them.
	for _, h := range handles {
		statusCompleted := tasks.TaskCompleted
		duration := time.Since(h.startedAt).Round(time.Second).String()
		desc := streamShutdownDescription(duration)
		s.taskStore.Update(h.taskID, tasks.TaskUpdate{ //nolint:errcheck
			Status:      &statusCompleted,
			Description: &desc,
		})
	}

	streamLogShutdown(len(handles))
}

func (s *StreamMonitorTool) Name() string             { return "stream_monitor" }
func (s *StreamMonitorTool) Description() string      { return streamMonitorDescription }
func (s *StreamMonitorTool) Schema() *tool.JSONSchema { return streamMonitorSchema }

// ListMonitors returns the status of all active monitors.
func (s *StreamMonitorTool) ListMonitors() []MonitorInfo {
	s.mu.Lock()
	handles := make([]*monitorHandle, 0, len(s.monitors))
	for _, h := range s.monitors {
		handles = append(handles, h)
	}
	s.mu.Unlock()

	infos := make([]MonitorInfo, 0, len(handles))
	for _, h := range handles {
		h.mu.Lock()
		info := MonitorInfo{
			TaskID:      h.taskID,
			Subject:     h.subject,
			StreamURL:   h.streamURL,
			Processed:   h.stats.processed,
			Skipped:     h.stats.skipped,
			Events:      h.stats.events,
			AudioChunks: h.stats.audioChunks,
			LastError:   h.stats.lastError,
			Uptime:      time.Since(h.startedAt).Round(time.Second).String(),
		}
		h.mu.Unlock()

		select {
		case <-h.done:
			info.Running = false
		default:
			info.Running = true
		}
		infos = append(infos, info)
	}
	return infos
}

type monitorHandle struct {
	taskID      string
	subject     string
	streamURL   string
	enableAudio bool
	cancel      context.CancelFunc
	done        chan struct{}

	webhookURL string
	startedAt  time.Time

	mu    sync.Mutex
	stats monitorStats
}

type monitorStats struct {
	processed   int
	skipped     int
	events      int
	audioChunks int
	lastError   string
}
