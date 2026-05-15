package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/conversation"
	"github.com/google/uuid"
)

const (
	convTeeWebOwnerUserID = "web"
	convTeeWebClient      = "web"
	// convTeeOpTimeout caps a single tee write. Generous enough to absorb
	// SQLite WAL contention spikes but tight enough to prevent a degraded
	// conversation store from stalling SessionStore mutations.
	convTeeOpTimeout = 5 * time.Second
)

// convTee mirrors SessionStore mutations into the unified conversation.Store.
// It is bound at SessionStore construction time to a single projectID — for
// the legacy single-project Server, "default"; for per-project registries,
// scope.ProjectID.
//
// All operations are nil-safe and best-effort: errors are logged and swallowed
// so a degraded conversation.Store never breaks the Web UI's SessionStore
// writes.
type convTee struct {
	store       *conversation.Store
	projectID   string
	ownerUserID string
	client      string
	logger      *slog.Logger
}

// newConvTee returns a tee bound to projectID, or nil when store is nil.
// SessionStore stores the *convTee directly; nil-receiver methods make every
// call site safe regardless of whether a tee was attached.
func newConvTee(store *conversation.Store, projectID string, logger *slog.Logger) *convTee {
	if store == nil {
		return nil
	}
	pid := strings.TrimSpace(projectID)
	if pid == "" {
		pid = "default"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &convTee{
		store:       store,
		projectID:   pid,
		ownerUserID: convTeeWebOwnerUserID,
		client:      convTeeWebClient,
		logger:      logger,
	}
}

// recordThreadCreate creates the matching thread row in conversation.Store.
// Reuses the SessionStore-generated UUID as the thread id so subsequent event
// appends locate the right row. A duplicate thread (e.g., same UUID seen
// twice) surfaces from the store as an error and is logged but swallowed —
// SessionStore can't easily roll back its own create on a tee failure.
func (t *convTee) recordThreadCreate(threadID, title string) {
	if t == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), convTeeOpTimeout)
	defer cancel()
	if _, err := t.store.CreateThreadWithID(ctx, threadID, t.projectID, t.ownerUserID, title, t.client); err != nil {
		t.logger.Warn("server: conv tee thread create failed",
			"thread_id", threadID, "project_id", t.projectID, "error", err)
	}
}

// recordThreadDelete soft-deletes the thread. The SessionStore equivalent is
// a hard delete; here we keep the events for forensic purposes.
func (t *convTee) recordThreadDelete(threadID string) {
	if t == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), convTeeOpTimeout)
	defer cancel()
	if err := t.store.SoftDeleteThread(ctx, threadID); err != nil && !errors.Is(err, conversation.ErrThreadNotFound) {
		t.logger.Warn("server: conv tee thread delete failed",
			"thread_id", threadID, "project_id", t.projectID, "error", err)
	}
}

// recordThreadTitleUpdate mirrors UpdateThreadTitle. Pure thread-row update;
// not projected as an event.
func (t *convTee) recordThreadTitleUpdate(threadID, title string) {
	if t == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), convTeeOpTimeout)
	defer cancel()
	if err := t.store.UpdateThreadTitle(ctx, threadID, title); err != nil && !errors.Is(err, conversation.ErrThreadNotFound) {
		t.logger.Warn("server: conv tee title update failed",
			"thread_id", threadID, "project_id", t.projectID, "error", err)
	}
}

// recordItem mirrors AppendItem / AppendItemWithArtifacts. The SessionStore
// item's turnID is reused as the conversation turn_id so items from one HTTP
// request share a turn — matching the api.Runtime path's "one persistHistory
// call = one turn" convention.
//
// SessionStore has no OpenTurn/CloseTurn lifecycle, so this code skips both:
// AppendEvent only requires the turn_id string. Items are appended one at a
// time rather than batched — chatty but correct.
func (t *convTee) recordItem(threadID, role, content, turnID string, artifacts []Artifact) {
	if t == nil {
		return
	}
	t.appendEvent(threadID, role, "", content, turnID, artifacts)
}

// recordToolItem mirrors AppendToolItem. SessionStore does not retain
// tool_call_id (the wire shape never reached this layer), so the resulting
// event is demoted to System per the same safety net used in
// pkg/api/conversation_persist.go. Tool name is preserved in ContentJSON so
// reconstructors can still attribute the result to a specific tool.
func (t *convTee) recordToolItem(threadID, toolName, content, turnID string, artifacts []Artifact) {
	if t == nil {
		return
	}
	t.appendEvent(threadID, "tool", toolName, content, turnID, artifacts)
}

func (t *convTee) appendEvent(threadID, role, toolName, content, turnID string, artifacts []Artifact) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	if strings.TrimSpace(turnID) == "" {
		// No turn id from caller (legacy paths). Synthesize one rather than
		// failing the append — every Append* item still lands.
		turnID = uuid.New().String()
	}

	kind, normalizedRole := classifyConvTeeEvent(role)
	payload := map[string]any{}
	if toolName != "" {
		payload["tool_name"] = toolName
	}
	if len(artifacts) > 0 {
		payload["artifacts"] = artifacts
	}
	var contentJSON any
	if len(payload) > 0 {
		contentJSON = payload
	}

	ctx, cancel := context.WithTimeout(context.Background(), convTeeOpTimeout)
	defer cancel()
	if _, err := t.store.AppendEvent(ctx, conversation.AppendEventInput{
		ThreadID:    threadID,
		ProjectID:   t.projectID,
		TurnID:      turnID,
		Kind:        kind,
		Role:        normalizedRole,
		ContentText: content,
		ContentJSON: contentJSON,
	}); err != nil {
		t.logger.Warn("server: conv tee append event failed",
			"thread_id", threadID, "project_id", t.projectID,
			"role", role, "error", err)
	}
}

// classifyConvTeeEvent maps SessionStore role values to conversation
// EventKind. Tool results are demoted to System because SessionStore loses
// the tool_call_id needed for EventKindToolResult — without it the
// projection layer rejects the insert. Mirrors the safety net in
// pkg/api/conversation_persist.go.
func classifyConvTeeEvent(role string) (conversation.EventKind, string) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return conversation.EventKindUserMessage, "user"
	case "assistant":
		return conversation.EventKindAssistantText, "assistant"
	case "tool", "function":
		return conversation.EventKindSystem, "tool"
	case "system", "developer":
		return conversation.EventKindSystem, "system"
	default:
		return conversation.EventKindSystem, role
	}
}
