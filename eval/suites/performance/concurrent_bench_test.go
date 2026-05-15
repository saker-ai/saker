package performance

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/pipeline"
	"github.com/saker-ai/saker/pkg/tool"
)

type noopModel struct{}

func (noopModel) Complete(context.Context, model.Request) (*model.Response, error) {
	return &model.Response{Message: model.Message{Role: "assistant"}}, nil
}
func (noopModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	resp, _ := noopModel{}.Complete(ctx, req)
	if cb != nil {
		return cb(model.StreamResult{Final: true, Response: resp})
	}
	return nil
}

type benchEchoTool struct{}

func (benchEchoTool) Name() string             { return "echo" }
func (benchEchoTool) Description() string      { return "echo" }
func (benchEchoTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (benchEchoTool) Execute(_ context.Context, p map[string]interface{}) (*tool.ToolResult, error) {
	text, _ := p["text"].(string)
	return &tool.ToolResult{Output: text}, nil
}

func newBenchRuntime(b *testing.B) *api.Runtime {
	b.Helper()
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:         b.TempDir(),
		Model:               noopModel{},
		EnabledBuiltinTools: []string{},
		CustomTools:         []tool.Tool{benchEchoTool{}},
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = rt.Close() })
	return rt
}

func BenchmarkConcurrentSessions_10(b *testing.B) {
	benchConcurrentSessions(b, 10)
}

func BenchmarkConcurrentSessions_50(b *testing.B) {
	benchConcurrentSessions(b, 50)
}

func BenchmarkConcurrentSessions_100(b *testing.B) {
	benchConcurrentSessions(b, 100)
}

func benchConcurrentSessions(b *testing.B, concurrency int) {
	rt := newBenchRuntime(b)
	step := pipeline.Step{Name: "echo", Tool: "echo", With: map[string]any{"text": "concurrent"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		wg.Add(concurrency)
		for j := 0; j < concurrency; j++ {
			j := j
			go func() {
				defer wg.Done()
				_, err := rt.Run(context.Background(), api.Request{
					SessionID: fmt.Sprintf("session-%d-%d", i, j),
					Pipeline:  &step,
				})
				if err != nil {
					b.Errorf("session %d: %v", j, err)
				}
			}()
		}
		wg.Wait()
	}
}
