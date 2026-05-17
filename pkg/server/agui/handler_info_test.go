package agui

import (
	"encoding/json"
	"testing"
)

func TestAguiUnwrapEnvelope_PlainBody(t *testing.T) {
	t.Parallel()
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	got, method := aguiUnwrapEnvelope(nil, body)
	if method != "" {
		t.Errorf("plain body should have empty method, got %q", method)
	}
	if string(got) != string(body) {
		t.Errorf("plain body should pass through unchanged, got %q", got)
	}
}

func TestAguiUnwrapEnvelope_InfoMethod(t *testing.T) {
	t.Parallel()
	body := []byte(`{"method":"info"}`)
	got, method := aguiUnwrapEnvelope(nil, body)
	if method != "info" {
		t.Errorf("method=info should return method='info', got %q", method)
	}
	if got != nil {
		t.Errorf("info method should return nil body, got %q", got)
	}
}

func TestAguiUnwrapEnvelope_AgentRunMethod(t *testing.T) {
	t.Parallel()
	inner := `{"messages":[{"role":"user","content":"hello"}]}`
	envelope := `{"method":"agent/run","body":` + inner + `}`
	got, method := aguiUnwrapEnvelope(nil, []byte(envelope))
	if method != "" {
		t.Errorf("agent/run should have empty method, got %q", method)
	}
	var parsed struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("failed to parse unwrapped body: %v", err)
	}
	if len(parsed.Messages) != 1 || parsed.Messages[0].Content != "hello" {
		t.Errorf("unwrapped body mismatch: %s", got)
	}
}

func TestAguiUnwrapEnvelope_AgentConnectMethod(t *testing.T) {
	t.Parallel()
	inner := `{"thread_id":"t1"}`
	envelope := `{"method":"agent/connect","body":` + inner + `}`
	got, method := aguiUnwrapEnvelope(nil, []byte(envelope))
	if method != "agent/connect" {
		t.Errorf("agent/connect should return method='agent/connect', got %q", method)
	}
	if string(got) != inner {
		t.Errorf("got %q, want %q", got, inner)
	}
}

func TestAguiUnwrapEnvelope_InvalidJSON(t *testing.T) {
	t.Parallel()
	body := []byte(`not json`)
	got, method := aguiUnwrapEnvelope(nil, body)
	if method != "" {
		t.Errorf("invalid JSON should have empty method, got %q", method)
	}
	if string(got) != string(body) {
		t.Errorf("invalid JSON should return original body, got %q", got)
	}
}

func TestAguiUnwrapEnvelope_UnknownMethod(t *testing.T) {
	t.Parallel()
	body := []byte(`{"method":"unknown","body":{}}`)
	got, method := aguiUnwrapEnvelope(nil, body)
	if method != "" {
		t.Errorf("unknown method should have empty method, got %q", method)
	}
	if string(got) != string(body) {
		t.Errorf("unknown method should return original, got %q", got)
	}
}

func TestAguiUnwrapEnvelope_AgentRunEmptyBody(t *testing.T) {
	t.Parallel()
	body := []byte(`{"method":"agent/run"}`)
	got, method := aguiUnwrapEnvelope(nil, body)
	if method != "" {
		t.Errorf("agent/run with empty body should have empty method, got %q", method)
	}
	if string(got) != string(body) {
		t.Errorf("agent/run with no body field should return original, got %q", got)
	}
}
