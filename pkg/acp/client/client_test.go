package client

import (
	"context"
	"testing"
	"time"

	"github.com/saker-ai/saker/pkg/runtime/subagents"
)

func TestEnsureACPFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"nil args", nil, []string{"--acp=true"}},
		{"empty args", []string{}, []string{"--acp=true"}},
		{"no acp flag", []string{"--mode=code"}, []string{"--mode=code", "--acp=true"}},
		{"has acp flag", []string{"--acp"}, []string{"--acp"}},
		{"has acp=true", []string{"--acp=true"}, []string{"--acp=true"}},
		{"has acp=false", []string{"--acp=false"}, []string{"--acp=false"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureACPFlag(tt.args)
			if len(got) != len(tt.want) {
				t.Fatalf("ensureACPFlag(%v) = %v, want %v", tt.args, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ensureACPFlag(%v)[%d] = %q, want %q", tt.args, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestEnsureACPFlag_DoesNotMutateInput(t *testing.T) {
	original := []string{"--verbose"}
	originalCopy := make([]string, len(original))
	copy(originalCopy, original)

	_ = ensureACPFlag(original)

	for i, v := range original {
		if v != originalCopy[i] {
			t.Fatalf("ensureACPFlag mutated input: original[%d] = %q, want %q", i, v, originalCopy[i])
		}
	}
}

func TestClientHandler_SessionUpdate(t *testing.T) {
	h := newClientHandler()

	if len(h.updatesSnapshot()) != 0 {
		t.Fatal("expected empty updates initially")
	}

	// The handler should collect updates without error.
	// We can't easily construct full SessionNotification without the SDK's
	// internal types, but we can test the basic flow.
	updates := h.updatesSnapshot()
	if len(updates) != 0 {
		t.Fatalf("expected 0 updates, got %d", len(updates))
	}

	h.clearUpdates()
	if len(h.updatesSnapshot()) != 0 {
		t.Fatal("expected empty updates after clear")
	}
}

func TestDialFailsWithEmptyCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := Dial(ctx, DialOptions{})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestDialFailsWithBadCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := Dial(ctx, DialOptions{Command: "/nonexistent/binary/path"})
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
}

// --- ACPRunner tests ---

type mockRunner struct {
	called bool
	result subagents.Result
	err    error
}

func (m *mockRunner) RunSubagent(_ context.Context, req subagents.RunRequest) (subagents.Result, error) {
	m.called = true
	m.result.Subagent = req.Target
	return m.result, m.err
}

func TestACPRunner_FallbackForUnknownTarget(t *testing.T) {
	fallback := &mockRunner{result: subagents.Result{Output: "fallback-output"}}
	runner := NewACPRunner(ACPRunnerConfig{
		Agents: map[string]ACPAgentConfig{
			"claude": {Command: "claude"},
		},
	}, fallback)

	ctx := context.Background()
	result, err := runner.RunSubagent(ctx, subagents.RunRequest{
		Target:      "some-internal-agent",
		Instruction: "do something",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fallback.called {
		t.Fatal("expected fallback runner to be called")
	}
	if result.Output != "fallback-output" {
		t.Fatalf("expected fallback output, got %q", result.Output)
	}
}

func TestACPRunner_NoFallbackReturnsError(t *testing.T) {
	runner := NewACPRunner(ACPRunnerConfig{
		Agents: map[string]ACPAgentConfig{
			"claude": {Command: "claude"},
		},
	}, nil)

	ctx := context.Background()
	_, err := runner.RunSubagent(ctx, subagents.RunRequest{
		Target:      "unknown-target",
		Instruction: "do something",
	})
	if err == nil {
		t.Fatal("expected error for unknown target with nil fallback")
	}
}

func TestACPRunner_EmptyConfigReturnsFallback(t *testing.T) {
	fallback := &mockRunner{result: subagents.Result{Output: "direct"}}
	runner := NewACPRunner(ACPRunnerConfig{}, fallback)

	// When config is empty, NewACPRunner returns the fallback directly.
	ctx := context.Background()
	result, err := runner.RunSubagent(ctx, subagents.RunRequest{
		Target:      "anything",
		Instruction: "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "direct" {
		t.Fatalf("expected direct fallback output, got %q", result.Output)
	}
}

func TestACPRunner_ACPTargetFailsWithBadCommand(t *testing.T) {
	runner := NewACPRunner(ACPRunnerConfig{
		Agents: map[string]ACPAgentConfig{
			"bad-agent": {Command: "/nonexistent/agent/binary"},
		},
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := runner.RunSubagent(ctx, subagents.RunRequest{
		Target:      "bad-agent",
		Instruction: "hello",
	})
	if err == nil {
		t.Fatal("expected error for bad command")
	}
	if result.Subagent != "bad-agent" {
		t.Fatalf("expected subagent=%q, got %q", "bad-agent", result.Subagent)
	}
	if result.Metadata["runtime"] != "acp" {
		t.Fatal("expected runtime=acp in metadata")
	}
}
