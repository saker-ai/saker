package server

import (
	"context"
	"fmt"
	"time"

	coreevents "github.com/saker-ai/saker/pkg/core/events"
	toolbuiltin "github.com/saker-ai/saker/pkg/tool/builtin"
	"github.com/google/uuid"
)

func (h *Handler) handleThreadList(ctx context.Context, req Request) Response {
	threads := h.sessionsFor(ctx).ListThreads()
	return h.success(req.ID, map[string]any{"threads": threads})
}

func (h *Handler) handleThreadCreate(ctx context.Context, req Request) Response {
	title, _ := req.Params["title"].(string)
	if title == "" {
		title = "New Thread"
	}
	thread := h.sessionsFor(ctx).CreateThread(title)
	return h.success(req.ID, thread)
}

func (h *Handler) handleThreadUpdate(ctx context.Context, req Request) Response {
	threadID, _ := req.Params["threadId"].(string)
	if threadID == "" {
		return h.invalidParams(req.ID, "threadId is required")
	}
	title, _ := req.Params["title"].(string)
	if title == "" {
		return h.invalidParams(req.ID, "title is required")
	}
	store := h.sessionsFor(ctx)
	if !store.UpdateThreadTitle(threadID, title) {
		return h.invalidParams(req.ID, "thread not found")
	}
	thread, _ := store.GetThread(threadID)
	return h.success(req.ID, thread)
}

func (h *Handler) handleThreadDelete(ctx context.Context, req Request) Response {
	threadID, _ := req.Params["threadId"].(string)
	if threadID == "" {
		return h.invalidParams(req.ID, "threadId is required")
	}
	if !h.sessionsFor(ctx).DeleteThread(threadID) {
		return h.invalidParams(req.ID, "thread not found")
	}
	// Clean up subscribers for this thread.
	h.mu.Lock()
	delete(h.subscribers, threadID)
	h.mu.Unlock()
	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleThreadSubscribe(ctx context.Context, clientID string, req Request) Response {
	threadID, _ := req.Params["threadId"].(string)
	if threadID == "" {
		return h.invalidParams(req.ID, "threadId is required")
	}
	store := h.sessionsFor(ctx)
	if _, ok := store.GetThread(threadID); !ok {
		return h.invalidParams(req.ID, "thread not found")
	}

	h.mu.Lock()
	if h.subscribers[threadID] == nil {
		h.subscribers[threadID] = make(map[string]*wsClient)
	}
	if c, ok := h.clients[clientID]; ok {
		h.subscribers[threadID][clientID] = c
	}
	h.mu.Unlock()

	items := store.GetItems(threadID)

	// Include active turn info so reconnecting clients can resume streaming state.
	var activeTurns []ActiveTurn
	if h.tracker != nil {
		for _, t := range h.tracker.List() {
			if t.ThreadID == threadID {
				activeTurns = append(activeTurns, t)
			}
		}
	}

	// Migrate any remote artifact URLs to local cache in the background.
	go h.migrateRemoteArtifacts(store, threadID)

	return h.success(req.ID, map[string]any{"items": items, "activeTurns": activeTurns})
}

func (h *Handler) handleThreadUnsubscribe(clientID string, req Request) Response {
	threadID, _ := req.Params["threadId"].(string)
	if threadID == "" {
		return h.invalidParams(req.ID, "threadId is required")
	}
	h.mu.Lock()
	if subs, ok := h.subscribers[threadID]; ok {
		delete(subs, clientID)
	}
	h.mu.Unlock()
	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleThreadHistory(ctx context.Context, req Request) Response {
	threadID, _ := req.Params["threadId"].(string)
	if threadID == "" {
		return h.invalidParams(req.ID, "threadId is required")
	}
	items := h.sessionsFor(ctx).GetItems(threadID)
	return h.success(req.ID, map[string]any{"items": items})
}

func (h *Handler) handleApprovalRespond(req Request) Response {
	approvalID, _ := req.Params["approvalId"].(string)
	decision, _ := req.Params["decision"].(string)
	if approvalID == "" || decision == "" {
		return h.invalidParams(req.ID, "approvalId and decision are required")
	}

	var d coreevents.PermissionDecisionType
	switch decision {
	case "allow":
		d = coreevents.PermissionAllow
	case "deny":
		d = coreevents.PermissionDeny
	default:
		return h.invalidParams(req.ID, "decision must be 'allow' or 'deny'")
	}

	h.approvalMu.Lock()
	ch, ok := h.approvals[approvalID]
	if ok {
		delete(h.approvals, approvalID)
	}
	h.approvalMu.Unlock()

	if !ok {
		return h.invalidParams(req.ID, "approval not found or already resolved")
	}

	ch <- d
	return h.success(req.ID, map[string]any{"ok": true})
}

// MakeAskQuestionHandler returns a blocking AskQuestionFunc that pushes
// questions to the frontend via WebSocket and waits for user answers.
func (h *Handler) MakeAskQuestionHandler(threadID, turnID string) toolbuiltin.AskQuestionFunc {
	return func(ctx context.Context, questions []toolbuiltin.Question) (map[string]string, error) {
		questionID := uuid.New().String()
		resultCh := make(chan map[string]string, 1)

		h.questionMu.Lock()
		h.questions[questionID] = resultCh
		h.questionMu.Unlock()

		// Convert to server types for JSON serialization.
		items := make([]QuestionItem, len(questions))
		for i, q := range questions {
			opts := make([]QuestionOption, len(q.Options))
			for j, o := range q.Options {
				opts[j] = QuestionOption{Label: o.Label, Description: o.Description}
			}
			items[i] = QuestionItem{
				Question:    q.Question,
				Header:      q.Header,
				Options:     opts,
				MultiSelect: q.MultiSelect,
			}
		}

		qr := QuestionRequest{
			ID:        questionID,
			ThreadID:  threadID,
			TurnID:    turnID,
			Questions: items,
		}
		h.logger.Info("sending question/request", "question_id", questionID, "thread_id", threadID, "num_questions", len(items))
		h.notifySubscribers(threadID, "question/request", qr)

		select {
		case answers := <-resultCh:
			return answers, nil
		case <-time.After(approvalTimeout):
			h.questionMu.Lock()
			delete(h.questions, questionID)
			h.questionMu.Unlock()
			h.notifySubscribers(threadID, "question/timeout", map[string]any{
				"questionId": questionID,
				"turnId":     turnID,
			})
			return nil, fmt.Errorf("question timed out after %s", approvalTimeout)
		case <-ctx.Done():
			h.questionMu.Lock()
			delete(h.questions, questionID)
			h.questionMu.Unlock()
			return nil, ctx.Err()
		}
	}
}

func (h *Handler) handleQuestionRespond(req Request) Response {
	questionID, _ := req.Params["questionId"].(string)
	if questionID == "" {
		return h.invalidParams(req.ID, "questionId is required")
	}

	rawAnswers, _ := req.Params["answers"].(map[string]any)
	if len(rawAnswers) == 0 {
		return h.invalidParams(req.ID, "answers is required")
	}

	answers := make(map[string]string, len(rawAnswers))
	for k, v := range rawAnswers {
		if s, ok := v.(string); ok {
			answers[k] = s
		}
	}

	h.questionMu.Lock()
	ch, ok := h.questions[questionID]
	if ok {
		delete(h.questions, questionID)
	}
	h.questionMu.Unlock()

	if !ok {
		return h.invalidParams(req.ID, "question not found or already resolved")
	}

	ch <- answers
	return h.success(req.ID, map[string]any{"ok": true})
}
