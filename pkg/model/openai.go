package model

import (
	"context"
)

// openai.go retains the public Complete entrypoint. The implementation has
// been split into focused sibling files:
//   - openai_client.go:   OpenAIConfig, openaiModel, NewOpenAI, retry, options
//   - openai_request.go:  Request → openai.ChatCompletionNewParams conversion
//   - openai_response.go: openai.ChatCompletion → provider-neutral Response
//   - openai_stream.go:   CompleteStream and streaming chunk accumulation

// Complete issues a non-streaming completion.
func (m *openaiModel) Complete(ctx context.Context, req Request) (*Response, error) {
	recordModelRequest(ctx, req)
	var resp *Response
	err := m.doWithRetry(ctx, func(ctx context.Context) error {
		params, err := m.buildParams(req)
		if err != nil {
			return err
		}

		completion, err := m.completions.New(ctx, params, m.extraBodyOpts()...)
		if err != nil {
			return err
		}

		resp = convertOpenAIResponse(completion)
		recordModelResponse(ctx, resp)
		return nil
	})
	return resp, err
}
