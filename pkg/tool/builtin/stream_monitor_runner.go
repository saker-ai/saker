package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/cinience/saker/pkg/runtime/tasks"
	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
)

// stream_monitor_runner.go owns Execute and the start/stop/status action
// dispatch, plus the background goroutines that pull frames from a
// pipeline.Go2RTCStreamSource and dispatch events. Webhook delivery itself is
// in stream_monitor_webhook.go; struct definitions live in
// stream_monitor_state.go.

func (s *StreamMonitorTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	action, _ := params["action"].(string)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "start":
		return s.start(ctx, params)
	case "stop":
		return s.stop(params)
	case "status":
		return s.status(params)
	default:
		return nil, fmt.Errorf("stream_monitor: unknown action %q (expected start, stop, or status)", action)
	}
}

func (s *StreamMonitorTool) start(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	urlStr, _ := params["url"].(string)
	urlStr = strings.TrimSpace(urlStr)
	if urlStr == "" {
		return nil, fmt.Errorf("stream_monitor: url is required for start")
	}
	if !pipeline.IsStreamScheme(urlStr) {
		return nil, fmt.Errorf("stream_monitor: unsupported URL scheme (expected rtsp://, rtmp://, onvif://, or HLS .m3u8): %s", urlStr)
	}

	subject := "Stream Task"
	if v, _ := params["subject"].(string); strings.TrimSpace(v) != "" {
		subject = strings.TrimSpace(v)
	}

	sampleRate := 5
	if v, ok := toInt(params["sample_rate"]); ok && v > 0 {
		sampleRate = v
	}
	if sampleRate > 100 {
		sampleRate = 100
	}

	webhookURL, _ := params["webhook_url"].(string)
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL != "" {
		if !strings.HasPrefix(webhookURL, "http://") && !strings.HasPrefix(webhookURL, "https://") {
			return nil, fmt.Errorf("stream_monitor: webhook_url must start with http:// or https://")
		}
		parsed, err := url.Parse(webhookURL)
		if err != nil {
			return nil, fmt.Errorf("stream_monitor: invalid webhook_url: %w", err)
		}
		// Pre-flight SSRF check — same fail-closed behavior as webhook tool.
		// Per-event delivery does its own per-request pinning so a TOCTOU
		// here is harmless, but blocking up-front gives early operator
		// feedback when the URL is obviously misconfigured.
		if _, err := security.CheckSSRF(ctx, parsed.Hostname()); err != nil {
			return nil, fmt.Errorf("stream_monitor: webhook_url rejected: %w", err)
		}
	}

	var keywords []string
	if ev, _ := params["events"].(string); ev != "" {
		for _, kw := range strings.Split(ev, ",") {
			kw = strings.TrimSpace(kw)
			if kw != "" {
				keywords = append(keywords, kw)
			}
		}
	}

	// Create task
	task, err := s.taskStore.Create(subject, fmt.Sprintf("Stream %s | events: %s", urlStr, strings.Join(keywords, ",")), "Processing stream")
	if err != nil {
		return nil, fmt.Errorf("stream_monitor: create task: %w", err)
	}
	statusInProgress := tasks.TaskInProgress
	if _, err := s.taskStore.Update(task.ID, tasks.TaskUpdate{Status: &statusInProgress}); err != nil {
		return nil, fmt.Errorf("stream_monitor: update task status: %w", err)
	}

	// Build event rules from keywords
	var rules []pipeline.EventRule
	for _, kw := range keywords {
		rules = append(rules, pipeline.NewKeywordEventRule(kw+"_detected", kw, 30*time.Second))
	}

	// Create stream source
	src := pipeline.NewGo2RTCStreamSource(urlStr, pipeline.Go2RTCSourceOptions{
		SampleRate: 1,
		BufferSize: 32,
		HTTPClient: ssrfSafeClient,
	})

	enableAudio, _ := params["enable_audio"].(bool)

	monCtx, cancel := context.WithCancel(context.Background())
	handle := &monitorHandle{
		taskID:      task.ID,
		subject:     subject,
		streamURL:   urlStr,
		enableAudio: enableAudio,
		cancel:      cancel,
		done:        make(chan struct{}),
		webhookURL:  webhookURL,
		startedAt:   time.Now(),
	}

	s.mu.Lock()
	s.monitors[task.ID] = handle
	s.mu.Unlock()

	slog.Info("stream_monitor: started monitoring", "url", urlStr, "task_id", task.ID, "keywords", keywords, "sample_rate", sampleRate)

	// Start background monitoring goroutine
	go s.runMonitor(monCtx, handle, src, rules, sampleRate)

	output, _ := json.Marshal(map[string]any{
		"task_id": task.ID,
		"status":  "started",
		"url":     urlStr,
		"events":  keywords,
	})

	return &tool.ToolResult{
		Success: true,
		Output:  string(output),
	}, nil
}

func (s *StreamMonitorTool) stop(params map[string]any) (*tool.ToolResult, error) {
	taskID, _ := params["task_id"].(string)
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("stream_monitor: task_id is required for stop")
	}

	s.mu.Lock()
	handle, ok := s.monitors[taskID]
	if ok {
		delete(s.monitors, taskID)
	}
	s.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("stream_monitor: no active stream task for %s", taskID)
	}

	handle.cancel()
	<-handle.done // wait for goroutine to finish

	// Update task to completed
	statusCompleted := tasks.TaskCompleted
	duration := time.Since(handle.startedAt).Round(time.Second).String()
	desc := fmt.Sprintf("Stream task completed. Duration: %s", duration)
	s.taskStore.Update(taskID, tasks.TaskUpdate{ //nolint:errcheck
		Status:      &statusCompleted,
		Description: &desc,
	})

	handle.mu.Lock()
	stats := handle.stats
	handle.mu.Unlock()

	slog.Info("stream_monitor: stopped", "url", handle.streamURL, "task_id", taskID, "processed", stats.processed, "events", stats.events, "duration", duration)

	output, _ := json.Marshal(map[string]any{
		"task_id":   taskID,
		"status":    "stopped",
		"processed": stats.processed,
		"skipped":   stats.skipped,
		"events":    stats.events,
		"duration":  duration,
	})

	return &tool.ToolResult{
		Success: true,
		Output:  string(output),
	}, nil
}

func (s *StreamMonitorTool) status(params map[string]any) (*tool.ToolResult, error) {
	taskID, _ := params["task_id"].(string)
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("stream_monitor: task_id is required for status")
	}

	s.mu.Lock()
	handle, ok := s.monitors[taskID]
	s.mu.Unlock()

	if !ok {
		return &tool.ToolResult{
			Output: fmt.Sprintf("No active stream task for %s (may have been stopped)", taskID),
		}, nil
	}

	handle.mu.Lock()
	stats := handle.stats
	handle.mu.Unlock()

	running := true
	select {
	case <-handle.done:
		running = false
	default:
	}

	output, _ := json.Marshal(map[string]any{
		"task_id":   taskID,
		"running":   running,
		"processed": stats.processed,
		"skipped":   stats.skipped,
		"events":    stats.events,
		"uptime":    time.Since(handle.startedAt).Round(time.Second).String(),
		"stream":    handle.streamURL,
	})

	return &tool.ToolResult{
		Success: true,
		Output:  string(output),
	}, nil
}

// runMonitor is the background goroutine that processes frames from the stream.
// When an Executor is configured, frames are analyzed via FrameProcessor and
// event rules are matched against the AI output. Otherwise, frames are counted
// for statistics only.
func (s *StreamMonitorTool) runMonitor(ctx context.Context, handle *monitorHandle, src *pipeline.Go2RTCStreamSource, rules []pipeline.EventRule, sampleRate int) {
	defer func() {
		src.Close()
		close(handle.done)
		// Auto-cleanup: remove from monitors map and update task on abnormal exit
		s.cleanupOnExit(handle)
	}()

	// Snapshot executor and transcriber under lock to avoid data races.
	s.mu.Lock()
	exec := s.executor
	transcribeFn := s.transcriber
	s.mu.Unlock()

	if exec != nil {
		s.runWithFrameProcessor(ctx, handle, src, rules, sampleRate, exec, transcribeFn)
	} else {
		s.runBasicCapture(ctx, handle, src, sampleRate)
	}
}

// runWithFrameProcessor uses FrameProcessor for AI-driven event detection.
func (s *StreamMonitorTool) runWithFrameProcessor(ctx context.Context, handle *monitorHandle, src *pipeline.Go2RTCStreamSource, rules []pipeline.EventRule, sampleRate int, exec *pipeline.Executor, transcribeFn pipeline.TranscribeFunc) {
	fpConfig := pipeline.FrameProcessorConfig{
		Step: pipeline.Step{
			Tool: "frame_analyzer",
			With: map[string]any{"prompt": "Describe what you see in this frame. Focus on people, vehicles, and notable events."},
		},
		SampleRate:    sampleRate,
		ContextWindow: 3,
		EventRules:    rules,
		OnEvent: func(ev pipeline.Event) {
			s.dispatchEvent(handle, ev)
		},
		FrameInterval: time.Second,
	}

	// Start audio extraction and transcription if enabled.
	var audioExtractor *pipeline.AudioExtractor
	var audioTranscriber *pipeline.AudioTranscriber
	if handle.enableAudio && transcribeFn != nil {
		extractor := pipeline.NewAudioExtractor(handle.streamURL, pipeline.AudioExtractorOptions{
			Interval:   5 * time.Second,
			HTTPClient: ssrfSafeClient,
		})
		if err := extractor.Start(ctx); err != nil {
			slog.Warn("stream_monitor: audio extraction failed, continuing without audio",
				"url", handle.streamURL, "task_id", handle.taskID, "error", err)
		} else {
			audioExtractor = extractor
			// Wrap transcribeFn to count successful transcriptions.
			countedFn := func(ctx context.Context, audioPath string) (string, error) {
				text, err := transcribeFn(ctx, audioPath)
				if err == nil && text != "" {
					handle.mu.Lock()
					handle.stats.audioChunks++
					handle.mu.Unlock()
				}
				return text, err
			}
			transcriber := pipeline.NewAudioTranscriber(countedFn, extractor, 5)
			go transcriber.Run(ctx)
			audioTranscriber = transcriber

			// Inject audio context into frame analysis.
			fpConfig.AudioContext = transcriber.RecentTranscript

			slog.Info("stream_monitor: audio recognition enabled",
				"url", handle.streamURL, "task_id", handle.taskID)
		}
	}

	defer func() {
		if audioTranscriber != nil {
			audioTranscriber.Close()
		}
		if audioExtractor != nil {
			audioExtractor.Close()
		}
	}()

	fp := &pipeline.FrameProcessor{
		Executor: *exec,
		Config:   fpConfig,
	}

	results := fp.Run(ctx, src)
	for result := range results {
		if result.Skipped {
			handle.mu.Lock()
			handle.stats.skipped++
			handle.mu.Unlock()
			continue
		}
		handle.mu.Lock()
		handle.stats.processed++
		handle.mu.Unlock()
	}
}

// runBasicCapture captures frames without AI analysis (fallback when no Executor).
// The underlying Go2RTCStreamSource does not support reconnection (once the
// connection fails or the stream ends, Next() returns io.EOF permanently), so
// this loop exits on any terminal error rather than retrying a dead source.
func (s *StreamMonitorTool) runBasicCapture(ctx context.Context, handle *monitorHandle, src *pipeline.Go2RTCStreamSource, sampleRate int) {
	frameIdx := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ref, err := src.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// EOF means stream ended normally; any other error is a failure.
			// In both cases the source is terminal — no reconnection possible.
			if errors.Is(err, io.EOF) {
				slog.Info("stream_monitor: stream ended",
					"url", handle.streamURL, "task_id", handle.taskID)
			} else {
				handle.mu.Lock()
				handle.stats.lastError = err.Error()
				handle.mu.Unlock()
				slog.Error("stream_monitor: stream error",
					"url", handle.streamURL, "task_id", handle.taskID, "error", err)
			}
			return
		}

		frameIdx++

		// Apply sample rate — skip frames
		if frameIdx%sampleRate != 0 {
			handle.mu.Lock()
			handle.stats.skipped++
			handle.mu.Unlock()
			continue
		}

		handle.mu.Lock()
		handle.stats.processed++
		handle.mu.Unlock()

		_ = ref // Frame available for future use
	}
}

// cleanupOnExit removes the monitor from the active map and updates the task
// status if the goroutine exited without an explicit stop call.
func (s *StreamMonitorTool) cleanupOnExit(handle *monitorHandle) {
	s.mu.Lock()
	_, stillTracked := s.monitors[handle.taskID]
	if stillTracked {
		delete(s.monitors, handle.taskID)
	}
	s.mu.Unlock()

	// Only update task if we weren't already stopped (stop() removes from map first)
	if stillTracked {
		handle.mu.Lock()
		lastErr := handle.stats.lastError
		stats := handle.stats
		handle.mu.Unlock()

		duration := time.Since(handle.startedAt).Round(time.Second).String()
		var desc string
		var status tasks.TaskStatus
		if lastErr != "" {
			desc = fmt.Sprintf("Stream task error after %s: %s (processed=%d, events=%d)",
				duration, lastErr, stats.processed, stats.events)
			status = tasks.TaskCompleted
			slog.Error("stream_monitor: exited with error", "url", handle.streamURL, "task_id", handle.taskID, "error", lastErr)
		} else {
			desc = fmt.Sprintf("Stream task ended after %s (processed=%d, events=%d)",
				duration, stats.processed, stats.events)
			status = tasks.TaskCompleted
			slog.Info("stream_monitor: ended normally", "url", handle.streamURL, "task_id", handle.taskID)
		}
		s.taskStore.Update(handle.taskID, tasks.TaskUpdate{ //nolint:errcheck
			Status:      &status,
			Description: &desc,
		})
	}
}

// dispatchEvent sends an event to the task store, webhook, and event bus.
func (s *StreamMonitorTool) dispatchEvent(handle *monitorHandle, ev pipeline.Event) {
	handle.mu.Lock()
	handle.stats.events++
	handle.mu.Unlock()

	// Update task description with event
	now := time.Now().Format("15:04:05")
	eventLine := fmt.Sprintf("[%s] %s: %s (confidence: %.0f%%)", now, ev.Type, ev.Detail, ev.Confidence*100)

	slog.Info("stream_monitor: event detected", "url", handle.streamURL, "task_id", handle.taskID, "event", eventLine)

	// Cap description growth to prevent unbounded memory use (keep last 50 events).
	const maxEventLines = 50
	task, err := s.taskStore.Get(handle.taskID)
	if err == nil {
		lines := strings.Split(task.Description, "\n")
		lines = append(lines, eventLine)
		if len(lines) > maxEventLines {
			lines = lines[len(lines)-maxEventLines:]
		}
		desc := strings.Join(lines, "\n")
		s.taskStore.Update(handle.taskID, tasks.TaskUpdate{Description: &desc}) //nolint:errcheck
	}

	// Webhook POST
	if handle.webhookURL != "" {
		go s.sendWebhook(handle.webhookURL, handle.taskID, handle.streamURL, ev)
	}

	// Event bus notification — snapshot under lock to avoid race with SetEventBus.
	s.mu.Lock()
	bus := s.eventBus
	s.mu.Unlock()
	if bus != nil {
		bus.Publish(events.Event{ //nolint:errcheck
			Type: events.Notification,
			Payload: events.NotificationPayload{
				Title:            "Stream Event",
				Message:          eventLine,
				NotificationType: "stream_event",
				Meta: map[string]any{
					"task_id":    handle.taskID,
					"event_type": ev.Type,
					"frame":      ev.Frame,
					"stream_url": handle.streamURL,
				},
			},
		})
	}
}

// streamShutdownDescription centralises the description string used when
// Close() finalises a monitor task. Kept here so the small bit of formatting
// shared between Close and stop stays close to the runner code.
func streamShutdownDescription(duration string) string {
	return fmt.Sprintf("Stream task stopped by shutdown. Duration: %s", duration)
}

func streamLogShutdown(count int) {
	slog.Info("stream_monitor: all monitors stopped", "count", count)
}
