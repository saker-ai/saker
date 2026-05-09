package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/agent"
	coreevents "github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/middleware"
	"github.com/cinience/saker/pkg/runtime/subagents"
	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
)

func (t *runtimeToolExecutor) isAllowed(ctx context.Context, name string) bool {
	if t.yolo {
		return true
	}
	canon := canonicalToolName(name)
	if canon == "" {
		return false
	}
	reqAllowed := len(t.allow) == 0
	if len(t.allow) > 0 {
		_, reqAllowed = t.allow[canon]
	}
	subCtx, ok := subagents.FromContext(ctx)
	if !ok || len(subCtx.ToolWhitelist) == 0 {
		return reqAllowed
	}
	subSet := toLowerSet(subCtx.ToolWhitelist)
	if len(subSet) == 0 {
		return reqAllowed
	}
	_, subAllowed := subSet[canon]
	if len(t.allow) == 0 {
		return subAllowed
	}
	return reqAllowed && subAllowed
}

func buildPermissionResolver(hooks *runtimeHookAdapter, handler PermissionRequestHandler, approvals *security.ApprovalQueue, approver string, whitelistTTL time.Duration, approvalWait bool) tool.PermissionResolver {
	if hooks == nil && handler == nil && approvals == nil {
		return nil
	}
	return func(ctx context.Context, call tool.Call, decision security.PermissionDecision) (security.PermissionDecision, error) {
		if decision.Action != security.PermissionAsk {
			return decision, nil
		}

		req := PermissionRequest{
			ToolName:   call.Name,
			ToolParams: call.Params,
			SessionID:  call.SessionID,
			Rule:       decision.Rule,
			Target:     decision.Target,
			Reason:     buildPermissionReason(decision),
		}

		var record *security.ApprovalRecord
		if approvals != nil && strings.TrimSpace(call.SessionID) != "" {
			command := formatApprovalCommand(call.Name, decision.Target)
			rec, err := approvals.Request(call.SessionID, command, nil)
			if err != nil {
				return decision, err
			}
			record = rec
			req.Approval = rec
			if rec != nil && rec.State == security.ApprovalApproved && rec.AutoApproved {
				return decisionWithAction(decision, security.PermissionAllow), nil
			}
		}

		if hooks != nil {
			hookDecision, err := hooks.PermissionRequest(ctx, coreevents.PermissionRequestPayload{
				ToolName:   call.Name,
				ToolParams: call.Params,
				Reason:     req.Reason,
			})
			if err != nil {
				return decision, err
			}
			switch hookDecision {
			case coreevents.PermissionAllow:
				if record != nil {
					if _, err := approvals.Approve(record.ID, approvalActor(approver), whitelistTTL); err != nil {
						return decision, err
					}
				}
				return decisionWithAction(decision, security.PermissionAllow), nil
			case coreevents.PermissionDeny:
				if record != nil {
					if _, err := approvals.Deny(record.ID, approvalActor(approver), "denied by permission hook"); err != nil {
						return decision, err
					}
				}
				return decisionWithAction(decision, security.PermissionDeny), nil
			}
		}

		if handler != nil {
			hostDecision, err := handler(ctx, req)
			if err != nil {
				return decision, err
			}
			switch hostDecision {
			case coreevents.PermissionAllow:
				if record != nil {
					if _, err := approvals.Approve(record.ID, approvalActor(approver), whitelistTTL); err != nil {
						return decision, err
					}
				}
				return decisionWithAction(decision, security.PermissionAllow), nil
			case coreevents.PermissionDeny:
				if record != nil {
					if _, err := approvals.Deny(record.ID, approvalActor(approver), "denied by host"); err != nil {
						return decision, err
					}
				}
				return decisionWithAction(decision, security.PermissionDeny), nil
			}
		}

		if approvalWait && approvals != nil && record != nil {
			resolved, err := approvals.Wait(ctx, record.ID)
			if err != nil {
				return decision, err
			}
			switch resolved.State {
			case security.ApprovalApproved:
				return decisionWithAction(decision, security.PermissionAllow), nil
			case security.ApprovalDenied:
				return decisionWithAction(decision, security.PermissionDeny), nil
			}
		}

		return decision, nil
	}
}

func buildPermissionReason(decision security.PermissionDecision) string {
	rule := strings.TrimSpace(decision.Rule)
	target := strings.TrimSpace(decision.Target)
	switch {
	case rule == "" && target == "":
		return ""
	case rule == "":
		return fmt.Sprintf("target %q", target)
	case target == "":
		return fmt.Sprintf("rule %q", rule)
	default:
		return fmt.Sprintf("rule %q for %s", rule, target)
	}
}

func formatApprovalCommand(toolName, target string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return name
	}
	return fmt.Sprintf("%s(%s)", name, target)
}

func decisionWithAction(base security.PermissionDecision, action security.PermissionAction) security.PermissionDecision {
	base.Action = action
	return base
}

func approvalActor(approver string) string {
	if strings.TrimSpace(approver) == "" {
		return "host"
	}
	return strings.TrimSpace(approver)
}

// newSafetyMiddleware creates a SafetyMiddleware that bridges the agent.ToolResult
// type into the middleware layer for leak detection and injection sanitization.
func newSafetyMiddleware() *middleware.SafetyMiddleware {
	extract := func(toolResult any) (string, string, bool) {
		tr, ok := toolResult.(agent.ToolResult)
		if !ok {
			return "", "", false
		}
		return tr.Name, tr.Output, true
	}
	write := func(st *middleware.State, output string, meta map[string]any) {
		tr, ok := st.ToolResult.(agent.ToolResult)
		if !ok {
			return
		}
		tr.Output = output
		if tr.Metadata == nil {
			tr.Metadata = map[string]any{}
		}
		for k, v := range meta {
			tr.Metadata[k] = v
		}
		st.ToolResult = tr
	}
	return middleware.NewSafetyMiddleware(extract, write)
}

// newSubdirHintsMiddleware creates a SubdirHints middleware that bridges the
// agent.ToolCall / agent.ToolResult types into the middleware layer.
func newSubdirHintsMiddleware(workDir string) middleware.Middleware {
	return middleware.NewSubdirHints(middleware.SubdirHintsConfig{
		WorkingDir: workDir,
		ExtractInput: func(toolCall any) map[string]any {
			tc, ok := toolCall.(agent.ToolCall)
			if !ok {
				return nil
			}
			return tc.Input
		},
		AppendToResult: func(st *middleware.State, extra string) {
			tr, ok := st.ToolResult.(agent.ToolResult)
			if !ok {
				return
			}
			tr.Output += extra
			st.ToolResult = tr
		},
	})
}