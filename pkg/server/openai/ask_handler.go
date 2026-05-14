package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cinience/saker/pkg/runhub"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
	"github.com/google/uuid"
)

// makeAskQuestionFunc returns a toolbuiltin.AskQuestionFunc that pauses the
// agent run by publishing OpenAI-style tool_calls chunks, then blocks until
// the client submits an answer via the resume path.
//
// The returned function is safe to call multiple times (multi-round asking
// within a single turn). Each invocation allocates a fresh pauseCh for the
// next SSE consumer to monitor.
func (g *Gateway) makeAskQuestionFunc(
	hubRun *runhub.Run,
	builder *chatChunkBuilder,
	ps *pauseSignal,
) toolbuiltin.AskQuestionFunc {
	return func(ctx context.Context, questions []toolbuiltin.Question) (map[string]string, error) {
		toolCallID := "call_axq_" + uuid.New().String()[:12]

		args := marshalAskArgs(questions)

		// Publish tool_calls delta chunk
		tcChunk := builder.envelope(ChatChoice{
			Index: 0,
			Delta: &ChatMessageOut{
				ToolCalls: []ChatToolCall{{
					ID:   toolCallID,
					Type: "function",
					Function: ChatToolCallInvocation{
						Name:      "ask_user_question",
						Arguments: args,
					},
				}},
			},
		})
		if data, err := json.Marshal(tcChunk); err == nil {
			hubRun.Publish("chunk", data)
		}

		// Publish finish_reason: "tool_calls" chunk
		finishChunk := builder.envelope(ChatChoice{
			Index:        0,
			Delta:        &ChatMessageOut{},
			FinishReason: "tool_calls",
		})
		if data, err := json.Marshal(finishChunk); err == nil {
			hubRun.Publish("chunk", data)
		}

		// Register pending ask BEFORE signaling pause (race-free: the submit
		// handler checks the registry after the SSE consumer has returned).
		answerCh := make(chan askAnswer, 1)
		g.pendingAsks.Register(&pendingAsk{
			RunID:      hubRun.ID,
			SessionID:  hubRun.SessionID,
			TenantID:   hubRun.TenantID,
			ToolCallID: toolCallID,
			AnswerCh:   answerCh,
			Pause:      ps,
			CreatedAt:  time.Now(),
		})
		hubRun.SetStatus(runhub.RunStatusRequiresAction)

		// Signal the SSE consumer to write [DONE] and close.
		ps.Signal()

		g.deps.Logger.Info("ask_user_question paused run",
			"run_id", hubRun.ID,
			"tool_call_id", toolCallID,
		)

		// Block until answer, cancel, or context timeout.
		select {
		case ans := <-answerCh:
			g.pendingAsks.Remove(hubRun.ID)
			hubRun.SetStatus(runhub.RunStatusInProgress)
			switch ans.Action {
			case "cancel":
				return nil, fmt.Errorf("user cancelled")
			case "decline":
				return map[string]string{}, nil
			default:
				return ans.Answers, nil
			}
		case <-ctx.Done():
			g.pendingAsks.Remove(hubRun.ID)
			return nil, ctx.Err()
		}
	}
}

// marshalAskArgs serializes questions into the JSON function arguments string
// that clients see in the tool_calls chunk.
func marshalAskArgs(questions []toolbuiltin.Question) string {
	payload := map[string]any{"questions": questions}
	data, err := json.Marshal(payload)
	if err != nil {
		return `{"questions":[]}`
	}
	return string(data)
}

// parseToolResponse parses the client's tool response content into answers and action.
// Supports two forms:
//   - Form A (simple map): {"question": "answer", ...}
//   - Form B (three-state envelope): {"action": "accept|decline|cancel", "content": {...}}
func parseToolResponse(raw json.RawMessage) (answers map[string]string, action string, err error) {
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("empty tool response content")
	}

	// The content field is a JSON string (per OpenAI spec: tool message content is a string).
	// Try to unmarshal as a string first, then parse the inner JSON.
	var contentStr string
	if err := json.Unmarshal(raw, &contentStr); err == nil {
		// It's a JSON-encoded string — parse the inner value.
		raw = json.RawMessage(contentStr)
	}
	// If unmarshal-as-string failed, raw is already the direct JSON object.

	// Try Form B first (has "action" key).
	var envelope struct {
		Action  string            `json:"action"`
		Content map[string]string `json:"content"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Action != "" {
		action = envelope.Action
		if action == "accept" || action == "" {
			if envelope.Content == nil {
				return nil, "", fmt.Errorf("action=accept requires content field")
			}
			return envelope.Content, "accept", nil
		}
		return envelope.Content, action, nil
	}

	// Form A: direct map
	var directMap map[string]string
	if err := json.Unmarshal(raw, &directMap); err != nil {
		return nil, "", fmt.Errorf("tool response must be JSON object (map[string]string) or {action, content} envelope: %w", err)
	}
	return directMap, "accept", nil
}
