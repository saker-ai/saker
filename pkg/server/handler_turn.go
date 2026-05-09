package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/api"
	coreevents "github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/logging"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/profile"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
	"github.com/google/uuid"
)

func (h *Handler) handleTurnSend(reqCtx context.Context, clientID string, req Request) Response {
	threadID, _ := req.Params["threadId"].(string)
	text, _ := req.Params["text"].(string)
	if threadID == "" || text == "" {
		return h.invalidParams(req.ID, "threadId and text are required")
	}

	// Parse optional attachments from the request.
	var contentBlocks []model.ContentBlock
	var userArtifacts []Artifact
	if rawAttach, ok := req.Params["attachments"]; ok {
		blocks, extraPrompt, artifacts := h.parseAttachments(rawAttach)
		contentBlocks = blocks
		userArtifacts = artifacts
		if extraPrompt != "" {
			text = extraPrompt + "\n\n" + text
		}
	}

	turnID := uuid.New().String()

	h.logger.Info("turn started", "turn_id", turnID, "thread_id", threadID, "client_id", clientID, "prompt_len", len(text), "attachments", len(contentBlocks))

	// Append user item (persisted immediately, but notify after response).
	var userItem ThreadItem
	store := h.sessionsFor(reqCtx)
	if len(userArtifacts) > 0 {
		userItem = store.AppendItemWithArtifacts(threadID, "user", text, turnID, userArtifacts)
	} else {
		userItem = store.AppendItem(threadID, "user", text, turnID)
	}

	// Extract authenticated user info for per-user isolation from the original
	// request context before creating the background turn context.
	username := UserFromContext(reqCtx)
	role := RoleFromContext(reqCtx)

	// Create cancellable context for this turn, carrying all request context
	// values (project Scope, logger, tracing) while removing cancellation
	// propagation so the turn survives WebSocket disconnections (e.g. user
	// switches threads or refreshes the page). context.WithoutCancel preserves
	// every value from reqCtx — Scope, logger, trace IDs — but detaches the
	// Done/cancel chain so only turn/cancel or the 45-minute timeout terminates it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), defaultTurnTimeout)
	h.cancelMu.Lock()
	h.cancels[turnID] = cancel
	h.turnThreads[turnID] = threadID
	h.cancelMu.Unlock()

	// Notify user item and start agent asynchronously, so the JSON-RPC
	// response reaches the client before the first notification.
	go func() {
		h.notifySubscribers(threadID, "thread/item", userItem)
		h.executeTurnWithBlocks(ctx, threadID, turnID, text, contentBlocks, username, role)
	}()

	return h.success(req.ID, map[string]any{"turnId": turnID})
}

// parseAttachments converts raw attachment params into ContentBlocks and Artifacts.
// Images and PDFs are read and base64-encoded for the LLM.
// Videos and audio files can't be sent directly — a prompt hint with the file path is returned instead.
// Artifacts are returned for persisting in the user's thread item.
func (h *Handler) parseAttachments(raw interface{}) (blocks []model.ContentBlock, extraPrompt string, artifacts []Artifact) {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil, "", nil
	}

	var fileHints []string
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		filePath, _ := m["path"].(string)
		name, _ := m["name"].(string)
		mediaType, _ := m["media_type"].(string)

		if filePath == "" || mediaType == "" {
			continue
		}

		// Resolve the disk path from the /api/files/ URL path.
		diskPath := strings.TrimPrefix(filePath, "/api/files/")
		diskPath = strings.TrimPrefix(diskPath, "/api/files")
		if diskPath == "" {
			continue
		}

		// Determine artifact type for the thread item.
		artType := "image"
		if strings.HasPrefix(mediaType, "video/") {
			artType = "video"
		} else if strings.HasPrefix(mediaType, "audio/") {
			artType = "audio"
		} else if mediaType == "application/pdf" {
			artType = "document"
		}
		artifacts = append(artifacts, Artifact{Type: artType, URL: filePath, Name: name})

		switch {
		case strings.HasPrefix(mediaType, "image/"):
			data, err := os.ReadFile(diskPath)
			if err != nil {
				h.logger.Warn("failed to read attachment", "path", diskPath, "error", err)
				continue
			}
			blocks = append(blocks, model.ContentBlock{
				Type:      model.ContentBlockImage,
				MediaType: mediaType,
				Data:      base64.StdEncoding.EncodeToString(data),
			})

		case mediaType == "application/pdf":
			data, err := os.ReadFile(diskPath)
			if err != nil {
				h.logger.Warn("failed to read attachment", "path", diskPath, "error", err)
				continue
			}
			blocks = append(blocks, model.ContentBlock{
				Type:      model.ContentBlockDocument,
				MediaType: mediaType,
				Data:      base64.StdEncoding.EncodeToString(data),
			})

		default:
			// Video/audio: can't send as content blocks, provide file path hint.
			label := name
			if label == "" {
				label = filepath.Base(diskPath)
			}
			fileHints = append(fileHints, fmt.Sprintf("[Attached file: %s — path: %s]", label, diskPath))
		}
	}

	if len(fileHints) > 0 {
		extraPrompt = strings.Join(fileHints, "\n")
	}
	return blocks, extraPrompt, artifacts
}

func (h *Handler) handleTurnCancel(req Request) Response {
	turnID, _ := req.Params["turnId"].(string)
	if turnID == "" {
		return h.invalidParams(req.ID, "turnId is required")
	}
	h.cancelMu.Lock()
	if cancel, ok := h.cancels[turnID]; ok {
		cancel()
		delete(h.cancels, turnID)
	}
	h.cancelMu.Unlock()
	return h.success(req.ID, map[string]any{"ok": true})
}

// handleThreadInterrupt cancels all active turns belonging to a specific thread.
// This enables thread-scoped interruption without affecting other concurrent sessions.
func (h *Handler) handleThreadInterrupt(req Request) Response {
	threadID, _ := req.Params["threadId"].(string)
	if threadID == "" {
		return h.invalidParams(req.ID, "threadId is required")
	}
	h.cancelMu.Lock()
	cancelled := 0
	for turnID, tid := range h.turnThreads {
		if tid == threadID {
			if cancel, ok := h.cancels[turnID]; ok {
				cancel()
				delete(h.cancels, turnID)
			}
			delete(h.turnThreads, turnID)
			cancelled++
		}
	}
	h.cancelMu.Unlock()
	h.logger.Info("thread interrupted", "thread_id", threadID, "cancelled_turns", cancelled)
	return h.success(req.ID, map[string]any{"ok": true, "cancelledTurns": cancelled})
}

// CancelAllTurns cancels all active turns. Called during graceful shutdown
// to ensure in-flight turns are terminated cleanly with proper notifications.
func (h *Handler) CancelAllTurns() {
	h.cancelMu.Lock()
	for turnID, cancel := range h.cancels {
		cancel()
		delete(h.cancels, turnID)
		delete(h.turnThreads, turnID)
	}
	h.cancelMu.Unlock()
}

// executeTurnWithBlocks runs the agent loop in a goroutine and streams events.
// contentBlocks may be nil for text-only turns.
func (h *Handler) executeTurnWithBlocks(ctx context.Context, threadID, turnID, prompt string, contentBlocks []model.ContentBlock, username, role string) {
	start := time.Now()
	logger := logging.From(ctx).With("turn_id", turnID, "thread_id", threadID)

	// Track active turn. Read from the per-project SessionStore so multi-tenant
	// turns see the correct thread title.
	store := h.sessionsFor(ctx)
	threadTitle := ""
	if t, ok := store.GetThread(threadID); ok {
		threadTitle = t.Title
	}
	if h.tracker != nil {
		h.tracker.Register(turnID, threadID, threadTitle, prompt, "user")
	}

	defer func() {
		if h.tracker != nil {
			h.tracker.Unregister(turnID)
		}
		h.cancelMu.Lock()
		delete(h.cancels, turnID)
		delete(h.turnThreads, turnID)
		h.cancelMu.Unlock()
		logger.Info("turn completed", "duration_ms", time.Since(start).Milliseconds())
	}()

	// Use threadID as sessionID so conversation history accumulates.
	// For non-admin users, prefix sessionID with username to isolate history.
	sessionID := threadID
	if role == "user" && username != "" {
		sessionID = "u_" + username + "_" + threadID
		// Ensure user's profile directory exists.
		_ = profile.EnsureExists(h.runtime.ProjectRoot(), username)
	}
	// Inject interactive question handler so AskUserQuestion blocks for user input.
	ctx = toolbuiltin.WithAskQuestionFunc(ctx, h.MakeAskQuestionHandler(threadID, turnID))
	// Inject the thread ID so canvas tools (canvas_get_node, canvas_list_nodes)
	// can default to the current thread without the LLM having to thread it.
	ctx = toolbuiltin.WithThreadID(ctx, threadID)

	// Prepend a <canvas_state> block ONLY when the prompt mentions canvas-related
	// concepts. For unrelated chats the agent can still call canvas_list_nodes
	// on demand — this keeps the per-turn token cost down.
	if promptMentionsCanvas(prompt) {
		if summary := h.loadCanvasSummary(ctx, threadID); summary != "" {
			prompt = "<canvas_state>\n" + summary + "\n</canvas_state>\n\n" + prompt
		}
	}

	logger.Info("executing agent run", "prompt_len", len(prompt), "content_blocks", len(contentBlocks), "user", username, "role", role)
	ch, err := h.runtime.RunStream(ctx, api.Request{
		Prompt:        prompt,
		ContentBlocks: contentBlocks,
		SessionID:     sessionID,
		User:          username,
		UserRole:      role,
	})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			logger.Warn("turn timed out", "timeout", defaultTurnTimeout)
			h.notifySubscribers(threadID, "turn/error", map[string]any{
				"turnId": turnID,
				"error":  "turn timed out after " + defaultTurnTimeout.String(),
			})
		} else {
			logger.Error("agent run failed", "error", err)
			h.notifySubscribers(threadID, "turn/error", map[string]any{
				"turnId": turnID,
				"error":  err.Error(),
			})
		}
		return
	}

	var buf strings.Builder
	toolCalls := map[string]string{}      // toolUseID → tool name
	seenArtifactURLs := map[string]bool{} // deduplicate artifacts within a turn
	eventCount := 0
	// Per-stream artifact filter: holds back partial / unclosed
	// function-call tags before they reach SSE subscribers, so the UI never
	// briefly flashes "⊐...inati" or its inner JSON. The persisted
	// assistant text still goes through stripFunctionCallArtifacts below as
	// a belt-and-braces guard.
	streamFilter := &streamArtifactFilter{}
	for evt := range ch {
		eventCount++
		// Update active turn tracker.
		if h.tracker != nil {
			h.tracker.UpdateFromEvent(turnID, evt)
		}
		// Accumulate the unfiltered text first — the post-stream rewrite
		// uses the canonical strip pass on the raw buffer.
		if evt.Delta != nil && evt.Delta.Text != "" {
			buf.WriteString(evt.Delta.Text)
			// Replace the forwarded delta text with the filtered version.
			// Shallow-clone the event so we don't mutate state shared with
			// the trackers / future iterations.
			filtered := streamFilter.Push(evt.Delta.Text)
			if filtered == "" {
				// Whole chunk is held back — skip forwarding so the
				// subscriber doesn't see a no-op text_delta.
				continue
			}
			fwd := evt
			deltaCopy := *evt.Delta
			deltaCopy.Text = filtered
			fwd.Delta = &deltaCopy
			h.notifySubscribers(threadID, "stream/event", fwd)
			continue
		}
		// Forward the raw StreamEvent to subscribers — zero conversion.
		h.notifySubscribers(threadID, "stream/event", evt)

		// Track tool calls and persist results as ThreadItems.
		switch evt.Type {
		case "tool_execution_start":
			toolCalls[evt.ToolUseID] = evt.Name
			logger.Debug("stream event", "type", evt.Type, "tool", evt.Name)

		case "tool_execution_result":
			toolName := toolCalls[evt.ToolUseID]
			if toolName == "" {
				toolName = evt.Name
			}
			content := formatToolResult(toolName, evt.Output)
			var itemArtifacts []Artifact
			if evt.IsError == nil || !*evt.IsError {
				for _, a := range extractArtifacts(toolName, evt.Output) {
					// Deduplicate artifacts by URL within the same turn.
					if !seenArtifactURLs[a.URL] {
						seenArtifactURLs[a.URL] = true
						itemArtifacts = append(itemArtifacts, a)
					}
				}
			}
			// Skip low-value tool outputs (e.g. "no matches") when no artifacts.
			if isLowValueToolOutput(content) && len(itemArtifacts) == 0 {
				continue
			}
			if content != "" || len(itemArtifacts) > 0 {
				// Cache remote media URLs synchronously before persisting so
				// the saved artifact already points at the local copy.
				// Async caching loses to signed-URL expiration (DashScope /
				// aigo URLs typically expire in 24h); if the goroutine
				// hasn't finished by reload time the persisted remote URL is
				// already dead. The download is fast (≤2s for images) and
				// the surrounding tool execution already cost 10–60s, so a
				// little extra latency here is the right trade for never
				// orphaning an artifact.
				for i, a := range itemArtifacts {
					if !strings.HasPrefix(a.URL, "http://") && !strings.HasPrefix(a.URL, "https://") {
						continue
					}
					cached := h.cacheArtifactMedia(a)
					if cached.URL != a.URL {
						itemArtifacts[i] = cached
						continue
					}
					// Cache failed (logged inside cacheArtifactMedia). Keep
					// the remote URL — migrateRemoteArtifacts on the next
					// thread/subscribe will retry, and the warn already
					// surfaced the failure to the operator.
				}
				item := store.AppendToolItem(threadID, toolName, content, turnID, itemArtifacts)
				h.notifySubscribers(threadID, "thread/item", item)
			}
		}
	}

	logger.Info("stream finished", "events", eventCount, "reply_len", buf.Len())

	// Persist the complete assistant reply, trimming streaming dot artifacts
	// and any leaked function-call syntax (Qwen-style XML, Claude-style invoke,
	// etc.) that occasionally bleeds into the text channel when a model gets
	// confused — see the eddaff17 thread incident for an example.
	if text := cleanAssistantReply(buf.String()); text != "" {
		assistantItem := store.AppendItem(threadID, "assistant", text, turnID)
		h.notifySubscribers(threadID, "thread/item", assistantItem)

		// Auto-generate thread title on the first assistant reply.
		h.maybeGenerateTitle(ctx, threadID, prompt, text)
	}
	h.notifySubscribers(threadID, "turn/finished", map[string]any{"turnId": turnID})
}

// maybeGenerateTitle kicks off async title generation when the thread still
// has the default "New Thread" title. Uses context.WithoutCancel so the
// background goroutine inherits trace/logging values but won't die when
// the parent request ends. A 30s ceiling prevents goroutine leaks.
func (h *Handler) maybeGenerateTitle(ctx context.Context, threadID, userMsg, assistantMsg string) {
	store := h.sessionsFor(ctx)
	t, ok := store.GetThread(threadID)
	if !ok || t.Title != "New Thread" {
		return
	}
	bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	go func() {
		defer cancel()
		title, err := generateThreadTitle(h.runtime, userMsg, assistantMsg)
		if bgCtx.Err() != nil || err != nil || title == "" {
			return
		}
		store.UpdateThreadTitle(threadID, title)
		if updated, ok := store.GetThread(threadID); ok {
			h.notifySubscribers(threadID, "thread/updated", updated)
		}
	}()
}

// MakePermissionHandler returns a PermissionRequestHandler that bridges
// approval requests through WebSocket to the connected frontend.
func (h *Handler) MakePermissionHandler(threadID, turnID string) api.PermissionRequestHandler {
	return func(ctx context.Context, req api.PermissionRequest) (coreevents.PermissionDecisionType, error) {
		approvalID := uuid.New().String()
		resultCh := make(chan coreevents.PermissionDecisionType, 1)

		h.approvalMu.Lock()
		h.approvals[approvalID] = resultCh
		h.approvalMu.Unlock()

		// Push approval request to frontend.
		h.notifySubscribers(threadID, "approval/request", ApprovalRequest{
			ID:         approvalID,
			ThreadID:   threadID,
			TurnID:     turnID,
			ToolName:   req.ToolName,
			ToolParams: req.ToolParams,
			Reason:     req.Reason,
		})

		// Wait for frontend response, approval timeout, or context cancellation.
		select {
		case d := <-resultCh:
			return d, nil
		case <-time.After(approvalTimeout):
			h.approvalMu.Lock()
			delete(h.approvals, approvalID)
			h.approvalMu.Unlock()
			h.notifySubscribers(threadID, "approval/timeout", map[string]any{
				"approvalId": approvalID,
				"turnId":     turnID,
			})
			return coreevents.PermissionDeny, fmt.Errorf("approval timed out after %s", approvalTimeout)
		case <-ctx.Done():
			h.approvalMu.Lock()
			delete(h.approvals, approvalID)
			h.approvalMu.Unlock()
			return coreevents.PermissionDeny, ctx.Err()
		}
	}
}