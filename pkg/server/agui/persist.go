package agui

import (
	"context"
	"strings"

	"github.com/saker-ai/saker/pkg/conversation"
)

const aguiClient = "agui"

func (g *Gateway) ensureThread(ctx context.Context, threadID string, identity Identity) {
	cs := g.deps.ConversationStore
	if cs == nil {
		return
	}
	projectID := identity.ProjectID
	if projectID == "" {
		projectID = "default"
	}
	owner := identity.UserID
	if owner == "" {
		owner = identity.Username
	}
	if owner == "" {
		owner = "anonymous"
	}
	_, err := cs.GetThread(ctx, threadID)
	if err == nil {
		return
	}
	if _, err := cs.CreateThreadWithID(ctx, threadID, projectID, owner, "", aguiClient); err != nil {
		g.deps.Logger.Warn("agui: failed to create thread", "thread_id", threadID, "error", err)
	}
}

func (g *Gateway) persistUserMessage(ctx context.Context, threadID, turnID, projectID, text string) {
	cs := g.deps.ConversationStore
	if cs == nil || strings.TrimSpace(text) == "" {
		return
	}
	if projectID == "" {
		projectID = "default"
	}
	if _, err := cs.AppendEvent(ctx, conversation.AppendEventInput{
		ThreadID:    threadID,
		ProjectID:   projectID,
		TurnID:      turnID,
		Kind:        conversation.EventKindUserMessage,
		ContentText: text,
	}); err != nil {
		g.deps.Logger.Warn("agui: failed to persist user message", "thread_id", threadID, "error", err)
	}
}

func (g *Gateway) persistAssistantMessage(ctx context.Context, threadID, turnID, projectID, text string) {
	cs := g.deps.ConversationStore
	if cs == nil || strings.TrimSpace(text) == "" {
		return
	}
	if projectID == "" {
		projectID = "default"
	}
	if _, err := cs.AppendEvent(ctx, conversation.AppendEventInput{
		ThreadID:    threadID,
		ProjectID:   projectID,
		TurnID:      turnID,
		Kind:        conversation.EventKindAssistantText,
		ContentText: text,
	}); err != nil {
		g.deps.Logger.Warn("agui: failed to persist assistant message", "thread_id", threadID, "error", err)
	}
}
