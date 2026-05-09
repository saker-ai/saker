package subagents

import (
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/message"
)

func TestIsForkTarget(t *testing.T) {
	tests := []struct {
		target string
		want   bool
	}{
		{"", true},
		{"fork", true},
		{"Fork", true},
		{"  fork  ", true},
		{"general-purpose", false},
		{"explore", false},
	}
	for _, tt := range tests {
		if got := IsForkTarget(tt.target); got != tt.want {
			t.Errorf("IsForkTarget(%q) = %v, want %v", tt.target, got, tt.want)
		}
	}
}

func TestIsInForkChild(t *testing.T) {
	t.Run("no fork boilerplate", func(t *testing.T) {
		msgs := []message.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		}
		if IsInForkChild(msgs) {
			t.Error("expected false for messages without fork boilerplate")
		}
	})

	t.Run("with fork boilerplate", func(t *testing.T) {
		msgs := []message.Message{
			{Role: "user", Content: "hello"},
			{Role: "user", Content: "<fork-boilerplate>\nSTOP. READ THIS FIRST.\n</fork-boilerplate>"},
		}
		if !IsInForkChild(msgs) {
			t.Error("expected true for messages with fork boilerplate")
		}
	})

	t.Run("boilerplate in assistant message ignored", func(t *testing.T) {
		msgs := []message.Message{
			{Role: "assistant", Content: "<fork-boilerplate>test</fork-boilerplate>"},
		}
		if IsInForkChild(msgs) {
			t.Error("expected false when boilerplate is only in assistant message")
		}
	})
}

func TestBuildChildDirective(t *testing.T) {
	directive := "Search for all TODO comments in the codebase"
	result := BuildChildDirective(directive)

	if !strings.Contains(result, "<fork-boilerplate>") {
		t.Error("missing opening fork-boilerplate tag")
	}
	if !strings.Contains(result, "</fork-boilerplate>") {
		t.Error("missing closing fork-boilerplate tag")
	}
	if !strings.Contains(result, "You are a forked worker process") {
		t.Error("missing worker instructions")
	}
	if !strings.Contains(result, ForkDirectivePrefix+directive) {
		t.Error("missing directive with prefix")
	}
}

func TestBuildForkedMessages_NoToolCalls(t *testing.T) {
	msgs := BuildForkedMessages("do something", message.Message{
		Role:    "assistant",
		Content: "just text",
	})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected user role, got %s", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "<fork-boilerplate>") {
		t.Error("missing fork boilerplate in fallback message")
	}
}

func TestBuildForkedMessages_WithToolCalls(t *testing.T) {
	assistant := message.Message{
		Role:    "assistant",
		Content: "I'll help you",
		ToolCalls: []message.ToolCall{
			{ID: "tc_1", Name: "Bash", Arguments: map[string]any{"command": "ls"}},
			{ID: "tc_2", Name: "Read", Arguments: map[string]any{"path": "file.go"}},
		},
	}

	msgs := BuildForkedMessages("analyze the output", assistant)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	// First message is the cloned assistant
	if msgs[0].Role != "assistant" {
		t.Errorf("expected assistant role for first message, got %s", msgs[0].Role)
	}
	if len(msgs[0].ToolCalls) != 2 {
		t.Errorf("expected 2 tool calls in cloned assistant, got %d", len(msgs[0].ToolCalls))
	}

	// Second message is user with placeholder results + directive
	if msgs[1].Role != "user" {
		t.Errorf("expected user role for second message, got %s", msgs[1].Role)
	}
	content := msgs[1].Content
	if !strings.Contains(content, ForkPlaceholderResult) {
		t.Error("missing placeholder result in user message")
	}
	if !strings.Contains(content, "tc_1") {
		t.Error("missing tool_result for tc_1")
	}
	if !strings.Contains(content, "tc_2") {
		t.Error("missing tool_result for tc_2")
	}
	if !strings.Contains(content, "<fork-boilerplate>") {
		t.Error("missing fork boilerplate in user message")
	}
	if !strings.Contains(content, "analyze the output") {
		t.Error("missing directive in user message")
	}
}

func TestBuildForkedMessages_DoesNotMutateOriginal(t *testing.T) {
	original := message.Message{
		Role:    "assistant",
		Content: "original content",
		ToolCalls: []message.ToolCall{
			{ID: "tc_1", Name: "Bash", Arguments: map[string]any{"command": "ls"}},
		},
	}
	originalContent := original.Content

	msgs := BuildForkedMessages("directive", original)

	// Original should be unchanged
	if original.Content != originalContent {
		t.Error("original message was mutated")
	}
	// Forked message should be independent
	if &msgs[0].ToolCalls[0] == &original.ToolCalls[0] {
		t.Error("tool calls share memory with original")
	}
}
