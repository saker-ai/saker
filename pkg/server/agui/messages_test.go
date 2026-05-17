package agui

import (
	"testing"

	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
)

func TestMessagesToRequest_SimpleUser(t *testing.T) {
	t.Parallel()
	input := aguitypes.RunAgentInput{
		ThreadID: "thread_1",
		Messages: []aguitypes.Message{
			{Role: aguitypes.RoleUser, Content: "Hello saker"},
		},
	}
	req := messagesToRequest(input, Identity{})
	if req.Prompt != "Hello saker" {
		t.Errorf("Prompt = %q, want %q", req.Prompt, "Hello saker")
	}
	if req.SessionID != "thread_1" {
		t.Errorf("SessionID = %q, want thread_1", req.SessionID)
	}
}

func TestMessagesToRequest_LastUserMessage(t *testing.T) {
	t.Parallel()
	input := aguitypes.RunAgentInput{
		ThreadID: "t1",
		Messages: []aguitypes.Message{
			{Role: aguitypes.RoleUser, Content: "first"},
			{Role: aguitypes.RoleAssistant, Content: "ack"},
			{Role: aguitypes.RoleUser, Content: "second"},
		},
	}
	req := messagesToRequest(input, Identity{})
	if req.Prompt != "second" {
		t.Errorf("Prompt = %q, want last user message %q", req.Prompt, "second")
	}
}

func TestMessagesToRequest_NoUserMessage(t *testing.T) {
	t.Parallel()
	input := aguitypes.RunAgentInput{
		ThreadID: "t1",
		Messages: []aguitypes.Message{
			{Role: aguitypes.RoleAssistant, Content: "hello"},
		},
	}
	req := messagesToRequest(input, Identity{})
	if req.Prompt != "" {
		t.Errorf("Prompt should be empty when no user message, got %q", req.Prompt)
	}
}

func TestMessagesToRequest_ContextInjection(t *testing.T) {
	t.Parallel()
	input := aguitypes.RunAgentInput{
		ThreadID: "t1",
		Messages: []aguitypes.Message{
			{Role: aguitypes.RoleUser, Content: "do it"},
		},
		Context: []aguitypes.Context{
			{Description: "Project", Value: "saker"},
			{Description: "Language", Value: "Go"},
		},
	}
	req := messagesToRequest(input, Identity{})
	if req.Prompt == "do it" {
		t.Error("context should be prepended to prompt")
	}
	if got := req.Prompt; got == "" {
		t.Fatal("prompt should not be empty")
	}
	if !contains(req.Prompt, "Project: saker") {
		t.Errorf("prompt should contain context, got %q", req.Prompt)
	}
	if !contains(req.Prompt, "Language: Go") {
		t.Errorf("prompt should contain context, got %q", req.Prompt)
	}
	if !contains(req.Prompt, "do it") {
		t.Errorf("prompt should still contain user message, got %q", req.Prompt)
	}
}

func TestMessagesToRequest_IdentityPropagation(t *testing.T) {
	t.Parallel()
	input := aguitypes.RunAgentInput{
		ThreadID: "t1",
		Messages: []aguitypes.Message{
			{Role: aguitypes.RoleUser, Content: "hi"},
		},
	}
	id := Identity{Username: "alice", UserID: "u_1"}
	req := messagesToRequest(input, id)
	if req.User != "alice" {
		t.Errorf("User = %q, want alice", req.User)
	}
}

func TestMessagesToRequest_EmptyIdentity(t *testing.T) {
	t.Parallel()
	input := aguitypes.RunAgentInput{
		ThreadID: "t1",
		Messages: []aguitypes.Message{
			{Role: aguitypes.RoleUser, Content: "hi"},
		},
	}
	req := messagesToRequest(input, Identity{})
	if req.User != "" {
		t.Errorf("User should be empty, got %q", req.User)
	}
}

func TestExtractTextContent_String(t *testing.T) {
	t.Parallel()
	if got := extractTextContent("hello"); got != "hello" {
		t.Errorf("string content: got %q, want hello", got)
	}
}

func TestExtractTextContent_Nil(t *testing.T) {
	t.Parallel()
	if got := extractTextContent(nil); got != "" {
		t.Errorf("nil content: got %q, want empty", got)
	}
}

func TestExtractTextContent_TextParts(t *testing.T) {
	t.Parallel()
	parts := []map[string]string{
		{"type": "text", "text": "part1"},
		{"type": "text", "text": "part2"},
	}
	got := extractTextContent(parts)
	if !contains(got, "part1") || !contains(got, "part2") {
		t.Errorf("text parts: got %q, want both parts", got)
	}
}

func TestExtractTextContent_NonTextPartsIgnored(t *testing.T) {
	t.Parallel()
	parts := []map[string]string{
		{"type": "image", "text": ""},
		{"type": "text", "text": "visible"},
	}
	got := extractTextContent(parts)
	if !contains(got, "visible") {
		t.Errorf("got %q, want 'visible'", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
