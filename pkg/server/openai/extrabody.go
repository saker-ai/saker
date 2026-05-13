package openai

import (
	"errors"
	"fmt"
	"strings"
)

// HumanInputMode mirrors AutoGen's three-state HITL umbrella switch.
// always = full interactive (default), terminate = half-auto with safe-tool
// allowlist, never = pure one-shot. See design §9.4.
type HumanInputMode string

const (
	// HumanInputAlways is the default: ask_user_question and approval are
	// fully wired up to the host (WebSocket UI, etc.).
	HumanInputAlways HumanInputMode = "always"
	// HumanInputTerminate runs without an interactive UI but still allows
	// safe (read-only) tools through the approval handler. ask_user_question
	// goes through the graceful fallback path.
	HumanInputTerminate HumanInputMode = "terminate"
	// HumanInputNever is the pure one-shot mode: ask_user_question is
	// stripped from the toolset, every approval auto-denies, and
	// cancel_on_disconnect is forced true.
	HumanInputNever HumanInputMode = "never"
)

// AskUserQuestionMode selects how AskUserQuestion is exposed to the LLM.
// Mirrors the three-state design in §9.3.
type AskUserQuestionMode string

const (
	// AskQuestionFallback (default): no askFn injection — the tool returns
	// Success=false with an "ask in text" prompt to the LLM.
	AskQuestionFallback AskUserQuestionMode = "fallback"
	// AskQuestionToolCall: upgrade to OpenAI tool_call, turn pauses in
	// requires_action until the client posts a tool message back.
	AskQuestionToolCall AskUserQuestionMode = "tool_call"
	// AskQuestionDisabled: strip ask_user_question from the toolset; LLM
	// cannot even attempt to call it.
	AskQuestionDisabled AskUserQuestionMode = "disabled"
)

// SystemPromptMode controls how the client-supplied system message
// composes with saker's persona. Default is prepend (client first, then
// persona). replace fully overrides persona; useful for SDK-as-LLM proxy.
type SystemPromptMode string

const (
	SystemPromptPrepend SystemPromptMode = "prepend"
	SystemPromptReplace SystemPromptMode = "replace"
)

// ExtraBody holds the saker-specific keys clients pass via the
// `extra_body` field on chat/completions and responses requests. All
// fields use pointer / zero-value semantics so we can distinguish
// "client didn't send this" from "client sent the zero value".
//
// Documented in .docs/openai-inbound-gateway.md §15.
type ExtraBody struct {
	// SessionID continues an existing saker session. Empty means a brand
	// new session is created server-side.
	SessionID string

	// Interactive is the deprecated bool alias. true → always, false → never.
	// Nil means "client did not send `interactive`". When both Interactive
	// and HumanInputMode are present, HumanInputMode wins (explicit > alias).
	Interactive *bool

	// HumanInputMode is the umbrella HITL switch (§9.4).
	HumanInputMode HumanInputMode

	// AskUserQuestionMode is the per-request override for AskUserQuestion
	// behavior. Empty means use the default (fallback) — unless
	// HumanInputMode forces a different value.
	AskUserQuestionMode AskUserQuestionMode

	// ExposeToolCalls controls whether non-AskUserQuestion tool_calls are
	// transparently emitted to the OpenAI client. Off by default — saker
	// runs the tool itself and only the final assistant text streams out.
	ExposeToolCalls bool

	// CancelOnDisconnect ends the run when the SSE client disconnects.
	// Default false (the run continues for reconnect). Forced true when
	// HumanInputMode is "never".
	CancelOnDisconnect bool

	// ExpiresAfterSeconds overrides the per-Run idle/await timeout. Zero
	// means use the operator default from Options.ExpiresAfterSeconds.
	ExpiresAfterSeconds int

	// SystemPromptMode picks how the client `system` message merges with
	// the persona system prompt. Empty means prepend.
	SystemPromptMode SystemPromptMode

	// AllowedTools is a per-request tool whitelist. When non-empty, only
	// tools whose canonical name appears in this list are sent to the LLM.
	// Maps directly to api.Request.ToolWhitelist.
	AllowedTools []string
}

// EffectiveHumanInputMode resolves the alias (Interactive) onto
// HumanInputMode, applying the documented precedence:
//
//   - HumanInputMode wins if explicitly set (non-empty)
//   - else fall back to Interactive (true → always, false → never)
//   - else default to "always"
func (e ExtraBody) EffectiveHumanInputMode() HumanInputMode {
	if e.HumanInputMode != "" {
		return e.HumanInputMode
	}
	if e.Interactive != nil {
		if *e.Interactive {
			return HumanInputAlways
		}
		return HumanInputNever
	}
	return HumanInputAlways
}

// EffectiveAskUserQuestionMode applies HumanInputMode coercion: never and
// terminate both downgrade to disabled (different reasons — never strips
// the tool entirely, terminate lets the fallback path run but still
// prevents real human prompts). always honors what the client sent.
func (e ExtraBody) EffectiveAskUserQuestionMode() AskUserQuestionMode {
	switch e.EffectiveHumanInputMode() {
	case HumanInputNever:
		return AskQuestionDisabled
	case HumanInputTerminate:
		// terminate: askFn isn't injected (so the tool falls back to
		// "ask in text"); semantically "fallback" not "disabled".
		return AskQuestionFallback
	}
	if e.AskUserQuestionMode == "" {
		return AskQuestionFallback
	}
	return e.AskUserQuestionMode
}

// EffectiveCancelOnDisconnect forces true when HumanInputMode is "never"
// (no human is going to reconnect, so don't waste tokens). Otherwise
// returns whatever the client requested (defaults to false).
func (e ExtraBody) EffectiveCancelOnDisconnect() bool {
	if e.EffectiveHumanInputMode() == HumanInputNever {
		return true
	}
	return e.CancelOnDisconnect
}

// EffectiveSystemPromptMode defaults to prepend.
func (e ExtraBody) EffectiveSystemPromptMode() SystemPromptMode {
	if e.SystemPromptMode == "" {
		return SystemPromptPrepend
	}
	return e.SystemPromptMode
}

// ParseExtraBody normalizes the raw JSON object out of the request body
// into a typed ExtraBody. Unknown keys are silently ignored (forward
// compatibility); type-mismatched known keys yield an error so clients
// see the problem fast instead of silently being routed to default
// behavior.
//
// raw == nil returns a zero-value ExtraBody, all defaults.
func ParseExtraBody(raw map[string]any) (ExtraBody, error) {
	out := ExtraBody{}
	if raw == nil {
		return out, nil
	}

	if v, ok := raw["session_id"]; ok {
		s, err := coerceString(v)
		if err != nil {
			return out, fmt.Errorf("extra_body.session_id: %w", err)
		}
		out.SessionID = s
	}

	if v, ok := raw["interactive"]; ok {
		b, err := coerceBool(v)
		if err != nil {
			return out, fmt.Errorf("extra_body.interactive: %w", err)
		}
		out.Interactive = &b
	}

	if v, ok := raw["human_input_mode"]; ok {
		s, err := coerceString(v)
		if err != nil {
			return out, fmt.Errorf("extra_body.human_input_mode: %w", err)
		}
		mode := HumanInputMode(strings.ToLower(strings.TrimSpace(s)))
		switch mode {
		case HumanInputAlways, HumanInputTerminate, HumanInputNever:
			out.HumanInputMode = mode
		default:
			return out, fmt.Errorf("extra_body.human_input_mode: unknown value %q (want always|terminate|never)", s)
		}
	}

	if v, ok := raw["ask_user_question_mode"]; ok {
		s, err := coerceString(v)
		if err != nil {
			return out, fmt.Errorf("extra_body.ask_user_question_mode: %w", err)
		}
		mode := AskUserQuestionMode(strings.ToLower(strings.TrimSpace(s)))
		switch mode {
		case AskQuestionFallback, AskQuestionToolCall, AskQuestionDisabled:
			out.AskUserQuestionMode = mode
		default:
			return out, fmt.Errorf("extra_body.ask_user_question_mode: unknown value %q (want fallback|tool_call|disabled)", s)
		}
	}

	if v, ok := raw["expose_tool_calls"]; ok {
		b, err := coerceBool(v)
		if err != nil {
			return out, fmt.Errorf("extra_body.expose_tool_calls: %w", err)
		}
		out.ExposeToolCalls = b
	}

	if v, ok := raw["cancel_on_disconnect"]; ok {
		b, err := coerceBool(v)
		if err != nil {
			return out, fmt.Errorf("extra_body.cancel_on_disconnect: %w", err)
		}
		out.CancelOnDisconnect = b
	}

	if v, ok := raw["expires_after_seconds"]; ok {
		n, err := coerceInt(v)
		if err != nil {
			return out, fmt.Errorf("extra_body.expires_after_seconds: %w", err)
		}
		if n < 60 || n > 86400 {
			return out, fmt.Errorf("extra_body.expires_after_seconds: %d outside allowed range 60..86400", n)
		}
		out.ExpiresAfterSeconds = n
	}

	if v, ok := raw["allowed_tools"]; ok {
		s, err := coerceStringSlice(v)
		if err != nil {
			return out, fmt.Errorf("extra_body.allowed_tools: %w", err)
		}
		out.AllowedTools = s
	}

	if v, ok := raw["system_prompt_mode"]; ok {
		s, err := coerceString(v)
		if err != nil {
			return out, fmt.Errorf("extra_body.system_prompt_mode: %w", err)
		}
		mode := SystemPromptMode(strings.ToLower(strings.TrimSpace(s)))
		switch mode {
		case SystemPromptPrepend, SystemPromptReplace:
			out.SystemPromptMode = mode
		default:
			return out, fmt.Errorf("extra_body.system_prompt_mode: unknown value %q (want prepend|replace)", s)
		}
	}

	return out, nil
}

func coerceString(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("expected string, got %T", v)
	}
	return s, nil
}

func coerceBool(v any) (bool, error) {
	if v == nil {
		return false, errors.New("value is null")
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("expected boolean, got %T", v)
	}
	return b, nil
}

func coerceStringSlice(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", v)
	}
	out := make([]string, 0, len(arr))
	for i, elem := range arr {
		s, ok := elem.(string)
		if !ok {
			return nil, fmt.Errorf("element [%d]: expected string, got %T", i, elem)
		}
		out = append(out, s)
	}
	return out, nil
}

// coerceInt accepts JSON numbers, which arrive as float64 from
// encoding/json. Truncating is fine — the only int-typed extra_body
// field is expires_after_seconds, which is bounded to [60, 86400]
// well within int range.
func coerceInt(v any) (int, error) {
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case int64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("expected number, got %T", v)
	}
}
