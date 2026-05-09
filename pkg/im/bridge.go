// Package im provides adapters that bridge the saker runtime to the goim
// IM gateway module.
package im

import (
	"context"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/model"
	"github.com/godeps/goim"
)

// RuntimeAdapter wraps an api.Runtime to implement goim.Runtime.
type RuntimeAdapter struct {
	rt *api.Runtime
}

// NewRuntimeAdapter creates a goim.Runtime from an saker api.Runtime.
func NewRuntimeAdapter(rt *api.Runtime) *RuntimeAdapter {
	return &RuntimeAdapter{rt: rt}
}

// RunStream converts goim types to api types and streams the response back.
func (a *RuntimeAdapter) RunStream(ctx context.Context, req goim.Request) (<-chan goim.StreamEvent, error) {
	apiReq := api.Request{
		Prompt:    req.Prompt,
		SessionID: req.SessionID,
	}

	// Convert content blocks.
	for _, cb := range req.ContentBlocks {
		apiReq.ContentBlocks = append(apiReq.ContentBlocks, model.ContentBlock{
			Type:      model.ContentBlockType(cb.Type),
			MediaType: cb.MediaType,
			Data:      cb.Data,
		})
	}

	apiStream, err := a.rt.RunStream(ctx, apiReq)
	if err != nil {
		return nil, err
	}

	// Convert api.StreamEvent -> goim.StreamEvent in a goroutine.
	out := make(chan goim.StreamEvent, 64)
	go func() {
		defer close(out)
		for evt := range apiStream {
			ge := goim.StreamEvent{
				Type:      string(evt.Type),
				Name:      evt.Name,
				Output:    evt.Output,
				SessionID: evt.SessionID,
			}
			if evt.Delta != nil {
				ge.Delta = &goim.Delta{
					Type: evt.Delta.Type,
					Text: evt.Delta.Text,
				}
			}
			select {
			case out <- ge:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}
