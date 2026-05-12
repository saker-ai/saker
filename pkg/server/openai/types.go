package openai

import "encoding/json"

// Minimal OpenAI Chat Completions wire types — defined locally so the
// gateway has zero external SDK dependency (no openai-go in go.mod).
// Only the fields saker actually reads or emits are modeled; unknown
// fields are passed through `Extra` so clients see the same JSON they
// sent (forward-compat, e.g. tool_choice).

// ChatRequest is the POST /v1/chat/completions body. Many fields are
// `any` because they're either passed through opaquely or only
// inspected for shape (e.g. content can be a string OR a content-blocks
// array).
type ChatRequest struct {
	Model            string         `json:"model"`
	Messages         []ChatMessage  `json:"messages"`
	Stream           bool           `json:"stream,omitempty"`
	StreamOptions    map[string]any `json:"stream_options,omitempty"`
	Temperature      *float64       `json:"temperature,omitempty"`
	TopP             *float64       `json:"top_p,omitempty"`
	N                int            `json:"n,omitempty"`
	Stop             any            `json:"stop,omitempty"`
	MaxTokens        int            `json:"max_tokens,omitempty"`
	MaxCompletionT   int            `json:"max_completion_tokens,omitempty"`
	PresencePenalty  *float64       `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64       `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]int `json:"logit_bias,omitempty"`
	User             string         `json:"user,omitempty"`
	Tools            []ChatTool     `json:"tools,omitempty"`
	ToolChoice       any            `json:"tool_choice,omitempty"`
	ResponseFormat   any            `json:"response_format,omitempty"`
	Seed             *int           `json:"seed,omitempty"`
	// ParallelToolCalls is the OpenAI standard knob for letting the model
	// emit multiple tool_use blocks in a single turn. nil = provider default.
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`

	// ExtraBody is the OpenAI Python SDK convention for forwarding
	// non-standard fields. The Go SDK uses `extra_body` directly in the
	// request struct; both reach us as a sub-object.
	ExtraBody map[string]any `json:"extra_body,omitempty"`
}

// ChatMessage models a single message in the messages[] array. Content
// can be either a plain string OR an array of content parts (text /
// image_url / input_image), so we keep it as raw JSON and parse on
// demand.
type ChatMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content,omitempty"`
	Name         string          `json:"name,omitempty"`
	ToolCalls    []ChatToolCall  `json:"tool_calls,omitempty"`
	ToolCallID   string          `json:"tool_call_id,omitempty"`
	FunctionCall any             `json:"function_call,omitempty"` // legacy
}

// ChatTool is the function-style tool definition.
type ChatTool struct {
	Type     string             `json:"type"` // "function"
	Function ChatToolDefinition `json:"function"`
}

// ChatToolDefinition is the inner OpenAI function-tool shape.
type ChatToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ChatToolCall is one tool call emitted by the assistant.
type ChatToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"` // "function"
	Function ChatToolCallInvocation `json:"function"`
}

// ChatToolCallInvocation carries the tool name and JSON-encoded arguments.
type ChatToolCallInvocation struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatCompletionResponse is the full (non-streaming) response envelope.
type ChatCompletionResponse struct {
	ID                string             `json:"id"`
	Object            string             `json:"object"` // "chat.completion"
	Created           int64              `json:"created"`
	Model             string             `json:"model"`
	Choices           []ChatChoice       `json:"choices"`
	Usage             *ChatUsage         `json:"usage,omitempty"`
	SystemFingerprint string             `json:"system_fingerprint,omitempty"`
	ServiceTier       string             `json:"service_tier,omitempty"`
	XSakerExtras      map[string]any     `json:"x_saker_extras,omitempty"`
	StreamOptions     map[string]any     `json:"-"`
	finishHook        func(*ChatChoice)  `json:"-"` // for tests
	_                 [0]json.RawMessage // prevent struct comparison
}

// ChatChoice is a single completion in the choices array.
type ChatChoice struct {
	Index        int             `json:"index"`
	Message      *ChatMessageOut `json:"message,omitempty"`
	Delta        *ChatMessageOut `json:"delta,omitempty"`
	FinishReason string          `json:"finish_reason,omitempty"`
	Logprobs     any             `json:"logprobs,omitempty"`
}

// ChatMessageOut is the assistant message shape used in non-stream
// responses and stream deltas. Mirrors ChatMessage but with content as
// a plain string (assistant output is never multimodal in saker today).
type ChatMessageOut struct {
	Role             string         `json:"role,omitempty"`
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ChatToolCall `json:"tool_calls,omitempty"`
	Refusal          string         `json:"refusal,omitempty"`
}

// ChatUsage is the token-accounting block. saker maps Anthropic-style
// usage onto these fields.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatCompletionChunk is the streaming response shape — same envelope
// as ChatCompletionResponse but with `object: "chat.completion.chunk"`
// and per-choice `delta` instead of `message`.
type ChatCompletionChunk struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"` // "chat.completion.chunk"
	Created           int64          `json:"created"`
	Model             string         `json:"model"`
	Choices           []ChatChoice   `json:"choices"`
	Usage             *ChatUsage     `json:"usage,omitempty"` // emitted only on the final chunk when stream_options.include_usage=true
	SystemFingerprint string         `json:"system_fingerprint,omitempty"`
	ServiceTier       string         `json:"service_tier,omitempty"`
	XSakerExtras      map[string]any `json:"x_saker_extras,omitempty"`
}

// ContentPart is one item in a multimodal content array. Used when
// ChatMessage.Content unmarshals to []ContentPart.
type ContentPart struct {
	Type     string          `json:"type"`               // "text" / "image_url" / "input_image"
	Text     string          `json:"text,omitempty"`     // type=text
	ImageURL *ContentImage   `json:"image_url,omitempty"` // type=image_url
	Image    json.RawMessage `json:"-"`                  // raw, for forward-compat
}

// ContentImage holds the image_url payload. URL can be a data: URI
// (base64-inlined) or a real http(s) URL.
type ContentImage struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}
