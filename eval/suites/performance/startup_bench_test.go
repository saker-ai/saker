package performance

import (
	"context"
	"fmt"
	"testing"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/tool"
)

// dummyTool is a minimal tool for registry scaling tests.
type dummyTool struct {
	name string
}

func (d dummyTool) Name() string             { return d.name }
func (d dummyTool) Description() string      { return "dummy tool " + d.name }
func (d dummyTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (d dummyTool) Execute(_ context.Context, _ map[string]interface{}) (*tool.ToolResult, error) {
	return &tool.ToolResult{Output: "ok"}, nil
}

func makeDummyTools(n int) []tool.Tool {
	tools := make([]tool.Tool, n)
	for i := range tools {
		tools[i] = dummyTool{name: fmt.Sprintf("tool-%d", i)}
	}
	return tools
}

func BenchmarkStartup_0Tools(b *testing.B) {
	benchStartup(b, 0)
}

func BenchmarkStartup_10Tools(b *testing.B) {
	benchStartup(b, 10)
}

func BenchmarkStartup_50Tools(b *testing.B) {
	benchStartup(b, 50)
}

func BenchmarkStartup_100Tools(b *testing.B) {
	benchStartup(b, 100)
}

func benchStartup(b *testing.B, toolCount int) {
	tools := makeDummyTools(toolCount)
	root := b.TempDir()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rt, err := api.New(context.Background(), api.Options{
			ProjectRoot:         root,
			Model:               noopModel{},
			EnabledBuiltinTools: []string{},
			CustomTools:         tools,
		})
		if err != nil {
			b.Fatal(err)
		}
		_ = rt.Close()
	}
}

func BenchmarkToolRegistryLookup(b *testing.B) {
	reg := tool.NewRegistry()
	for _, t := range makeDummyTools(100) {
		reg.Register(t)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := fmt.Sprintf("tool-%d", i%100)
		_, _ = reg.Get(name)
	}
}
