package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/security"
)

// MessagesToRequest folds OpenAI-style messages[] into a saker
// api.Request. The conversion is intentionally lossy in one direction
// (multi-turn chat history → single concatenated prompt) because the
// gateway forces Ephemeral=true so saker doesn't double-write the
// conversation to its own history table.
//
// extra carries the per-request ExtraBody (system_prompt_mode, etc).
// modelTier resolves the OpenAI model id ("saker-mid" / "saker-default")
// onto a saker ModelTier; an empty tier means "use saker default".
func MessagesToRequest(ctx context.Context, msgs []ChatMessage, extra ExtraBody, modelTier api.ModelTier) (api.Request, error) {
	if len(msgs) == 0 {
		return api.Request{}, errors.New("messages is empty")
	}

	systemMode := extra.EffectiveSystemPromptMode()
	var (
		systemTexts  []string
		userParts    []string
		blocks       []model.ContentBlock
		lastUserText string
	)

	for i, m := range msgs {
		switch strings.ToLower(strings.TrimSpace(m.Role)) {
		case "system", "developer":
			s, err := extractMessageText(m.Content)
			if err != nil {
				return api.Request{}, fmt.Errorf("messages[%d] (%s): %w", i, m.Role, err)
			}
			if s != "" {
				systemTexts = append(systemTexts, s)
			}
		case "user":
			s, parts, err := extractUserContent(ctx, m.Content)
			if err != nil {
				return api.Request{}, fmt.Errorf("messages[%d] (user): %w", i, err)
			}
			if s != "" {
				userParts = append(userParts, "user: "+s)
				lastUserText = s
			}
			blocks = append(blocks, parts...)
		case "assistant":
			s, err := extractMessageText(m.Content)
			if err != nil {
				// Tool calls without text content are valid — just skip
				// the text but keep iterating so subsequent tool messages
				// fold in.
				if len(m.ToolCalls) == 0 {
					return api.Request{}, fmt.Errorf("messages[%d] (assistant): %w", i, err)
				}
			}
			if s != "" {
				userParts = append(userParts, "assistant: "+s)
			}
			for _, tc := range m.ToolCalls {
				userParts = append(userParts, fmt.Sprintf("assistant invoked tool %q with %s", tc.Function.Name, tc.Function.Arguments))
			}
		case "tool", "function":
			s, _ := extractMessageText(m.Content)
			label := m.ToolCallID
			if label == "" {
				label = m.Name
			}
			if label == "" {
				label = "tool"
			}
			userParts = append(userParts, fmt.Sprintf("tool result (%s): %s", label, s))
		default:
			return api.Request{}, fmt.Errorf("messages[%d]: unknown role %q", i, m.Role)
		}
	}

	if lastUserText == "" && len(blocks) == 0 {
		return api.Request{}, errors.New("messages: no user content found")
	}

	prompt := lastUserText
	// When the conversation has more than one user-relevant turn, prepend
	// a brief history header so the agent sees prior context.
	if len(userParts) > 1 {
		// Drop the trailing "user: <lastUserText>" entry to avoid
		// duplicating it; the prompt itself already carries the latest.
		hist := userParts[:len(userParts)-1]
		prompt = strings.Join(hist, "\n") + "\n\n" + lastUserText
	}

	if len(systemTexts) > 0 {
		systemPrefix := strings.Join(systemTexts, "\n\n")
		switch systemMode {
		case SystemPromptReplace:
			// The replace contract is honored at the persona layer in
			// future work; for the MVP we still concatenate, but mark it
			// so a follow-up patch knows where to branch.
			prompt = systemPrefix + "\n\n" + prompt
		case SystemPromptPrepend:
			prompt = systemPrefix + "\n\n" + prompt
		}
	}

	req := api.Request{
		Prompt:        prompt,
		ContentBlocks: blocks,
		Ephemeral:     true,
		Model:         modelTier,
		Mode: api.ModeContext{
			EntryPoint: api.EntryPointPlatform,
		},
		Tags: map[string]string{
			"openai_gateway": "1",
		},
	}
	if extra.SessionID != "" {
		req.SessionID = extra.SessionID
	}
	if len(extra.AllowedTools) > 0 {
		req.ToolWhitelist = extra.AllowedTools
	}
	return req, nil
}

// extractMessageText returns the plain-text content of a message. Both
// "string content" and "[{type:text,text:...}]" shapes are supported;
// other content types are ignored.
func extractMessageText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	// Try parts array.
	var parts []ContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", fmt.Errorf("content: expected string or parts array")
	}
	var b strings.Builder
	for _, p := range parts {
		if strings.EqualFold(p.Type, "text") {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(p.Text)
		}
	}
	return b.String(), nil
}

// extractUserContent returns both the text portion and any image blocks
// for a user message. Image URL parts that are http(s) are downloaded
// once and inlined; data: URIs are decoded inline. Non-image, non-text
// parts are silently dropped (forward-compat for new content types).
func extractUserContent(ctx context.Context, raw json.RawMessage) (string, []model.ContentBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil, nil
	}
	var parts []ContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil, fmt.Errorf("content: expected string or parts array")
	}
	var (
		txt    strings.Builder
		blocks []model.ContentBlock
	)
	for _, p := range parts {
		switch strings.ToLower(strings.TrimSpace(p.Type)) {
		case "text":
			if txt.Len() > 0 {
				txt.WriteByte('\n')
			}
			txt.WriteString(p.Text)
		case "image_url", "input_image":
			if p.ImageURL == nil || p.ImageURL.URL == "" {
				continue
			}
			block, err := imageURLToBlock(ctx, p.ImageURL.URL)
			if err != nil {
				return "", nil, fmt.Errorf("image_url: %w", err)
			}
			blocks = append(blocks, block)
		default:
			// Forward-compat: ignore unknown types.
		}
	}
	return txt.String(), blocks, nil
}

// imageURLToBlock turns either a data: URI or an http(s) URL into a
// model.ContentBlock with base64-inlined data.
func imageURLToBlock(ctx context.Context, u string) (model.ContentBlock, error) {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "data:") {
		return decodeDataURI(u)
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return model.ContentBlock{}, fmt.Errorf("unsupported image URL scheme: %s", u)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return model.ContentBlock{}, fmt.Errorf("invalid image URL: %w", err)
	}
	host := parsed.Host
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	result, err := security.CheckSSRF(ctx, host)
	if err != nil {
		return model.ContentBlock{}, fmt.Errorf("image fetch blocked: %w", err)
	}
	transport := &http.Transport{
		DialContext: security.NewSSRFSafeDialer(result, port),
	}
	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return model.ContentBlock{}, fmt.Errorf("image request: %w", err)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return model.ContentBlock{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return model.ContentBlock{}, fmt.Errorf("fetch %s: status %d", u, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return model.ContentBlock{}, err
	}
	mt := resp.Header.Get("Content-Type")
	if mt == "" {
		mt = http.DetectContentType(data)
	}
	if !strings.HasPrefix(mt, "image/") {
		return model.ContentBlock{}, fmt.Errorf("fetch %s: not an image (Content-Type: %s)", u, mt)
	}
	return model.ContentBlock{
		Type:      model.ContentBlockImage,
		MediaType: mt,
		Data:      base64.StdEncoding.EncodeToString(data),
	}, nil
}

// decodeDataURI parses "data:<media>;base64,<payload>" into a content block.
func decodeDataURI(u string) (model.ContentBlock, error) {
	const prefix = "data:"
	if !strings.HasPrefix(u, prefix) {
		return model.ContentBlock{}, errors.New("not a data URI")
	}
	rest := u[len(prefix):]
	commaIdx := strings.Index(rest, ",")
	if commaIdx < 0 {
		return model.ContentBlock{}, errors.New("malformed data URI: missing comma")
	}
	meta := rest[:commaIdx]
	payload := rest[commaIdx+1:]
	parts := strings.Split(meta, ";")
	mediaType := parts[0]
	if mediaType == "" {
		mediaType = "image/png"
	}
	isBase64 := false
	for _, p := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(p), "base64") {
			isBase64 = true
		}
	}
	if !isBase64 {
		return model.ContentBlock{}, errors.New("data URI must be base64-encoded")
	}
	// Validate the base64 by decoding once; we re-encode below to
	// normalize whitespace / line breaks.
	dec, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		dec, err = base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			return model.ContentBlock{}, fmt.Errorf("base64: %w", err)
		}
	}
	return model.ContentBlock{
		Type:      model.ContentBlockImage,
		MediaType: mediaType,
		Data:      base64.StdEncoding.EncodeToString(dec),
	}, nil
}

// ResolveModelTier maps an OpenAI-style model id (the kind we emit from
// /v1/models) onto a saker tier. Unknown names → "" so saker uses its
// default.
func ResolveModelTier(modelID string) api.ModelTier {
	switch strings.ToLower(strings.TrimSpace(modelID)) {
	case "saker-low":
		return api.ModelTierLow
	case "saker-mid":
		return api.ModelTierMid
	case "saker-high":
		return api.ModelTierHigh
	default:
		return ""
	}
}
