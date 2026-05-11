package tool

import (
	"context"
	"fmt"
	"testing"
)

// stubBenchTool is a minimal Tool implementation used by bench tests. We avoid
// the real builtin tools so the benchmark stays focused on Registry operations
// (map lookups, mutex contention, sort) rather than tool internals.
type stubBenchTool struct {
	name string
}

func (t *stubBenchTool) Name() string                                                { return t.name }
func (t *stubBenchTool) Description() string                                         { return "bench stub" }
func (t *stubBenchTool) Schema() *JSONSchema                                         { return nil }
func (t *stubBenchTool) Execute(_ context.Context, _ map[string]interface{}) (*ToolResult, error) {
	return &ToolResult{Output: "ok"}, nil
}

var benchToolSink Tool

// BenchmarkRegistryRegister measures the cost of registering tools into a
// fresh registry. Each iteration creates a new registry to avoid the duplicate
// detection short-circuit and to keep allocations comparable.
func BenchmarkRegistryRegister(b *testing.B) {
	tools := make([]Tool, 64)
	for i := range tools {
		tools[i] = &stubBenchTool{name: fmt.Sprintf("tool-%d", i)}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := NewRegistry()
		for _, t := range tools {
			_ = r.Register(t)
		}
	}
}

// BenchmarkRegistryGet measures the lookup hot path. Hot paths in the agent
// loop call Get() once per tool invocation, so this should stay O(1) and
// allocation-free.
func BenchmarkRegistryGet(b *testing.B) {
	r := NewRegistry()
	const n = 64
	names := make([]string, n)
	for i := 0; i < n; i++ {
		names[i] = fmt.Sprintf("tool-%d", i)
		_ = r.Register(&stubBenchTool{name: names[i]})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t, _ := r.Get(names[i&(n-1)])
		benchToolSink = t
	}
}

// BenchmarkRegistryList measures the snapshot+sort path used when the runtime
// builds the tool catalog for a model request.
func BenchmarkRegistryList(b *testing.B) {
	r := NewRegistry()
	for i := 0; i < 64; i++ {
		_ = r.Register(&stubBenchTool{name: fmt.Sprintf("tool-%d", i)})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tools := r.List()
		if len(tools) > 0 {
			benchToolSink = tools[0]
		}
	}
}
