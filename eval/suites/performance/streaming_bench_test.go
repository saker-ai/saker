package performance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/model"
)

// delayModel simulates streaming with configurable per-token delay.
type delayModel struct {
	tokenDelay time.Duration
	tokens     int
}

func (d delayModel) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	time.Sleep(d.tokenDelay * time.Duration(d.tokens))
	return &model.Response{
		Message: model.Message{Role: "assistant", Content: "done"},
	}, nil
}

func (d delayModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	for i := 0; i < d.tokens; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		time.Sleep(d.tokenDelay)
		if cb != nil {
			if err := cb(model.StreamResult{
				Delta: fmt.Sprintf("token-%d ", i),
				Final: i == d.tokens-1,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func BenchmarkStreaming_TTFT(b *testing.B) {
	m := delayModel{tokenDelay: 10 * time.Microsecond, tokens: 100}
	req := model.Request{
		Messages: []model.Message{{Role: "user", Content: "bench"}},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var firstTokenTime time.Duration
		start := time.Now()
		first := true
		_ = m.CompleteStream(context.Background(), req, func(sr model.StreamResult) error {
			if first {
				firstTokenTime = time.Since(start)
				first = false
			}
			return nil
		})
		_ = firstTokenTime // prevent elision
	}
}

func BenchmarkStreaming_Throughput(b *testing.B) {
	m := delayModel{tokenDelay: 1 * time.Microsecond, tokens: 1000}
	req := model.Request{
		Messages: []model.Message{{Role: "user", Content: "bench"}},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokenCount := 0
		_ = m.CompleteStream(context.Background(), req, func(sr model.StreamResult) error {
			tokenCount++
			return nil
		})
		b.ReportMetric(float64(tokenCount), "tokens/op")
	}
}

func BenchmarkStreaming_Alloc(b *testing.B) {
	m := delayModel{tokenDelay: 0, tokens: 500}
	req := model.Request{
		Messages: []model.Message{{Role: "user", Content: "bench"}},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.CompleteStream(context.Background(), req, func(sr model.StreamResult) error {
			return nil
		})
	}
}
