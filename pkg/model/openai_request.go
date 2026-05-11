package model

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// openai_request.go converts the provider-neutral Request type into the
// concrete openai.ChatCompletionNewParams payload, including message,
// content-block, tool, and response-format conversion.

func (m *openaiModel) buildParams(req Request) (openai.ChatCompletionNewParams, error) {
	messages := convertMessagesToOpenAI(req.Messages, m.system, req.System)

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = m.maxTokens
	}

	modelName := m.selectModel(req.Model)

	params := openai.ChatCompletionNewParams{
		Model:               shared.ChatModel(modelName),
		MaxCompletionTokens: openai.Int(int64(maxTokens)),
		Messages:            messages,
	}

	if len(req.Tools) > 0 {
		tools := convertToolsToOpenAI(req.Tools)
		params.Tools = tools
	}

	if req.ResponseFormat != nil {
		responseFormat, err := buildChatCompletionResponseFormat(req.ResponseFormat)
		if err != nil {
			return params, err
		}
		params.ResponseFormat = responseFormat
	}

	if m.temperature != nil {
		params.Temperature = openai.Float(*m.temperature)
	}
	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}

	if sessionID := strings.TrimSpace(req.SessionID); sessionID != "" {
		params.User = openai.String(sessionID)
	}

	return params, nil
}

func buildChatCompletionResponseFormat(format *ResponseFormat) (openai.ChatCompletionNewParamsResponseFormatUnion, error) {
	if format == nil {
		return openai.ChatCompletionNewParamsResponseFormatUnion{}, nil
	}

	switch strings.TrimSpace(format.Type) {
	case "", "text":
		return openai.ChatCompletionNewParamsResponseFormatUnion{}, nil
	case "json_object":
		obj := shared.NewResponseFormatJSONObjectParam()
		return openai.ChatCompletionNewParamsResponseFormatUnion{OfJSONObject: &obj}, nil
	case "json_schema":
		schema, err := validateResponseFormatJSONSchema(format.JSONSchema)
		if err != nil {
			return openai.ChatCompletionNewParamsResponseFormatUnion{}, err
		}
		return openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: *schema,
			},
		}, nil
	default:
		return openai.ChatCompletionNewParamsResponseFormatUnion{}, nil
	}
}

func validateResponseFormatJSONSchema(schema *OutputJSONSchema) (*shared.ResponseFormatJSONSchemaJSONSchemaParam, error) {
	if schema == nil {
		return nil, errors.New("response format json_schema schema is required")
	}
	name := strings.TrimSpace(schema.Name)
	if name == "" {
		return nil, errors.New("response format json_schema name is required")
	}
	if schema.Schema == nil {
		return nil, errors.New("response format json_schema schema body is required")
	}

	out := &shared.ResponseFormatJSONSchemaJSONSchemaParam{
		Name:   name,
		Schema: schema.Schema,
	}
	if desc := strings.TrimSpace(schema.Description); desc != "" {
		out.Description = openai.String(desc)
	}
	out.Strict = openai.Bool(schema.Strict)
	return out, nil
}

func convertMessagesToOpenAI(msgs []Message, defaults ...string) []openai.ChatCompletionMessageParamUnion {
	var result []openai.ChatCompletionMessageParamUnion

	// Add system messages from defaults
	for _, sys := range defaults {
		if trimmed := strings.TrimSpace(sys); trimmed != "" {
			result = append(result, openai.SystemMessage(trimmed))
		}
	}

	for _, msg := range msgs {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system":
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				result = append(result, openai.SystemMessage(trimmed))
			}
		case "assistant":
			result = append(result, buildOpenAIAssistantMessage(msg))
		case "tool":
			result = append(result, buildOpenAIToolResults(msg)...)
		default: // user
			if len(msg.ContentBlocks) > 0 {
				userParam := openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfArrayOfContentParts: buildOpenAIUserContentParts(msg),
					},
				}
				result = append(result, openai.ChatCompletionMessageParamUnion{
					OfUser: &userParam,
				})
				continue
			}
			content := msg.Content
			if strings.TrimSpace(content) == "" {
				content = "."
			}
			result = append(result, openai.UserMessage(content))
		}
	}

	if len(result) == 0 {
		result = append(result, openai.UserMessage("."))
	}

	return result
}

func buildOpenAIUserContentParts(msg Message) []openai.ChatCompletionContentPartUnionParam {
	parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(msg.ContentBlocks)+1)
	if text := strings.TrimSpace(msg.Content); text != "" {
		parts = append(parts, openai.TextContentPart(text))
	}
	for _, block := range msg.ContentBlocks {
		switch block.Type {
		case ContentBlockText:
			if text := strings.TrimSpace(block.Text); text != "" {
				parts = append(parts, openai.TextContentPart(text))
			}
		case ContentBlockImage:
			if imageURL := openAIImageURL(block); imageURL != "" {
				parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
					URL: imageURL,
				}))
			}
		}
	}
	if len(parts) == 0 {
		parts = append(parts, openai.TextContentPart("."))
	}
	return parts
}

// openAIImageURL returns a URL or data-URI for the image content block.
// When MediaType is empty, it defaults to "image/jpeg".
func openAIImageURL(block ContentBlock) string {
	if url := strings.TrimSpace(block.URL); url != "" {
		return url
	}
	data := strings.TrimSpace(block.Data)
	if data == "" {
		return ""
	}
	mediaType := strings.TrimSpace(block.MediaType)
	if mediaType == "" {
		mediaType = "image/jpeg" // default when MediaType is unspecified
	}
	return "data:" + mediaType + ";base64," + data
}

func buildOpenAIAssistantMessage(msg Message) openai.ChatCompletionMessageParamUnion {
	assistantParam := openai.ChatCompletionAssistantMessageParam{}

	// Set content
	content := msg.Content
	if strings.TrimSpace(content) == "" {
		content = "."
	}
	assistantParam.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
		OfString: openai.String(content),
	}

	// Add tool calls if present
	if len(msg.ToolCalls) > 0 {
		var toolCalls []openai.ChatCompletionMessageToolCallParam
		for _, call := range msg.ToolCalls {
			id := strings.TrimSpace(call.ID)
			name := strings.TrimSpace(call.Name)
			if id == "" || name == "" {
				continue
			}

			args := "{}"
			if call.Arguments != nil {
				if argsJSON, err := json.Marshal(call.Arguments); err == nil {
					args = string(argsJSON)
				}
			}
			toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
				ID: id,
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      name,
					Arguments: args,
				},
			})
		}
		assistantParam.ToolCalls = toolCalls
	}

	// Pass through reasoning_content for thinking models
	if msg.ReasoningContent != "" {
		assistantParam.SetExtraFields(map[string]any{
			"reasoning_content": msg.ReasoningContent,
		})
	}

	return openai.ChatCompletionMessageParamUnion{
		OfAssistant: &assistantParam,
	}
}

func buildOpenAIToolResults(msg Message) []openai.ChatCompletionMessageParamUnion {
	if len(msg.ToolCalls) == 0 {
		return []openai.ChatCompletionMessageParamUnion{
			openai.ToolMessage(msg.Content, ""),
		}
	}

	var results []openai.ChatCompletionMessageParamUnion
	for _, call := range msg.ToolCalls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			continue
		}
		content := call.Result
		if strings.TrimSpace(content) == "" {
			content = msg.Content
		}
		results = append(results, openai.ToolMessage(content, id))
	}

	if len(results) == 0 {
		results = append(results, openai.ToolMessage(msg.Content, ""))
	}

	return results
}

func convertToolsToOpenAI(tools []ToolDefinition) []openai.ChatCompletionToolParam {
	var result []openai.ChatCompletionToolParam
	for _, def := range tools {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}

		tool := openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:       name,
				Parameters: convertToFunctionParameters(def.Parameters),
			},
		}
		if desc := strings.TrimSpace(def.Description); desc != "" {
			tool.Function.Description = openai.Opt(desc)
		}

		result = append(result, tool)
	}
	return result
}

func convertToFunctionParameters(params map[string]any) shared.FunctionParameters {
	if len(params) == 0 {
		return shared.FunctionParameters{
			"type": "object",
		}
	}

	// Ensure type is set
	result := make(shared.FunctionParameters, len(params)+1)
	for k, v := range params {
		result[k] = v
	}
	if _, ok := result["type"]; !ok {
		result["type"] = "object"
	}
	return result
}
