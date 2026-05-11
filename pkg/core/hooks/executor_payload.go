package hooks

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cinience/saker/pkg/core/events"
)

// executor_payload.go centralises everything related to building the JSON
// payload streamed to a hook command on stdin and to extracting matcher
// targets from event payloads. This is purely a translation layer between the
// strongly typed events.Event and the spec-defined hook envelope.

// extractMatcherTarget returns the string to match against the hook's selector
// based on event type and payload, per Claude Code spec:
// - PreToolUse/PostToolUse/PostToolUseFailure/PermissionRequest → tool name
// - SessionStart → source; SessionEnd → reason
// - Notification → notification_type; PreCompact → trigger
// - SubagentStart/SubagentStop → agent_type (fallback to name)
// - UserPromptSubmit/Stop → always match (return empty to skip matcher)
func extractMatcherTarget(eventType events.EventType, payload any) string {
	switch eventType {
	case events.PreToolUse:
		if p, ok := payload.(events.ToolUsePayload); ok {
			return p.Name
		}
	case events.PostToolUse, events.PostToolUseFailure:
		if p, ok := payload.(events.ToolResultPayload); ok {
			return p.Name
		}
	case events.PermissionRequest:
		if p, ok := payload.(events.PermissionRequestPayload); ok {
			return p.ToolName
		}
	case events.SessionStart:
		if p, ok := payload.(events.SessionStartPayload); ok {
			return p.Source
		}
		if p, ok := payload.(events.SessionPayload); ok {
			if src, ok := p.Metadata["source"].(string); ok {
				return src
			}
		}
	case events.SessionEnd:
		if p, ok := payload.(events.SessionEndPayload); ok {
			return p.Reason
		}
		if p, ok := payload.(events.SessionPayload); ok {
			if reason, ok := p.Metadata["reason"].(string); ok {
				return reason
			}
		}
	case events.Notification:
		if p, ok := payload.(events.NotificationPayload); ok {
			return p.NotificationType
		}
	case events.PreCompact:
		if p, ok := payload.(events.PreCompactPayload); ok {
			return p.Trigger
		}
	case events.SubagentStart:
		if p, ok := payload.(events.SubagentStartPayload); ok {
			if p.AgentType != "" {
				return p.AgentType
			}
			return p.Name
		}
	case events.SubagentStop:
		if p, ok := payload.(events.SubagentStopPayload); ok {
			if p.AgentType != "" {
				return p.AgentType
			}
			return p.Name
		}
	case events.UserPromptSubmit, events.Stop:
		// These events always match (no matcher support)
		return ""
	}
	return ""
}

func validateEvent(t events.EventType) error {
	switch t {
	case events.PreToolUse, events.PostToolUse, events.PostToolUseFailure, events.PreCompact, events.ContextCompacted,
		events.Notification, events.UserPromptSubmit,
		events.SessionStart, events.SessionEnd, events.Stop, events.TokenUsage,
		events.SubagentStart, events.SubagentStop,
		events.PermissionRequest, events.ModelSelected:
		return nil
	default:
		return fmt.Errorf("hooks: unsupported event %s", t)
	}
}

func buildPayload(evt events.Event) ([]byte, error) {
	envelope := map[string]any{
		"hook_event_name": evt.Type,
	}
	if evt.SessionID != "" {
		envelope["session_id"] = evt.SessionID
	}

	// Flatten payload fields into envelope per Claude Code spec.
	switch p := evt.Payload.(type) {
	case events.ToolUsePayload:
		envelope["tool_name"] = p.Name
		envelope["tool_input"] = p.Params
		if p.ToolUseID != "" {
			envelope["tool_use_id"] = p.ToolUseID
		}
	case events.ToolResultPayload:
		envelope["tool_name"] = p.Name
		if p.Params != nil {
			envelope["tool_input"] = p.Params
		}
		if p.ToolUseID != "" {
			envelope["tool_use_id"] = p.ToolUseID
		}
		if p.Result != nil {
			envelope["tool_result"] = p.Result
		}
		if p.Duration > 0 {
			envelope["duration_ms"] = p.Duration.Milliseconds()
		}
		if p.Err != nil {
			envelope["error"] = p.Err.Error()
			envelope["is_error"] = true
		}
	case events.PreCompactPayload:
		envelope["trigger"] = p.Trigger
		if p.CustomInstructions != "" {
			envelope["custom_instructions"] = p.CustomInstructions
		}
		envelope["estimated_tokens"] = p.EstimatedTokens
		envelope["token_limit"] = p.TokenLimit
		envelope["threshold"] = p.Threshold
		envelope["preserve_count"] = p.PreserveCount
	case events.ContextCompactedPayload:
		envelope["summary"] = p.Summary
		envelope["original_messages"] = p.OriginalMessages
		envelope["preserved_messages"] = p.PreservedMessages
		envelope["estimated_tokens_before"] = p.EstimatedTokensBefore
		envelope["estimated_tokens_after"] = p.EstimatedTokensAfter
	case events.SubagentStartPayload:
		envelope["agent_name"] = p.Name
		if p.AgentID != "" {
			envelope["agent_id"] = p.AgentID
		}
		if p.AgentType != "" {
			envelope["agent_type"] = p.AgentType
		}
		if p.Metadata != nil {
			envelope["metadata"] = p.Metadata
		}
	case events.SubagentStopPayload:
		envelope["agent_name"] = p.Name
		if p.Reason != "" {
			envelope["reason"] = p.Reason
		}
		if p.AgentID != "" {
			envelope["agent_id"] = p.AgentID
		}
		if p.AgentType != "" {
			envelope["agent_type"] = p.AgentType
		}
		if p.TranscriptPath != "" {
			envelope["transcript_path"] = p.TranscriptPath
		}
		envelope["stop_hook_active"] = p.StopHookActive
	case events.PermissionRequestPayload:
		envelope["tool_name"] = p.ToolName
		if p.ToolParams != nil {
			envelope["tool_input"] = p.ToolParams
		}
		if p.Reason != "" {
			envelope["reason"] = p.Reason
		}
	case events.SessionStartPayload:
		if p.SessionID != "" {
			envelope["session_id"] = p.SessionID
		}
		if p.Source != "" {
			envelope["source"] = p.Source
		}
		if p.Model != "" {
			envelope["model"] = p.Model
		}
		if p.AgentType != "" {
			envelope["agent_type"] = p.AgentType
		}
		if p.Metadata != nil {
			envelope["metadata"] = p.Metadata
		}
	case events.SessionEndPayload:
		if p.SessionID != "" {
			envelope["session_id"] = p.SessionID
		}
		if p.Reason != "" {
			envelope["reason"] = p.Reason
		}
		if p.Metadata != nil {
			envelope["metadata"] = p.Metadata
		}
	case events.SessionPayload:
		// Legacy compat: flatten session payload
		if p.SessionID != "" {
			envelope["session_id"] = p.SessionID
		}
		if p.Metadata != nil {
			envelope["metadata"] = p.Metadata
		}
	case events.NotificationPayload:
		if p.Title != "" {
			envelope["title"] = p.Title
		}
		envelope["message"] = p.Message
		if p.NotificationType != "" {
			envelope["notification_type"] = p.NotificationType
		}
		if p.Meta != nil {
			envelope["metadata"] = p.Meta
		}
	case events.UserPromptPayload:
		envelope["user_prompt"] = p.Prompt
	case events.StopPayload:
		if p.Reason != "" {
			envelope["reason"] = p.Reason
		}
		envelope["stop_hook_active"] = p.StopHookActive
	case events.ModelSelectedPayload:
		envelope["tool_name"] = p.ToolName
		envelope["model_tier"] = p.ModelTier
		if p.Reason != "" {
			envelope["reason"] = p.Reason
		}
	case nil:
		// allowed
	default:
		return nil, fmt.Errorf("hooks: unsupported payload type %T", evt.Payload)
	}

	// Add cwd to all payloads
	if cwd, err := os.Getwd(); err == nil {
		envelope["cwd"] = cwd
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("hooks: marshal payload: %w", err)
	}
	return data, nil
}
