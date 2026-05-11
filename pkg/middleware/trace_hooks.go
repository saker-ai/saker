package middleware

import (
	"context"
	"fmt"
	"strings"
)

// trace_hooks.go owns the per-stage hook callbacks and the record() pipeline
// that turns raw State+Stage into a TraceEvent. Lifecycle (sessions/IO) is in
// trace_lifecycle.go; skill-snapshot tracing in trace_skills.go.

func (m *TraceMiddleware) BeforeAgent(ctx context.Context, st *State) error {
	m.traceSkillsSnapshot(ctx, st, true)
	m.record(ctx, StageBeforeAgent, st)
	return nil
}

func (m *TraceMiddleware) BeforeModel(ctx context.Context, st *State) error {
	m.record(ctx, StageBeforeModel, st)
	return nil
}

func (m *TraceMiddleware) AfterModel(ctx context.Context, st *State) error {
	m.record(ctx, StageAfterModel, st)
	return nil
}

func (m *TraceMiddleware) BeforeTool(ctx context.Context, st *State) error {
	m.record(ctx, StageBeforeTool, st)
	return nil
}

func (m *TraceMiddleware) AfterTool(ctx context.Context, st *State) error {
	m.record(ctx, StageAfterTool, st)
	return nil
}

func (m *TraceMiddleware) AfterAgent(ctx context.Context, st *State) error {
	m.traceSkillsSnapshot(ctx, st, false)
	m.record(ctx, StageAfterAgent, st)
	return nil
}

func (m *TraceMiddleware) record(ctx context.Context, stage Stage, st *State) {
	if m == nil || st == nil {
		return
	}
	ensureStateValues(st)
	sessionID := m.resolveSessionID(ctx, st)
	now := m.now()
	evt := TraceEvent{
		Timestamp: now,
		Stage:     stageName(stage),
		Iteration: st.Iteration,
		SessionID: sessionID,
	}
	evt.Input, evt.Output = stageIO(stage, st)
	evt.Input = sanitizePayload(evt.Input)
	evt.Output = sanitizePayload(evt.Output)
	evt.ModelRequest = captureModelRequest(stage, st)
	evt.ModelResponse = captureModelResponse(stage, st)
	evt.ToolCall = captureToolCall(stage, st)
	evt.ToolResult = captureToolResult(stage, st, evt.ToolCall)
	evt.Error = captureTraceError(stage, st, evt.ToolResult)
	evt.DurationMS = m.trackDuration(stage, st, now)

	sess := m.sessionFor(sessionID)
	if sess == nil {
		return
	}
	sess.append(evt, m)
}

func (m *TraceMiddleware) resolveSessionID(ctx context.Context, st *State) string {
	if st != nil {
		if id := firstString(st.Values, "trace.session_id", "session_id", "sessionID", "session"); id != "" {
			return id
		}
	}
	if id := contextString(ctx, TraceSessionIDContextKey); id != "" {
		return id
	}
	if id := contextString(ctx, SessionIDContextKey); id != "" {
		return id
	}
	if id := contextString(ctx, "trace.session_id"); id != "" {
		return id
	}
	if id := contextString(ctx, "session_id"); id != "" {
		return id
	}
	if st != nil {
		return fmt.Sprintf("session-%p", st)
	}
	return fmt.Sprintf("session-%d", m.now().UnixNano())
}

func contextString(ctx context.Context, key any) string {
	if ctx == nil || key == nil {
		return ""
	}
	return anyToString(ctx.Value(key))
}

func firstString(values map[string]any, keys ...string) string {
	if len(keys) == 0 || len(values) == 0 {
		return ""
	}
	for _, key := range keys {
		if val, ok := values[key]; ok {
			if s := anyToString(val); s != "" {
				return s
			}
		}
	}
	return ""
}

func anyToString(v any) string {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case fmt.Stringer:
		return strings.TrimSpace(val.String())
	case []byte:
		return strings.TrimSpace(string(val))
	}
	return ""
}

func stageIO(stage Stage, st *State) (any, any) {
	if st == nil {
		return nil, nil
	}
	switch stage {
	case StageBeforeAgent:
		return st.Agent, nil
	case StageBeforeModel:
		if st.ModelInput != nil {
			return st.ModelInput, nil
		}
		return st.Agent, nil
	case StageAfterModel:
		return st.ModelInput, st.ModelOutput
	case StageBeforeTool:
		return st.ToolCall, nil
	case StageAfterTool:
		return st.ToolCall, st.ToolResult
	case StageAfterAgent:
		return st.Agent, st.ModelOutput
	default:
		return nil, nil
	}
}

func stageName(stage Stage) string {
	switch stage {
	case StageBeforeAgent:
		return "before_agent"
	case StageBeforeModel:
		return "before_model"
	case StageAfterModel:
		return "after_model"
	case StageBeforeTool:
		return "before_tool"
	case StageAfterTool:
		return "after_tool"
	case StageAfterAgent:
		return "after_agent"
	default:
		return fmt.Sprintf("stage_%d", stage)
	}
}
