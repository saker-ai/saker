package api

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	coreevents "github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/logging"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

// Run executes the unified pipeline synchronously.
func (rt *Runtime) Run(ctx context.Context, req Request) (*Response, error) {
	if rt == nil {
		return nil, ErrRuntimeClosed
	}
	if err := rt.beginRun(); err != nil {
		return nil, err
	}
	defer rt.endRun()

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = defaultSessionID(rt.mode.EntryPoint)
	}
	req.SessionID = sessionID

	logger := logging.From(ctx)
	logger.Info("runtime.Run started", "session_id", sessionID, "prompt_len", len(req.Prompt))
	start := time.Now()

	if err := rt.sessionGate.Acquire(ctx, sessionID); err != nil {
		return nil, ErrConcurrentExecution
	}
	defer rt.sessionGate.Release(sessionID)

	if req.Pipeline != nil || strings.TrimSpace(req.ResumeFromCheckpoint) != "" {
		return rt.runPipeline(ctx, req)
	}

	prep, err := rt.prepare(ctx, req)
	if err != nil {
		logger.Error("runtime.Run prepare failed", "session_id", sessionID, "error", err)
		return nil, err
	}
	if !prep.normalized.Ephemeral {
		defer rt.persistHistory(prep.normalized.SessionID, prep.history)
	}
	result, err := rt.runAgent(prep)
	if err != nil {
		logger.Error("runtime.Run agent failed", "session_id", sessionID, "error", err, "duration_ms", time.Since(start).Milliseconds())
		return nil, err
	}
	logger.Info("runtime.Run completed", "session_id", sessionID, "duration_ms", time.Since(start).Milliseconds())
	return rt.buildResponse(prep, result), nil
}

// RunStream executes the pipeline asynchronously and returns events over a channel.
func (rt *Runtime) RunStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if rt == nil {
		return nil, ErrRuntimeClosed
	}
	if req.Pipeline == nil && strings.TrimSpace(req.ResumeFromCheckpoint) == "" && strings.TrimSpace(req.Prompt) == "" && len(req.ContentBlocks) == 0 {
		return nil, errors.New("api: prompt is empty")
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = defaultSessionID(rt.mode.EntryPoint)
	}
	req.SessionID = sessionID

	logger := logging.From(ctx)
	logger.Info("runtime.RunStream started", "session_id", sessionID, "prompt_len", len(req.Prompt))

	if err := rt.beginRun(); err != nil {
		return nil, err
	}

	if req.Pipeline != nil || strings.TrimSpace(req.ResumeFromCheckpoint) != "" {
		out := make(chan StreamEvent, 256)
		go func() {
			defer rt.endRun()
			defer close(out)
			if err := rt.sessionGate.Acquire(ctx, sessionID); err != nil {
				isErr := true
				out <- StreamEvent{Type: EventError, Output: ErrConcurrentExecution.Error(), IsError: &isErr}
				return
			}
			defer rt.sessionGate.Release(sessionID)

			out <- StreamEvent{Type: EventAgentStart, SessionID: sessionID}
			resp, err := rt.runPipeline(ctx, req)
			if err != nil {
				isErr := true
				out <- StreamEvent{Type: EventError, Output: err.Error(), IsError: &isErr, SessionID: sessionID}
				return
			}
			for _, entry := range resp.Timeline {
				entryCopy := entry
				out <- StreamEvent{Type: EventTimeline, Timeline: &entryCopy, SessionID: sessionID}
			}
			out <- StreamEvent{Type: EventAgentStop, SessionID: sessionID}
		}()
		return out, nil
	}

	// 缓冲区增大以吸收前端延迟（逐字符渲染等）导致的背压，避免 progress emit 阻塞工具执行
	out := make(chan StreamEvent, 512)
	progressChan := make(chan StreamEvent, 256)
	baseCtx := ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	progressMW := newProgressMiddleware(progressChan)
	ctxWithEmit := withStreamEmit(baseCtx, progressMW.streamEmit())
	go func() {
		defer rt.endRun()
		defer close(out)
		if err := rt.sessionGate.Acquire(ctxWithEmit, sessionID); err != nil {
			isErr := true
			out <- StreamEvent{Type: EventError, Output: ErrConcurrentExecution.Error(), IsError: &isErr}
			return
		}
		defer rt.sessionGate.Release(sessionID)

		prep, err := rt.prepare(ctxWithEmit, req)
		if err != nil {
			isErr := true
			out <- StreamEvent{Type: EventError, Output: err.Error(), IsError: &isErr}
			return
		}
		if !prep.normalized.Ephemeral {
			defer rt.persistHistory(prep.normalized.SessionID, prep.history)
		}

		// Emit skill activation events for explicitly matched skills only.
		// Skills with reason "always" (no matchers) or "mentioned" (name
		// appeared in prompt) are implicit and should not appear on the canvas.
		for _, sk := range prep.skillResults {
			if sk.MatchReason == "always" || sk.MatchReason == "mentioned" {
				continue
			}
			out <- StreamEvent{
				Type:      EventSkillActivation,
				Name:      sk.Definition.Name,
				SessionID: sessionID,
				Output: map[string]any{
					"description": sk.Definition.Description,
				},
			}
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			dropping := false
			for event := range progressChan {
				if dropping {
					continue
				}
				select {
				case out <- event:
				case <-ctxWithEmit.Done():
					dropping = true
				}
			}
		}()

		var runErr error
		var result runResult
		defer func() {
			if rt.hooks != nil {
				reason := "completed"
				if runErr != nil {
					reason = "error"
				}
				//nolint:errcheck // session end events are non-critical notifications
				rt.hooks.Publish(coreevents.Event{
					Type:      coreevents.SessionEnd,
					SessionID: req.SessionID,
					Payload:   coreevents.SessionEndPayload{SessionID: req.SessionID, Reason: reason},
				})
			}
		}()

		result, runErr = rt.runAgentWithMiddleware(prep, progressMW)
		close(progressChan)
		<-done

		if runErr != nil {
			isErr := true
			out <- StreamEvent{Type: EventError, Output: runErr.Error(), IsError: &isErr}
			return
		}
		rt.buildResponse(prep, result)
	}()
	return out, nil
}

// Close releases held resources.
func (rt *Runtime) Close() error {
	if rt == nil {
		return nil
	}
	rt.closeOnce.Do(func() {
		rt.runMu.Lock()
		rt.closed = true
		rt.runMu.Unlock()

		rt.runWG.Wait()

		var err error
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := toolbuiltin.DefaultAsyncTaskManager().Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			err = errors.Join(err, shutdownErr)
		}
		if shutdownErr == nil && rt.histories != nil {
			for _, sessionID := range rt.histories.SessionIDs() {
				if cleanupErr := cleanupBashOutputSessionDir(sessionID); cleanupErr != nil {
					slog.Error("api: session temp cleanup failed", "session_id", sessionID, "error", cleanupErr)
				}
				if cleanupErr := cleanupToolOutputSessionDir(sessionID); cleanupErr != nil {
					slog.Error("api: session tool output cleanup failed", "session_id", sessionID, "error", cleanupErr)
				}
			}
		}
		if rt.streamMonitor != nil {
			rt.streamMonitor.Close()
		}
		if rt.rulesLoader != nil {
			if e := rt.rulesLoader.Close(); e != nil {
				err = errors.Join(err, e)
			}
		}
		if rt.ownsTaskStore && rt.taskStore != nil {
			if e := rt.taskStore.Close(); e != nil {
				err = errors.Join(err, e)
			}
		}
		if rt.sessionDB != nil {
			if e := rt.sessionDB.Close(); e != nil {
				err = errors.Join(err, e)
			}
		}
		if rt.skillTracker != nil {
			if e := rt.skillTracker.Flush(); e != nil {
				err = errors.Join(err, e)
			}
		}
		if rt.registry != nil {
			rt.registry.Close()
		}
		if rt.tracer != nil {
			if e := rt.tracer.Shutdown(); e != nil {
				err = errors.Join(err, e)
			}
		}
		rt.closeErr = err
	})
	return rt.closeErr
}