package openai

import (
	"strings"
	"testing"
)

func TestParseExtraBody_NilReturnsZero(t *testing.T) {
	got, err := ParseExtraBody(nil)
	if err != nil {
		t.Fatalf("nil raw: unexpected err: %v", err)
	}
	if got.SessionID != "" || got.Interactive != nil || got.HumanInputMode != "" {
		t.Fatalf("nil raw should yield zero ExtraBody, got %+v", got)
	}
}

func TestParseExtraBody_AllFields(t *testing.T) {
	raw := map[string]any{
		"session_id":             "sess_abc",
		"interactive":            false,
		"human_input_mode":       "Terminate",
		"ask_user_question_mode": "tool_call",
		"expose_tool_calls":      true,
		"cancel_on_disconnect":   true,
		"expires_after_seconds":  float64(900),
		"system_prompt_mode":     "replace",
	}
	got, err := ParseExtraBody(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.SessionID != "sess_abc" {
		t.Errorf("SessionID: got %q want sess_abc", got.SessionID)
	}
	if got.Interactive == nil || *got.Interactive != false {
		t.Errorf("Interactive: got %v want pointer to false", got.Interactive)
	}
	if got.HumanInputMode != HumanInputTerminate {
		t.Errorf("HumanInputMode: got %q want terminate", got.HumanInputMode)
	}
	if got.AskUserQuestionMode != AskQuestionToolCall {
		t.Errorf("AskUserQuestionMode: got %q want tool_call", got.AskUserQuestionMode)
	}
	if !got.ExposeToolCalls {
		t.Error("ExposeToolCalls: want true")
	}
	if !got.CancelOnDisconnect {
		t.Error("CancelOnDisconnect: want true")
	}
	if got.ExpiresAfterSeconds != 900 {
		t.Errorf("ExpiresAfterSeconds: got %d want 900", got.ExpiresAfterSeconds)
	}
	if got.SystemPromptMode != SystemPromptReplace {
		t.Errorf("SystemPromptMode: got %q want replace", got.SystemPromptMode)
	}
}

func TestParseExtraBody_TypeErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{"session_id wrong type", map[string]any{"session_id": 42}, "session_id"},
		{"interactive wrong type", map[string]any{"interactive": "yes"}, "interactive"},
		{"human_input_mode unknown", map[string]any{"human_input_mode": "sometimes"}, "human_input_mode"},
		{"ask_user_question_mode unknown", map[string]any{"ask_user_question_mode": "maybe"}, "ask_user_question_mode"},
		{"expose_tool_calls wrong type", map[string]any{"expose_tool_calls": 1}, "expose_tool_calls"},
		{"expires_after_seconds out of range low", map[string]any{"expires_after_seconds": float64(10)}, "expires_after_seconds"},
		{"expires_after_seconds out of range high", map[string]any{"expires_after_seconds": float64(99999)}, "expires_after_seconds"},
		{"system_prompt_mode unknown", map[string]any{"system_prompt_mode": "merge"}, "system_prompt_mode"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseExtraBody(c.raw)
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %q, want substring %q", err.Error(), c.want)
			}
		})
	}
}

func TestParseExtraBody_UnknownKeyIgnored(t *testing.T) {
	raw := map[string]any{
		"session_id":          "x",
		"never_heard_of_this": "ok",
	}
	got, err := ParseExtraBody(raw)
	if err != nil {
		t.Fatalf("unknown key should be ignored: %v", err)
	}
	if got.SessionID != "x" {
		t.Errorf("expected SessionID to be parsed alongside unknown key, got %q", got.SessionID)
	}
}

func TestEffectiveHumanInputMode_Precedence(t *testing.T) {
	tt := true
	ff := false
	cases := []struct {
		name string
		eb   ExtraBody
		want HumanInputMode
	}{
		{"explicit always", ExtraBody{HumanInputMode: HumanInputAlways}, HumanInputAlways},
		{"explicit terminate", ExtraBody{HumanInputMode: HumanInputTerminate}, HumanInputTerminate},
		{"explicit never", ExtraBody{HumanInputMode: HumanInputNever}, HumanInputNever},
		{"alias true", ExtraBody{Interactive: &tt}, HumanInputAlways},
		{"alias false", ExtraBody{Interactive: &ff}, HumanInputNever},
		{"explicit wins over alias", ExtraBody{HumanInputMode: HumanInputTerminate, Interactive: &tt}, HumanInputTerminate},
		{"defaults to always", ExtraBody{}, HumanInputAlways},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.eb.EffectiveHumanInputMode(); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestEffectiveAskUserQuestionMode(t *testing.T) {
	tt := true
	cases := []struct {
		name string
		eb   ExtraBody
		want AskUserQuestionMode
	}{
		{"never coerces to disabled", ExtraBody{HumanInputMode: HumanInputNever, AskUserQuestionMode: AskQuestionToolCall}, AskQuestionDisabled},
		{"terminate coerces to fallback", ExtraBody{HumanInputMode: HumanInputTerminate, AskUserQuestionMode: AskQuestionToolCall}, AskQuestionFallback},
		{"always honors explicit tool_call", ExtraBody{HumanInputMode: HumanInputAlways, AskUserQuestionMode: AskQuestionToolCall}, AskQuestionToolCall},
		{"always defaults to fallback when empty", ExtraBody{HumanInputMode: HumanInputAlways}, AskQuestionFallback},
		{"alias true + explicit tool_call", ExtraBody{Interactive: &tt, AskUserQuestionMode: AskQuestionToolCall}, AskQuestionToolCall},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.eb.EffectiveAskUserQuestionMode(); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestEffectiveCancelOnDisconnect(t *testing.T) {
	cases := []struct {
		name string
		eb   ExtraBody
		want bool
	}{
		{"never forces true even if client said false", ExtraBody{HumanInputMode: HumanInputNever, CancelOnDisconnect: false}, true},
		{"always honors client false", ExtraBody{HumanInputMode: HumanInputAlways, CancelOnDisconnect: false}, false},
		{"always honors client true", ExtraBody{HumanInputMode: HumanInputAlways, CancelOnDisconnect: true}, true},
		{"terminate honors client false", ExtraBody{HumanInputMode: HumanInputTerminate}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.eb.EffectiveCancelOnDisconnect(); got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestEffectiveSystemPromptMode_Defaults(t *testing.T) {
	if got := (ExtraBody{}).EffectiveSystemPromptMode(); got != SystemPromptPrepend {
		t.Errorf("default = %q, want prepend", got)
	}
	if got := (ExtraBody{SystemPromptMode: SystemPromptReplace}).EffectiveSystemPromptMode(); got != SystemPromptReplace {
		t.Errorf("explicit replace = %q, want replace", got)
	}
}
