package api

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	coreevents "github.com/saker-ai/saker/pkg/core/events"
	corehooks "github.com/saker-ai/saker/pkg/core/hooks"
)

// HookRecorder records hook events for inspection.
type HookRecorder interface {
	Record(coreevents.Event)
	Drain() []coreevents.Event
}

// hookRecorder stores hook events for the response payload.
// It is safe for concurrent use from multiple goroutines.
type hookRecorder struct {
	mu     sync.Mutex
	events []coreevents.Event
}

func (r *hookRecorder) Record(evt coreevents.Event) {
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}
	r.mu.Lock()
	r.events = append(r.events, evt)
	r.mu.Unlock()
}

func (r *hookRecorder) Drain() []coreevents.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return nil
	}
	out := append([]coreevents.Event(nil), r.events...)
	r.events = nil
	return out
}

// defaultHookRecorder implements HookRecorder when callers do not provide one.
func defaultHookRecorder() *hookRecorder {
	return &hookRecorder{}
}

// runtimeHookAdapter wraps the hook executor and recorder.
type runtimeHookAdapter struct {
	executor *corehooks.Executor
	recorder HookRecorder
}

func (h *runtimeHookAdapter) PreToolUse(ctx context.Context, evt coreevents.ToolUsePayload) (map[string]any, error) {
	if h == nil || h.executor == nil {
		return evt.Params, nil
	}
	results, err := h.executor.Execute(ctx, coreevents.Event{Type: coreevents.PreToolUse, Payload: evt})
	if err != nil {
		return nil, err
	}
	h.record(coreevents.Event{Type: coreevents.PreToolUse, Payload: evt})

	// Print hook stderr output for debugging
	for _, res := range results {
		if res.Stderr != "" {
			fmt.Fprint(os.Stderr, res.Stderr)
		}
	}

	params := evt.Params
	for _, res := range results {
		if res.Output == nil {
			continue
		}
		// Check top-level decision
		if res.Output.Decision == "deny" {
			return nil, fmt.Errorf("%w: %s", ErrToolUseDenied, evt.Name)
		}
		// Check continue=false
		if res.Output.Continue != nil && !*res.Output.Continue {
			return nil, fmt.Errorf("%w: %s", ErrToolUseDenied, evt.Name)
		}
		// Check hookSpecificOutput for PreToolUse
		if hso := res.Output.HookSpecificOutput; hso != nil {
			switch hso.PermissionDecision {
			case "deny":
				return nil, fmt.Errorf("%w: %s", ErrToolUseDenied, evt.Name)
			case "ask":
				return nil, fmt.Errorf("%w: %s", ErrToolUseRequiresApproval, evt.Name)
			}
			if hso.UpdatedInput != nil {
				params = hso.UpdatedInput
			}
		}
	}
	return params, nil
}

func (h *runtimeHookAdapter) PostToolUse(ctx context.Context, evt coreevents.ToolResultPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	results, err := h.executor.Execute(ctx, coreevents.Event{Type: coreevents.PostToolUse, Payload: evt})
	if err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.PostToolUse, Payload: evt})

	// Print hook stderr output for debugging
	for _, res := range results {
		if res.Stderr != "" {
			fmt.Fprint(os.Stderr, res.Stderr)
		}
	}

	// Check if any hook wants to stop
	for _, res := range results {
		if res.Output != nil && res.Output.Continue != nil && !*res.Output.Continue {
			return fmt.Errorf("hooks: PostToolUse hook requested stop: %s", res.Output.StopReason)
		}
	}
	return nil
}

func (h *runtimeHookAdapter) UserPrompt(ctx context.Context, prompt string) error {
	if h == nil || h.executor == nil {
		return nil
	}
	payload := coreevents.UserPromptPayload{Prompt: prompt}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.UserPromptSubmit, Payload: payload}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.UserPromptSubmit, Payload: payload})
	return nil
}

func (h *runtimeHookAdapter) Stop(ctx context.Context, reason string) error {
	if h == nil || h.executor == nil {
		return nil
	}
	payload := coreevents.StopPayload{Reason: reason}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.Stop, Payload: payload}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.Stop, Payload: payload})
	return nil
}

func (h *runtimeHookAdapter) PermissionRequest(ctx context.Context, evt coreevents.PermissionRequestPayload) (coreevents.PermissionDecisionType, error) {
	if h == nil || h.executor == nil {
		return coreevents.PermissionAsk, nil
	}
	results, err := h.executor.Execute(ctx, coreevents.Event{Type: coreevents.PermissionRequest, Payload: evt})
	if err != nil {
		return coreevents.PermissionAsk, err
	}

	if len(results) == 0 {
		h.record(coreevents.Event{Type: coreevents.PermissionRequest, Payload: evt})
		return coreevents.PermissionAsk, nil
	}

	decision := coreevents.PermissionAllow
	for _, res := range results {
		if res.Output == nil {
			continue
		}
		switch res.Output.Decision {
		case "deny":
			decision = coreevents.PermissionDeny
		case "ask":
			if decision != coreevents.PermissionDeny {
				decision = coreevents.PermissionAsk
			}
		case "allow":
			// keep current decision
		}
	}
	h.record(coreevents.Event{Type: coreevents.PermissionRequest, Payload: evt})
	return decision, nil
}

func (h *runtimeHookAdapter) SessionStart(ctx context.Context, evt coreevents.SessionPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.SessionStart, Payload: evt}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.SessionStart, Payload: evt})
	return nil
}

func (h *runtimeHookAdapter) SessionEnd(ctx context.Context, evt coreevents.SessionPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.SessionEnd, Payload: evt}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.SessionEnd, Payload: evt})
	return nil
}

func (h *runtimeHookAdapter) SubagentStart(ctx context.Context, evt coreevents.SubagentStartPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.SubagentStart, Payload: evt}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.SubagentStart, Payload: evt})
	return nil
}

func (h *runtimeHookAdapter) SubagentStop(ctx context.Context, evt coreevents.SubagentStopPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.SubagentStop, Payload: evt}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.SubagentStop, Payload: evt})
	return nil
}

func (h *runtimeHookAdapter) ModelSelected(ctx context.Context, evt coreevents.ModelSelectedPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.ModelSelected, Payload: evt}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.ModelSelected, Payload: evt})
	return nil
}

func (h *runtimeHookAdapter) record(evt coreevents.Event) {
	if h == nil || h.recorder == nil {
		return
	}
	h.recorder.Record(evt)
}
