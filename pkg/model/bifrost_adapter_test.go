// bifrost_adapter_test.go: unit tests for the Bifrost adapter's conversion
// layer (saker ↔ Bifrost type mapping). End-to-end HTTP-level coverage lives
// in Phase 2 integration tests once SAKER_USE_BIFROST is wired into the real
// providers.
package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBifrostBuildBifrostMessages_BasicShapes(t *testing.T) {
	req := Request{
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
	}
	msgs := buildBifrostMessages(req, "you are a helper")
	require.Len(t, msgs, 3)
	assert.Equal(t, schemas.ChatMessageRoleSystem, msgs[0].Role)
	require.NotNil(t, msgs[0].Content)
	require.NotNil(t, msgs[0].Content.ContentStr)
	assert.Equal(t, "you are a helper", *msgs[0].Content.ContentStr)
	assert.Equal(t, schemas.ChatMessageRoleUser, msgs[1].Role)
	assert.Equal(t, schemas.ChatMessageRoleAssistant, msgs[2].Role)
}

func TestBifrostBuildBifrostMessages_SystemBlocksTakePrecedence(t *testing.T) {
	req := Request{
		System:       "should-be-ignored",
		SystemBlocks: []string{"static-prelude", "dynamic-context"},
		Messages: []Message{
			{Role: "user", Content: "hello"},
		},
	}
	msgs := buildBifrostMessages(req, "")
	// 2 system + 1 user
	require.Len(t, msgs, 3)
	assert.Equal(t, schemas.ChatMessageRoleSystem, msgs[0].Role)
	assert.Equal(t, "static-prelude", *msgs[0].Content.ContentStr)
	assert.Equal(t, schemas.ChatMessageRoleSystem, msgs[1].Role)
	assert.Equal(t, "dynamic-context", *msgs[1].Content.ContentStr)
}

func TestBifrostBuildBifrostMessages_EmptyMessagesGetsDot(t *testing.T) {
	msgs := buildBifrostMessages(Request{}, "")
	require.Len(t, msgs, 1)
	assert.Equal(t, schemas.ChatMessageRoleUser, msgs[0].Role)
	assert.Equal(t, ".", *msgs[0].Content.ContentStr)
}

func TestBifrostBuildBifrostMessages_AssistantWithToolCalls(t *testing.T) {
	req := Request{
		Messages: []Message{
			{
				Role:             "assistant",
				Content:          "calling tool",
				ReasoningContent: "I should call the weather tool",
				ToolCalls: []ToolCall{
					{ID: "call_1", Name: "get_weather", Arguments: map[string]any{"city": "Tokyo"}},
				},
			},
		},
	}
	msgs := buildBifrostMessages(req, "")
	require.Len(t, msgs, 1)
	m := msgs[0]
	assert.Equal(t, schemas.ChatMessageRoleAssistant, m.Role)
	require.NotNil(t, m.Content)
	require.NotNil(t, m.Content.ContentStr)
	assert.Equal(t, "calling tool", *m.Content.ContentStr)
	require.NotNil(t, m.ChatAssistantMessage)
	require.NotNil(t, m.ChatAssistantMessage.Reasoning)
	assert.Equal(t, "I should call the weather tool", *m.ChatAssistantMessage.Reasoning)
	require.Len(t, m.ChatAssistantMessage.ToolCalls, 1)
	tc := m.ChatAssistantMessage.ToolCalls[0]
	require.NotNil(t, tc.ID)
	assert.Equal(t, "call_1", *tc.ID)
	require.NotNil(t, tc.Function.Name)
	assert.Equal(t, "get_weather", *tc.Function.Name)
	assert.Contains(t, tc.Function.Arguments, "Tokyo")
}

func TestBifrostBuildBifrostMessages_ToolResultEmitsToolRole(t *testing.T) {
	req := Request{
		Messages: []Message{
			{
				Role: "tool",
				ToolCalls: []ToolCall{
					{ID: "call_1", Result: `{"temp":72}`},
					{ID: "call_2", Result: `{"temp":80}`},
				},
			},
		},
	}
	msgs := buildBifrostMessages(req, "")
	require.Len(t, msgs, 2)
	for i, want := range []string{"call_1", "call_2"} {
		assert.Equal(t, schemas.ChatMessageRoleTool, msgs[i].Role)
		require.NotNil(t, msgs[i].ChatToolMessage)
		require.NotNil(t, msgs[i].ChatToolMessage.ToolCallID)
		assert.Equal(t, want, *msgs[i].ChatToolMessage.ToolCallID)
	}
}

func TestBifrostBuildBifrostMessages_MultimodalUserBlocks(t *testing.T) {
	req := Request{
		Messages: []Message{
			{
				Role:    "user",
				Content: "look at this:",
				ContentBlocks: []ContentBlock{
					{Type: ContentBlockImage, URL: "https://example.com/cat.jpg"},
					{Type: ContentBlockImage, MediaType: "image/png", Data: "BASE64DATA"},
					{Type: ContentBlockDocument, MediaType: "application/pdf", Data: "PDFBASE64"},
				},
			},
		},
	}
	msgs := buildBifrostMessages(req, "")
	require.Len(t, msgs, 1)
	require.NotNil(t, msgs[0].Content)
	blocks := msgs[0].Content.ContentBlocks
	require.Len(t, blocks, 4)
	// Block 0: leading text
	assert.Equal(t, schemas.ChatContentBlockTypeText, blocks[0].Type)
	require.NotNil(t, blocks[0].Text)
	assert.Equal(t, "look at this:", *blocks[0].Text)
	// Block 1: URL image
	assert.Equal(t, schemas.ChatContentBlockTypeImage, blocks[1].Type)
	require.NotNil(t, blocks[1].ImageURLStruct)
	assert.Equal(t, "https://example.com/cat.jpg", blocks[1].ImageURLStruct.URL)
	// Block 2: base64 image
	assert.Equal(t, schemas.ChatContentBlockTypeImage, blocks[2].Type)
	require.NotNil(t, blocks[2].ImageURLStruct)
	assert.True(t, strings.HasPrefix(blocks[2].ImageURLStruct.URL, "data:image/png;base64,"))
	// Block 3: PDF document
	assert.Equal(t, schemas.ChatContentBlockTypeFile, blocks[3].Type)
	require.NotNil(t, blocks[3].File)
	require.NotNil(t, blocks[3].File.FileData)
	assert.Equal(t, "PDFBASE64", *blocks[3].File.FileData)
	require.NotNil(t, blocks[3].File.FileType)
	assert.Equal(t, "application/pdf", *blocks[3].File.FileType)
}

func TestBifrostApplyCacheControl_MarksFirstAndLastSystem(t *testing.T) {
	a, b, c := "a", "b", "c"
	msgs := []schemas.ChatMessage{
		{Role: schemas.ChatMessageRoleSystem, Content: &schemas.ChatMessageContent{ContentStr: &a}},
		{Role: schemas.ChatMessageRoleSystem, Content: &schemas.ChatMessageContent{ContentStr: &b}},
		{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: &c}},
	}
	applyBifrostCacheControl(msgs)
	// system[0] and system[1] should both be marked
	for i := 0; i < 2; i++ {
		require.NotNil(t, msgs[i].Content)
		require.Len(t, msgs[i].Content.ContentBlocks, 1)
		require.NotNil(t, msgs[i].Content.ContentBlocks[0].CacheControl)
		assert.Equal(t, schemas.CacheControlTypeEphemeral, msgs[i].Content.ContentBlocks[0].CacheControl.Type)
	}
}

func TestBifrostApplyCacheControl_MarksLastThreeUserMessages(t *testing.T) {
	build := func(s string) schemas.ChatMessage {
		v := s
		return schemas.ChatMessage{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: &v},
		}
	}
	msgs := []schemas.ChatMessage{
		build("u1"), build("u2"), build("u3"), build("u4"),
	}
	applyBifrostCacheControl(msgs)
	// First user (u1) should NOT be marked (only last 3)
	require.NotNil(t, msgs[0].Content)
	require.NotNil(t, msgs[0].Content.ContentStr)
	assert.Equal(t, "u1", *msgs[0].Content.ContentStr)
	assert.Empty(t, msgs[0].Content.ContentBlocks)
	// Last three (u2, u3, u4) should each have ephemeral marker
	for i := 1; i < 4; i++ {
		require.Len(t, msgs[i].Content.ContentBlocks, 1, "message %d", i)
		require.NotNil(t, msgs[i].Content.ContentBlocks[0].CacheControl)
		assert.Equal(t, schemas.CacheControlTypeEphemeral, msgs[i].Content.ContentBlocks[0].CacheControl.Type)
	}
}

func TestBifrostBuildTools_EmptyAndPopulated(t *testing.T) {
	tools := buildBifrostTools(nil)
	assert.Empty(t, tools)

	tools = buildBifrostTools([]ToolDefinition{
		{
			Name:        "get_weather",
			Description: "Look up weather",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
				"required": []any{"city"},
			},
		},
		{Name: ""}, // dropped
	})
	require.Len(t, tools, 1)
	assert.Equal(t, schemas.ChatToolTypeFunction, tools[0].Type)
	require.NotNil(t, tools[0].Function)
	assert.Equal(t, "get_weather", tools[0].Function.Name)
	require.NotNil(t, tools[0].Function.Description)
	assert.Equal(t, "Look up weather", *tools[0].Function.Description)
	require.NotNil(t, tools[0].Function.Parameters)
	assert.Equal(t, "object", tools[0].Function.Parameters.Type)
	require.NotNil(t, tools[0].Function.Parameters.Properties)
	assert.Equal(t, []string{"city"}, tools[0].Function.Parameters.Required)
}

func TestBifrostConvertMessage_NonStreamShapes(t *testing.T) {
	text := "hello"
	reasoning := "thought"
	id := "call_1"
	name := "get_weather"
	chatMsg := &schemas.ChatMessage{
		Role:    schemas.ChatMessageRoleAssistant,
		Content: &schemas.ChatMessageContent{ContentStr: &text},
		ChatAssistantMessage: &schemas.ChatAssistantMessage{
			Reasoning: &reasoning,
			ToolCalls: []schemas.ChatAssistantMessageToolCall{
				{
					Index: 0,
					ID:    &id,
					Function: schemas.ChatAssistantMessageToolCallFunction{
						Name:      &name,
						Arguments: `{"city":"Paris"}`,
					},
				},
			},
		},
	}
	msg := convertBifrostMessage(chatMsg)
	assert.Equal(t, "assistant", msg.Role)
	assert.Equal(t, "hello", msg.Content)
	assert.Equal(t, "thought", msg.ReasoningContent)
	require.Len(t, msg.ToolCalls, 1)
	assert.Equal(t, "call_1", msg.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", msg.ToolCalls[0].Name)
	assert.Equal(t, "Paris", msg.ToolCalls[0].Arguments["city"])
}

func TestBifrostConvertMessage_NilSafe(t *testing.T) {
	msg := convertBifrostMessage(nil)
	assert.Equal(t, "assistant", msg.Role)
	assert.Empty(t, msg.Content)
}

func TestBifrostConvertUsage(t *testing.T) {
	got := convertBifrostUsage(&schemas.BifrostLLMUsage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
		PromptTokensDetails: &schemas.ChatPromptTokensDetails{
			CachedReadTokens:  30,
			CachedWriteTokens: 10,
		},
	})
	assert.Equal(t, 100, got.InputTokens)
	assert.Equal(t, 50, got.OutputTokens)
	assert.Equal(t, 150, got.TotalTokens)
	assert.Equal(t, 30, got.CacheReadTokens)
	assert.Equal(t, 10, got.CacheCreationTokens)

	assert.Equal(t, Usage{}, convertBifrostUsage(nil))
}

func TestBifrostMapError_PreservesMessage(t *testing.T) {
	// Wrapping err takes precedence
	wrapped := errors.New("prompt is too long: tokens exceed limit")
	err := mapBifrostError(&schemas.BifrostError{
		Error: &schemas.ErrorField{Message: "fallback message", Error: wrapped},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompt is too long")

	// Falls back to Message string when Error is nil
	err = mapBifrostError(&schemas.BifrostError{
		Error: &schemas.ErrorField{Message: "rate limited"},
	})
	require.Error(t, err)
	assert.Equal(t, "rate limited", err.Error())

	// Nil → sentinel
	err = mapBifrostError(nil)
	require.Error(t, err)
}

func TestBifrostMapError_AttachesStatusCode(t *testing.T) {
	status := 429
	err := mapBifrostError(&schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "too many requests"},
	})
	require.Error(t, err)
	se, ok := err.(HTTPStatusError)
	require.True(t, ok, "expected HTTPStatusError")
	assert.Equal(t, 429, se.HTTPStatusCode())
	assert.Equal(t, "too many requests", err.Error())
}

func TestClassifyBifrostError_RoundTrip(t *testing.T) {
	status := 429
	c := ClassifyBifrostError(&schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "rate limit exceeded"},
	})
	assert.Equal(t, FailoverRateLimit, c.Reason)
	assert.Equal(t, 429, c.StatusCode)
	assert.True(t, c.Retryable)
	assert.True(t, c.ShouldFallback)
}

func TestBifrostToolCallAccumulator_AggregatesByIndex(t *testing.T) {
	id1, name1 := "call_a", "fn_a"
	id2, name2 := "call_b", "fn_b"
	acc := newBifrostToolCallAccumulator()
	// Two parallel tool calls, deltas interleaved.
	acc.Append(schemas.ChatAssistantMessageToolCall{
		Index: 0, ID: &id1,
		Function: schemas.ChatAssistantMessageToolCallFunction{Name: &name1, Arguments: `{"a":`},
	})
	acc.Append(schemas.ChatAssistantMessageToolCall{
		Index: 1, ID: &id2,
		Function: schemas.ChatAssistantMessageToolCallFunction{Name: &name2, Arguments: `{"b":`},
	})
	acc.Append(schemas.ChatAssistantMessageToolCall{
		Index:    0,
		Function: schemas.ChatAssistantMessageToolCallFunction{Arguments: `1}`},
	})
	acc.Append(schemas.ChatAssistantMessageToolCall{
		Index:    1,
		Function: schemas.ChatAssistantMessageToolCallFunction{Arguments: `2}`},
	})

	all := acc.All()
	require.Len(t, all, 2)
	got1 := all[0].toToolCall()
	require.NotNil(t, got1)
	assert.Equal(t, "call_a", got1.ID)
	assert.Equal(t, "fn_a", got1.Name)
	assert.EqualValues(t, 1, got1.Arguments["a"])

	got2 := all[1].toToolCall()
	require.NotNil(t, got2)
	assert.Equal(t, "call_b", got2.ID)
	assert.EqualValues(t, 2, got2.Arguments["b"])

	// Drain returns the same view (no clearing — callers track emission via map).
	drain := acc.Drain()
	require.Len(t, drain, 2)
}

func TestBifrostToolCall_DecodeMalformedArgs(t *testing.T) {
	// When args are non-JSON, decoder must still return a non-nil map so
	// downstream consumers don't panic.
	args := decodeToolArgs("not-json {{{")
	require.NotNil(t, args)
	assert.Equal(t, "not-json {{{", args["_raw"])

	args = decodeToolArgs("")
	require.NotNil(t, args)
	assert.Empty(t, args)
}

func TestNewBifrost_RequiresProviderModelKey(t *testing.T) {
	_, err := NewBifrost(BifrostConfig{ModelName: "x", APIKey: "k"})
	assert.Error(t, err, "provider required")

	_, err = NewBifrost(BifrostConfig{Provider: schemas.OpenAI, APIKey: "k"})
	assert.Error(t, err, "model required")

	_, err = NewBifrost(BifrostConfig{Provider: schemas.OpenAI, ModelName: "gpt"})
	assert.Error(t, err, "api key required")
}
