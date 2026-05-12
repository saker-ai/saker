package model

import "context"

// mockModel is the shared test stub for any *_test.go in this package that
// needs a configurable Model. completeF / streamF override default behaviour;
// when nil they emit a deterministic "ok from <name>" response.
type mockModel struct {
	name      string
	completeF func(ctx context.Context, req Request) (*Response, error)
	streamF   func(ctx context.Context, req Request, cb StreamHandler) error
	ctxWindow int
}

func (m *mockModel) Complete(ctx context.Context, req Request) (*Response, error) {
	if m.completeF != nil {
		return m.completeF(ctx, req)
	}
	return &Response{Message: Message{Content: "ok from " + m.name}}, nil
}

func (m *mockModel) CompleteStream(ctx context.Context, req Request, cb StreamHandler) error {
	if m.streamF != nil {
		return m.streamF(ctx, req, cb)
	}
	return cb(StreamResult{Delta: "ok from " + m.name, Final: true, Response: &Response{}})
}

func (m *mockModel) ModelName() string  { return m.name }
func (m *mockModel) ContextWindow() int { return m.ctxWindow }
