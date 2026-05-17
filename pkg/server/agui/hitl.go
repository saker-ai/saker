package agui

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/saker-ai/saker/pkg/api"
	coreevents "github.com/saker-ai/saker/pkg/core/events"
	toolbuiltin "github.com/saker-ai/saker/pkg/tool/builtin"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
)

const hitlTimeout = 5 * time.Minute

type sideEvent struct {
	event aguievents.Event
}

type pendingApproval struct {
	ApprovalID string
	ResultCh   chan coreevents.PermissionDecisionType
}

type pendingQuestion struct {
	QuestionID string
	ResultCh   chan map[string]string
}

type hitlRegistry struct {
	mu        sync.Mutex
	approvals map[string]*pendingApproval // keyed by runID
	questions map[string]*pendingQuestion // keyed by runID
}

func newHITLRegistry() *hitlRegistry {
	return &hitlRegistry{
		approvals: make(map[string]*pendingApproval),
		questions: make(map[string]*pendingQuestion),
	}
}

func (r *hitlRegistry) registerApproval(runID string, pa *pendingApproval) {
	r.mu.Lock()
	r.approvals[runID] = pa
	r.mu.Unlock()
}

func (r *hitlRegistry) lookupApproval(runID string) *pendingApproval {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.approvals[runID]
}

func (r *hitlRegistry) removeApproval(runID string) {
	r.mu.Lock()
	delete(r.approvals, runID)
	r.mu.Unlock()
}

func (r *hitlRegistry) registerQuestion(runID string, pq *pendingQuestion) {
	r.mu.Lock()
	r.questions[runID] = pq
	r.mu.Unlock()
}

func (r *hitlRegistry) lookupQuestion(runID string) *pendingQuestion {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.questions[runID]
}

func (r *hitlRegistry) removeQuestion(runID string) {
	r.mu.Lock()
	delete(r.questions, runID)
	r.mu.Unlock()
}

func (g *Gateway) makeAskQuestionHandler(runID string, sideCh chan<- sideEvent) toolbuiltin.AskQuestionFunc {
	return func(ctx context.Context, questions []toolbuiltin.Question) (map[string]string, error) {
		questionID := uuid.New().String()
		resultCh := make(chan map[string]string, 1)

		g.hitl.registerQuestion(runID, &pendingQuestion{
			QuestionID: questionID,
			ResultCh:   resultCh,
		})

		items := make([]map[string]any, len(questions))
		for i, q := range questions {
			opts := make([]map[string]string, len(q.Options))
			for j, o := range q.Options {
				opts[j] = map[string]string{"label": o.Label, "description": o.Description}
			}
			items[i] = map[string]any{
				"question":     q.Question,
				"options":      opts,
				"multi_select": q.MultiSelect,
			}
		}

		payload := map[string]any{
			"question_id": questionID,
			"run_id":      runID,
			"questions":   items,
		}

		evt := aguievents.NewCustomEvent("question_request", aguievents.WithValue(payload))
		sideCh <- sideEvent{event: evt}

		g.deps.Logger.Info("question_request sent, waiting for answer",
			"run_id", runID, "question_id", questionID)

		select {
		case answers := <-resultCh:
			g.hitl.removeQuestion(runID)
			return answers, nil
		case <-time.After(hitlTimeout):
			g.hitl.removeQuestion(runID)
			return nil, fmt.Errorf("question timed out after %s", hitlTimeout)
		case <-ctx.Done():
			g.hitl.removeQuestion(runID)
			return nil, ctx.Err()
		}
	}
}

func (g *Gateway) makePermissionHandler(runID string, sideCh chan<- sideEvent) api.PermissionRequestHandler {
	return func(ctx context.Context, req api.PermissionRequest) (coreevents.PermissionDecisionType, error) {
		approvalID := uuid.New().String()
		resultCh := make(chan coreevents.PermissionDecisionType, 1)

		g.hitl.registerApproval(runID, &pendingApproval{
			ApprovalID: approvalID,
			ResultCh:   resultCh,
		})

		paramsJSON, _ := json.Marshal(req.ToolParams)
		payload := map[string]any{
			"approval_id": approvalID,
			"run_id":      runID,
			"tool_name":   req.ToolName,
			"tool_params": json.RawMessage(paramsJSON),
			"reason":      req.Reason,
		}

		evt := aguievents.NewCustomEvent("approval_request", aguievents.WithValue(payload))
		sideCh <- sideEvent{event: evt}

		g.deps.Logger.Info("approval_request sent, waiting for decision",
			"run_id", runID, "approval_id", approvalID)

		select {
		case decision := <-resultCh:
			g.hitl.removeApproval(runID)
			return decision, nil
		case <-time.After(hitlTimeout):
			g.hitl.removeApproval(runID)
			return coreevents.PermissionDeny, fmt.Errorf("approval timed out after %s", hitlTimeout)
		case <-ctx.Done():
			g.hitl.removeApproval(runID)
			return coreevents.PermissionDeny, ctx.Err()
		}
	}
}
