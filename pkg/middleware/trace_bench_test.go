package middleware

import (
	"context"
	"testing"
)

// BenchmarkTraceMiddleware_BeforeAgent benchmarks the cheapest hook (just
// records a stage event with the State pointer as the session id, since no
// session id is set on the State).
func BenchmarkTraceMiddleware_BeforeAgent(b *testing.B) {
	tm := NewTraceMiddleware(b.TempDir())
	defer tm.Close()
	ctx := context.Background()
	st := &State{Iteration: 1, Agent: "bench-agent"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tm.BeforeAgent(ctx, st)
	}
}

// BenchmarkTraceMiddleware_FullCycle exercises one complete agent turn:
// BeforeAgent → BeforeModel → AfterModel → BeforeTool → AfterTool → AfterAgent.
// This mirrors the per-iteration cost the runtime pays when tracing is on.
func BenchmarkTraceMiddleware_FullCycle(b *testing.B) {
	tm := NewTraceMiddleware(b.TempDir())
	defer tm.Close()
	ctx := context.Background()
	st := &State{
		Iteration:   1,
		Agent:       "bench-agent",
		ModelInput:  map[string]any{"prompt": "hello"},
		ModelOutput: map[string]any{"text": "world"},
		ToolCall:    map[string]any{"name": "echo"},
		ToolResult:  map[string]any{"output": "ok"},
		Values:      map[string]any{"session_id": "bench-session"},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tm.BeforeAgent(ctx, st)
		_ = tm.BeforeModel(ctx, st)
		_ = tm.AfterModel(ctx, st)
		_ = tm.BeforeTool(ctx, st)
		_ = tm.AfterTool(ctx, st)
		_ = tm.AfterAgent(ctx, st)
	}
}

// BenchmarkSanitizeSessionComponent stays in the hot path of session id
// resolution; verify it remains allocation-light.
func BenchmarkSanitizeSessionComponent(b *testing.B) {
	inputs := []string{
		"simple-session-id",
		"id with spaces and 漢字",
		"weird/path/like::name",
		"",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sanitizeSessionComponent(inputs[i&3])
	}
}
