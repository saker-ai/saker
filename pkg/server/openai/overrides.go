package openai

import (
	"encoding/json"

	"github.com/saker-ai/saker/pkg/api"
)

// buildModelOverrides extracts the OpenAI standard sampling parameters
// from a ChatRequest into a *api.ModelOverrides. Returns nil when the
// request specifies no overrides, so callers can leave Request.ModelOverrides
// nil and let the runtime defaults apply.
//
// max_tokens / max_completion_tokens collapse onto a single int — the newer
// max_completion_tokens wins when both are set (it is the spec-correct field
// for o1+ reasoning models). Negative or zero values are ignored so a
// careless caller cannot accidentally disable the runtime cap.
//
// stop accepts either a string or []string per spec; other shapes are
// silently dropped (forward-compat for new parameter encodings).
//
// tool_choice is forwarded only when the value is a plain string ("auto",
// "none", "required", or a tool name). The struct form
// {"type":"function","function":{"name":"..."}} is not yet plumbed —
// providers will see ToolChoice="" and fall back to their default.
func buildModelOverrides(r ChatRequest) *api.ModelOverrides {
	o := api.ModelOverrides{}
	has := false

	if r.Temperature != nil {
		v := *r.Temperature
		o.Temperature = &v
		has = true
	}
	if r.TopP != nil {
		v := *r.TopP
		o.TopP = &v
		has = true
	}

	maxTok := r.MaxCompletionT
	if maxTok == 0 {
		maxTok = r.MaxTokens
	}
	if maxTok > 0 {
		v := maxTok
		o.MaxTokens = &v
		has = true
	}

	if stops := coerceStop(r.Stop); len(stops) > 0 {
		o.Stop = stops
		has = true
	}

	if r.Seed != nil {
		v := int64(*r.Seed)
		o.Seed = &v
		has = true
	}

	if s, ok := stringFromAny(r.ToolChoice); ok && s != "" {
		o.ToolChoice = s
		has = true
	}

	if r.ParallelToolCalls != nil {
		v := *r.ParallelToolCalls
		o.ParallelToolCalls = &v
		has = true
	}

	if !has {
		return nil
	}
	return &o
}

// coerceStop normalizes the OpenAI stop field which can be a string or
// []string per spec. Other JSON shapes are silently dropped.
func coerceStop(v any) []string {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []string:
		return filterNonEmpty(t)
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case json.RawMessage:
		var s string
		if err := json.Unmarshal(t, &s); err == nil {
			if s == "" {
				return nil
			}
			return []string{s}
		}
		var arr []string
		if err := json.Unmarshal(t, &arr); err == nil {
			return filterNonEmpty(arr)
		}
	}
	return nil
}

// stringFromAny extracts a plain string out of an any-typed JSON value.
// Returns ("", false) for non-string shapes so callers can fall through.
func stringFromAny(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	if raw, ok := v.(json.RawMessage); ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s, true
		}
	}
	return "", false
}

func filterNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
