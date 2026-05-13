package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/conversation"
)

// closeTurnTimeout caps the wall-clock budget for the post-stream
// finalize sequence (usage event + CloseTurn). Producer ctx may already
// be cancelled when a client disconnects, so the persister falls back to
// a fresh ctx to make sure usage/close still land. 5 s is generous for a
// local SQLite write and keeps a degraded DB from hanging the producer
// goroutine indefinitely.
const closeTurnTimeout = 5 * time.Second

// chatPersister writes /v1/chat/completions traffic into the
// conversation.Store. One persister per request, owned end-to-end by
// the request goroutine until handover to runChatProducer.
//
// Methods are safe to call on a nil receiver — every entry point
// short-circuits when persistence isn't configured. Callers therefore
// never need to nil-check before recording an event.
//
// The persister is single-writer by construction: handleChatCompletions
// owns it during input-recording, then transfers ownership to the
// producer goroutine. The producer is the sole event-source for the
// rest of the run, so no mutex is required.
type chatPersister struct {
	store     *conversation.Store
	threadID  string
	projectID string
	turnID    string
	logger    *slog.Logger
}

// openChatPersister resolves the conversation thread for this request
// (creating a new one or reusing the SDK-supplied session_id), opens a
// fresh turn, and returns the bound persister.
//
// Returns (nil, nil) when ConversationStore is unconfigured — the
// gateway runs unchanged without persistence in that case (back-compat
// for tests + opt-in rollout).
//
// Identity → tenant resolution rules:
//   - projectID = identity.ProjectID; falls back to "default" so the
//     dev-bypass localhost path still records something coherent.
//   - ownerUserID = identity.UserID, falling back to identity.Username,
//     then "anonymous" for full bypass requests.
//
// The "default"/"anonymous" fallbacks are explicitly NOT used in
// production: real deployments mount authMiddleware before this
// handler, which always populates a real ProjectID + UserID.
func (g *Gateway) openChatPersister(ctx context.Context, req ChatRequest, extra ExtraBody, identity Identity) (*chatPersister, error) {
	if g == nil || g.deps.ConversationStore == nil {
		return nil, nil
	}

	projectID := identity.ProjectID
	ownerUserID := identity.UserID
	if ownerUserID == "" {
		ownerUserID = identity.Username
	}
	if projectID == "" {
		projectID = "default"
	}
	if ownerUserID == "" {
		ownerUserID = "anonymous"
	}

	logger := g.deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	store := g.deps.ConversationStore
	title := chatTitleFromRequest(req)

	var threadID string
	if extra.SessionID != "" {
		// Reuse-or-create on the SDK-supplied id so the OpenAI client's
		// session_id remains the stable handle across requests. Refuse
		// cross-tenant access to prevent a leaked session id from being
		// used as a probe — return an error so the gateway returns 400
		// rather than silently rewriting the request.
		existing, err := store.GetThread(ctx, extra.SessionID)
		switch {
		case err == nil:
			if existing.ProjectID != projectID {
				return nil, fmt.Errorf("session_id %q belongs to a different project", extra.SessionID)
			}
			threadID = existing.ID
		case errors.Is(err, conversation.ErrThreadNotFound):
			th, cErr := store.CreateThreadWithID(ctx, extra.SessionID, projectID, ownerUserID, title, "openai")
			if cErr != nil {
				return nil, fmt.Errorf("create thread with id: %w", cErr)
			}
			threadID = th.ID
		default:
			return nil, fmt.Errorf("get thread: %w", err)
		}
	} else {
		th, err := store.CreateThread(ctx, projectID, ownerUserID, title, "openai")
		if err != nil {
			return nil, fmt.Errorf("create thread: %w", err)
		}
		threadID = th.ID
	}

	turnID, err := store.OpenTurn(ctx, threadID, "")
	if err != nil {
		return nil, fmt.Errorf("open turn: %w", err)
	}

	return &chatPersister{
		store:     store,
		threadID:  threadID,
		projectID: projectID,
		turnID:    turnID,
		logger:    logger,
	}, nil
}

// recordInputs writes one event per inbound chat message. Errors here
// are surfaced to the caller (handler logs + warns the client via
// header) since failing to record the prompt means the entire turn is
// orphaned downstream — better to bail out than to silently lose the
// user's question.
func (p *chatPersister) recordInputs(ctx context.Context, msgs []ChatMessage) error {
	if p == nil {
		return nil
	}
	for i, m := range msgs {
		if err := p.recordInputMessage(ctx, m); err != nil {
			return fmt.Errorf("recordInputs[%d]: %w", i, err)
		}
	}
	return nil
}

// recordInputMessage maps one OpenAI ChatMessage onto the matching
// EventKind. Unknown roles are rejected (rather than silently coerced
// to "user") so a client typo is loud at the boundary.
func (p *chatPersister) recordInputMessage(ctx context.Context, m ChatMessage) error {
	role := strings.ToLower(strings.TrimSpace(m.Role))
	var kind conversation.EventKind
	switch role {
	case "system", "developer":
		kind = conversation.EventKindSystem
	case "user":
		kind = conversation.EventKindUserMessage
	case "assistant":
		kind = conversation.EventKindAssistantText
	case "tool", "function":
		kind = conversation.EventKindToolResult
	default:
		return fmt.Errorf("unknown role %q", m.Role)
	}

	text, _ := extractMessageText(m.Content)

	in := conversation.AppendEventInput{
		ThreadID:    p.threadID,
		ProjectID:   p.projectID,
		TurnID:      p.turnID,
		Kind:        kind,
		Role:        role,
		ContentText: text,
	}
	if len(m.ToolCalls) > 0 {
		in.ContentJSON = m.ToolCalls
	}
	if role == "tool" || role == "function" {
		in.ToolCallID = m.ToolCallID
		in.ToolCallName = m.Name
	}

	if _, err := p.store.AppendEvent(ctx, in); err != nil {
		return err
	}
	return nil
}

// recordEvent persists a single saker StreamEvent to the conversation
// log. Called by the producer goroutine AFTER the chunk has been
// hubRun.Publish'd, so a slow DB never gates SSE delivery.
//
// Errors are logged and swallowed: dropping an event is bad, but
// breaking the chat for the user mid-stream is worse.
func (p *chatPersister) recordEvent(ctx context.Context, evt api.StreamEvent) {
	if p == nil {
		return
	}
	switch evt.Type {
	case api.EventContentBlockDelta:
		if evt.Delta == nil || evt.Delta.Text == "" {
			return
		}
		_, err := p.store.AppendEvent(ctx, conversation.AppendEventInput{
			ThreadID:    p.threadID,
			ProjectID:   p.projectID,
			TurnID:      p.turnID,
			Kind:        conversation.EventKindAssistantText,
			Role:        "assistant",
			ContentText: evt.Delta.Text,
		})
		if err != nil {
			p.logger.Warn("conversation persister: assistant_text append failed",
				"thread_id", p.threadID, "turn_id", p.turnID, "err", err)
		}
	case api.EventToolExecutionStart:
		args := stringifyOutput(evt.Input)
		payload := map[string]any{
			"id":   evt.ToolUseID,
			"name": evt.Name,
			"args": json.RawMessage(coerceJSONOrString(args)),
		}
		_, err := p.store.AppendEvent(ctx, conversation.AppendEventInput{
			ThreadID:     p.threadID,
			ProjectID:    p.projectID,
			TurnID:       p.turnID,
			Kind:         conversation.EventKindAssistantToolCall,
			Role:         "assistant",
			ContentJSON:  payload,
			ToolCallID:   evt.ToolUseID,
			ToolCallName: evt.Name,
		})
		if err != nil {
			p.logger.Warn("conversation persister: assistant_tool_call append failed",
				"thread_id", p.threadID, "turn_id", p.turnID,
				"tool_use_id", evt.ToolUseID, "err", err)
		}
	case api.EventError:
		msg := stringifyOutput(evt.Output)
		_, err := p.store.AppendEvent(ctx, conversation.AppendEventInput{
			ThreadID:    p.threadID,
			ProjectID:   p.projectID,
			TurnID:      p.turnID,
			Kind:        conversation.EventKindError,
			Role:        "system",
			ContentText: msg,
		})
		if err != nil {
			p.logger.Warn("conversation persister: error append failed",
				"thread_id", p.threadID, "turn_id", p.turnID, "err", err)
		}
	}
}

// close finalizes the turn: emits the usage event (when the runtime
// reported any token counts) and marks the turn terminal with the given
// status.
//
// Uses a fresh background context with closeTurnTimeout so the close
// path still runs after a client disconnect (which would have cancelled
// the producer's ctx). The 5 s budget keeps a degraded DB from hanging
// the producer goroutine forever.
func (p *chatPersister) close(usage *ChatUsage, status conversation.TurnStatus) {
	if p == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeTurnTimeout)
	defer cancel()

	if usage != nil {
		payload := map[string]any{
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
		}
		if _, err := p.store.AppendEvent(ctx, conversation.AppendEventInput{
			ThreadID:    p.threadID,
			ProjectID:   p.projectID,
			TurnID:      p.turnID,
			Kind:        conversation.EventKindUsage,
			Role:        "system",
			ContentJSON: payload,
		}); err != nil {
			p.logger.Warn("conversation persister: usage append failed",
				"thread_id", p.threadID, "turn_id", p.turnID, "err", err)
		}
	}
	if err := p.store.CloseTurn(ctx, p.turnID, status); err != nil {
		p.logger.Warn("conversation persister: close turn failed",
			"thread_id", p.threadID, "turn_id", p.turnID, "err", err)
	}
}

// chatTitleFromRequest derives a thread title from the first user
// message. Falls back to a generic label so threads always have a
// readable name in list views even when the prompt is image-only.
//
// 80-char truncation matches what most chat UIs render in their thread
// list; the full text remains in the events table for search.
func chatTitleFromRequest(req ChatRequest) string {
	for _, m := range req.Messages {
		if !strings.EqualFold(m.Role, "user") {
			continue
		}
		text, _ := extractMessageText(m.Content)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if len(text) > 80 {
			text = text[:80]
		}
		return text
	}
	return "OpenAI chat"
}

// coerceJSONOrString returns valid JSON bytes for s. Keeps tool-call
// arguments structured when the runtime emitted a JSON object, but
// still surfaces freeform strings as valid JSON so the consumer can
// json.Unmarshal without branching.
func coerceJSONOrString(s string) []byte {
	s = strings.TrimSpace(s)
	if s == "" {
		return []byte("null")
	}
	var probe any
	if err := json.Unmarshal([]byte(s), &probe); err == nil {
		return []byte(s)
	}
	quoted, _ := json.Marshal(s)
	return quoted
}
