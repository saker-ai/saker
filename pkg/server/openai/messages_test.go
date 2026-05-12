package openai

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/model"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestResolveModelTier(t *testing.T) {
	cases := []struct {
		in   string
		want api.ModelTier
	}{
		{"saker-low", api.ModelTierLow},
		{"saker-mid", api.ModelTierMid},
		{"saker-high", api.ModelTierHigh},
		{"  Saker-Mid  ", api.ModelTierMid},
		{"saker-default", ""},
		{"gpt-4o", ""},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := ResolveModelTier(c.in); got != c.want {
				t.Errorf("ResolveModelTier(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMessagesToRequest_EmptyError(t *testing.T) {
	_, err := MessagesToRequest(nil, ExtraBody{}, "")
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestMessagesToRequest_SimpleUser(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: mustJSON(t, "Hello, saker.")},
	}
	got, err := MessagesToRequest(msgs, ExtraBody{}, api.ModelTierMid)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.Prompt != "Hello, saker." {
		t.Errorf("Prompt = %q, want %q", got.Prompt, "Hello, saker.")
	}
	if !got.Ephemeral {
		t.Error("Ephemeral must be true to prevent saker from double-writing history")
	}
	if got.Model != api.ModelTierMid {
		t.Errorf("Model = %q, want %q", got.Model, api.ModelTierMid)
	}
	if got.Tags["openai_gateway"] != "1" {
		t.Errorf("Tags[openai_gateway] missing/wrong: %v", got.Tags)
	}
}

func TestMessagesToRequest_SystemPrepended(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: mustJSON(t, "Be concise.")},
		{Role: "user", Content: mustJSON(t, "Hi")},
	}
	got, err := MessagesToRequest(msgs, ExtraBody{}, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.HasPrefix(got.Prompt, "Be concise.") {
		t.Errorf("expected system text to prefix prompt, got %q", got.Prompt)
	}
	if !strings.Contains(got.Prompt, "Hi") {
		t.Errorf("expected user text in prompt, got %q", got.Prompt)
	}
}

func TestMessagesToRequest_MultiTurnFolding(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: mustJSON(t, "first")},
		{Role: "assistant", Content: mustJSON(t, "ack")},
		{Role: "user", Content: mustJSON(t, "second")},
	}
	got, err := MessagesToRequest(msgs, ExtraBody{}, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(got.Prompt, "first") {
		t.Errorf("expected history to include first user turn: %q", got.Prompt)
	}
	if !strings.Contains(got.Prompt, "ack") {
		t.Errorf("expected history to include assistant turn: %q", got.Prompt)
	}
	if !strings.HasSuffix(got.Prompt, "second") {
		t.Errorf("expected latest user turn at the end: %q", got.Prompt)
	}
}

func TestMessagesToRequest_ToolMessage(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: mustJSON(t, "search bugs")},
		{Role: "assistant", ToolCalls: []ChatToolCall{
			{ID: "call_1", Type: "function", Function: ChatToolCallInvocation{Name: "search", Arguments: `{"q":"bugs"}`}},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: mustJSON(t, "found 3 bugs")},
		{Role: "user", Content: mustJSON(t, "ok thanks")},
	}
	got, err := MessagesToRequest(msgs, ExtraBody{}, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(got.Prompt, "tool result (call_1)") {
		t.Errorf("expected tool result label in prompt, got %q", got.Prompt)
	}
	if !strings.Contains(got.Prompt, `assistant invoked tool "search"`) {
		t.Errorf("expected assistant tool-call summary in prompt, got %q", got.Prompt)
	}
}

func TestMessagesToRequest_UnknownRole(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "alien", Content: mustJSON(t, "hi")},
	}
	_, err := MessagesToRequest(msgs, ExtraBody{}, "")
	if err == nil {
		t.Fatal("expected error for unknown role")
	}
	if !strings.Contains(err.Error(), "alien") {
		t.Errorf("error should name the role, got %q", err.Error())
	}
}

func TestMessagesToRequest_NoUserContent(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: mustJSON(t, "Be helpful.")},
	}
	_, err := MessagesToRequest(msgs, ExtraBody{}, "")
	if err == nil {
		t.Fatal("expected error when no user content present")
	}
}

func TestMessagesToRequest_SessionIDPropagates(t *testing.T) {
	msgs := []ChatMessage{{Role: "user", Content: mustJSON(t, "hi")}}
	got, err := MessagesToRequest(msgs, ExtraBody{SessionID: "sess_99"}, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.SessionID != "sess_99" {
		t.Errorf("SessionID = %q, want sess_99", got.SessionID)
	}
}

func TestMessagesToRequest_ImageDataURI(t *testing.T) {
	pixel := []byte{0x89, 0x50, 0x4e, 0x47}
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pixel)
	parts := []ContentPart{
		{Type: "text", Text: "look at this"},
		{Type: "image_url", ImageURL: &ContentImage{URL: dataURI}},
	}
	msgs := []ChatMessage{
		{Role: "user", Content: mustJSON(t, parts)},
	}
	got, err := MessagesToRequest(msgs, ExtraBody{}, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got.ContentBlocks) != 1 {
		t.Fatalf("ContentBlocks: got %d want 1", len(got.ContentBlocks))
	}
	b := got.ContentBlocks[0]
	if b.Type != model.ContentBlockImage {
		t.Errorf("block type = %q, want image", b.Type)
	}
	if b.MediaType != "image/png" {
		t.Errorf("media type = %q, want image/png", b.MediaType)
	}
	if b.Data == "" {
		t.Error("expected base64 data to be set")
	}
}

func TestMessagesToRequest_ImageURLBadScheme(t *testing.T) {
	parts := []ContentPart{
		{Type: "image_url", ImageURL: &ContentImage{URL: "ftp://example.com/x.png"}},
	}
	msgs := []ChatMessage{
		{Role: "user", Content: mustJSON(t, parts)},
	}
	_, err := MessagesToRequest(msgs, ExtraBody{}, "")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestExtractMessageText_StringAndParts(t *testing.T) {
	s, err := extractMessageText(mustJSON(t, "plain"))
	if err != nil || s != "plain" {
		t.Fatalf("string content: got (%q, %v), want plain", s, err)
	}
	s, err = extractMessageText(mustJSON(t, []ContentPart{{Type: "text", Text: "a"}, {Type: "text", Text: "b"}}))
	if err != nil {
		t.Fatalf("parts: unexpected err: %v", err)
	}
	if s != "a\nb" {
		t.Errorf("parts joined wrong: got %q want %q", s, "a\nb")
	}
}

func TestExtractMessageText_NullEmpty(t *testing.T) {
	s, err := extractMessageText(nil)
	if err != nil || s != "" {
		t.Errorf("nil content: got (%q, %v)", s, err)
	}
	s, err = extractMessageText(json.RawMessage(`null`))
	if err != nil || s != "" {
		t.Errorf("null content: got (%q, %v)", s, err)
	}
}

func TestExtractUserContent_StringPath(t *testing.T) {
	txt, blocks, err := extractUserContent(mustJSON(t, "hi"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if txt != "hi" {
		t.Errorf("text = %q, want hi", txt)
	}
	if len(blocks) != 0 {
		t.Errorf("expected no blocks for plain string, got %d", len(blocks))
	}
}

func TestExtractUserContent_UnknownPartIgnored(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: "hello"},
		{Type: "audio", Text: "ignored"},
	}
	txt, blocks, err := extractUserContent(mustJSON(t, parts))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if txt != "hello" {
		t.Errorf("text = %q, want hello", txt)
	}
	if len(blocks) != 0 {
		t.Errorf("audio part should be ignored, got %d blocks", len(blocks))
	}
}

func TestDecodeDataURI_ValidPNG(t *testing.T) {
	payload := []byte{1, 2, 3, 4}
	uri := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(payload)
	b, err := decodeDataURI(uri)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if b.MediaType != "image/jpeg" {
		t.Errorf("media type = %q, want image/jpeg", b.MediaType)
	}
	dec, err := base64.StdEncoding.DecodeString(b.Data)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if string(dec) != string(payload) {
		t.Errorf("payload roundtrip mismatch")
	}
}

func TestDecodeDataURI_Errors(t *testing.T) {
	cases := []struct {
		name, uri string
	}{
		{"missing prefix", "image/png;base64,abc"},
		{"missing comma", "data:image/png;base64"},
		{"not base64 flag", "data:image/png,abc"},
		{"bad base64", "data:image/png;base64,!!!"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := decodeDataURI(c.uri); err == nil {
				t.Errorf("expected error for %s", c.uri)
			}
		})
	}
}
