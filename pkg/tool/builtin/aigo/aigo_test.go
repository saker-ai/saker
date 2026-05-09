package aigo

import (
	"context"
	"testing"

	sdk "github.com/godeps/aigo"
	"github.com/godeps/aigo/engine"
	"github.com/godeps/aigo/tooldef"
	"github.com/godeps/aigo/workflow"

	"github.com/cinience/saker/pkg/config"
)

// mockEngine implements engine.Engine for testing.
type mockEngine struct {
	lastGraph workflow.Graph
	result    engine.Result
	err       error
}

func (m *mockEngine) Execute(_ context.Context, g workflow.Graph) (engine.Result, error) {
	m.lastGraph = g
	return m.result, m.err
}

func TestConvertSchema(t *testing.T) {
	def := tooldef.GenerateImage()
	js := convertSchema(def.Parameters)

	if js.Type != "object" {
		t.Fatalf("expected type object, got %s", js.Type)
	}
	if len(js.Properties) == 0 {
		t.Fatal("expected non-empty properties")
	}
	if _, ok := js.Properties["prompt"]; !ok {
		t.Fatal("expected prompt property")
	}
	if len(js.Required) == 0 || js.Required[0] != "prompt" {
		t.Fatal("expected prompt in required")
	}

	// Check that enum values are preserved for size property.
	sizeMap, ok := js.Properties["size"].(map[string]interface{})
	if !ok {
		t.Fatal("expected size to be a map")
	}
	enumVal, ok := sizeMap["enum"]
	if !ok {
		t.Fatal("expected enum in size property")
	}
	enums, ok := enumVal.([]interface{})
	if !ok || len(enums) == 0 {
		t.Fatal("expected non-empty enum slice")
	}
}

func TestSchemaToMap(t *testing.T) {
	s := tooldef.Schema{
		Type:        "string",
		Description: "test description",
		Enum:        []string{"a", "b"},
	}
	m := schemaToMap(s)
	if m["type"] != "string" {
		t.Fatalf("expected string type, got %v", m["type"])
	}
	if m["description"] != "test description" {
		t.Fatalf("expected test description, got %v", m["description"])
	}
	enums, ok := m["enum"].([]interface{})
	if !ok || len(enums) != 2 {
		t.Fatalf("expected 2 enum values, got %v", m["enum"])
	}
}

func TestBuildImageTask(t *testing.T) {
	params := map[string]interface{}{
		"prompt":          "a cat",
		"negative_prompt": "blurry",
		"size":            "1024x1024",
		"width":           float64(512),
		"height":          float64(512),
	}
	task := buildTask("generate_image", params)
	if task.Prompt != "a cat" {
		t.Fatalf("expected prompt 'a cat', got %q", task.Prompt)
	}
	if task.NegativePrompt != "blurry" {
		t.Fatalf("expected negative_prompt 'blurry', got %q", task.NegativePrompt)
	}
	if task.Size != "1024x1024" {
		t.Fatalf("expected size '1024x1024', got %q", task.Size)
	}
	if task.Width != 512 || task.Height != 512 {
		t.Fatalf("expected 512x512, got %dx%d", task.Width, task.Height)
	}
}

func TestBuildVideoTask(t *testing.T) {
	params := map[string]interface{}{
		"prompt":          "a sunset",
		"duration":        float64(5),
		"reference_image": "https://example.com/img.jpg",
	}
	task := buildTask("generate_video", params)
	if task.Prompt != "a sunset" {
		t.Fatalf("expected prompt 'a sunset', got %q", task.Prompt)
	}
	if task.Duration != 5 {
		t.Fatalf("expected duration 5, got %d", task.Duration)
	}
	if len(task.References) != 1 || task.References[0].URL != "https://example.com/img.jpg" {
		t.Fatalf("expected reference image, got %v", task.References)
	}
	if task.References[0].Type != sdk.ReferenceTypeImage {
		t.Fatalf("expected image reference type, got %v", task.References[0].Type)
	}
}

func TestBuildVideoTaskDeduplicatesReferenceImages(t *testing.T) {
	params := map[string]interface{}{
		"prompt": "a sunset",
		"reference_images": []interface{}{
			"https://example.com/img.jpg",
			"https://example.com/img-2.jpg",
		},
		"reference_image": "https://example.com/img.jpg",
	}

	task := buildTask("generate_video", params)

	if len(task.References) != 2 {
		t.Fatalf("expected 2 unique image references, got %d (%v)", len(task.References), task.References)
	}
}

func TestResolveEnginesSmartRouting(t *testing.T) {
	engines := []string{"aliyun/wan2.7-t2v", "aliyun/wan2.7-i2v", "aliyun/wan2.7-r2v"}
	tool := &AigoTool{
		def:     tooldef.AllTools()[0], // placeholder
		engines: engines,
	}
	// Override the tool name to generate_video for routing logic.
	tool.def.Name = "generate_video"

	// No references → returns all engines (t2v default).
	task := sdk.AgentTask{Prompt: "a sunset"}
	got := tool.resolveEngines(nil, task)
	if len(got) != 3 {
		t.Fatalf("no refs: expected all 3 engines, got %v", got)
	}

	// Single image reference → i2v.
	task.References = []sdk.ReferenceAsset{{Type: sdk.ReferenceTypeImage, URL: "https://example.com/img.jpg"}}
	got = tool.resolveEngines(nil, task)
	if len(got) != 1 || got[0] != "aliyun/wan2.7-i2v" {
		t.Fatalf("1 image ref: expected i2v, got %v", got)
	}

	// Multiple image references → r2v.
	task.References = []sdk.ReferenceAsset{
		{Type: sdk.ReferenceTypeImage, URL: "https://example.com/img1.jpg"},
		{Type: sdk.ReferenceTypeImage, URL: "https://example.com/img2.jpg"},
	}
	got = tool.resolveEngines(nil, task)
	if len(got) != 1 || got[0] != "aliyun/wan2.7-r2v" {
		t.Fatalf("2 image refs: expected r2v, got %v", got)
	}

	// Video reference → r2v.
	task.References = []sdk.ReferenceAsset{{Type: sdk.ReferenceTypeVideo, URL: "https://example.com/vid.mp4"}}
	got = tool.resolveEngines(nil, task)
	if len(got) != 1 || got[0] != "aliyun/wan2.7-r2v" {
		t.Fatalf("video ref: expected r2v, got %v", got)
	}

	// Mixed image + video → r2v.
	task.References = []sdk.ReferenceAsset{
		{Type: sdk.ReferenceTypeImage, URL: "https://example.com/img.jpg"},
		{Type: sdk.ReferenceTypeVideo, URL: "https://example.com/vid.mp4"},
	}
	got = tool.resolveEngines(nil, task)
	if len(got) != 1 || got[0] != "aliyun/wan2.7-r2v" {
		t.Fatalf("mixed refs: expected r2v, got %v", got)
	}

	// Non-video tool → no routing.
	tool.def.Name = "generate_image"
	task.References = []sdk.ReferenceAsset{{Type: sdk.ReferenceTypeImage, URL: "https://example.com/img.jpg"}}
	got = tool.resolveEngines(nil, task)
	if len(got) != 3 {
		t.Fatalf("non-video tool: expected all engines, got %v", got)
	}
}

func TestBuildTTSTask(t *testing.T) {
	params := map[string]interface{}{
		"text":         "hello world",
		"voice":        "alloy",
		"language":     "en",
		"instructions": "speak slowly",
	}
	task := buildTask("text_to_speech", params)
	if task.Prompt != "hello world" {
		t.Fatalf("expected prompt 'hello world', got %q", task.Prompt)
	}
	if task.TTS == nil {
		t.Fatal("expected TTS options")
	}
	if task.TTS.Voice != "alloy" {
		t.Fatalf("expected voice 'alloy', got %q", task.TTS.Voice)
	}
	if task.TTS.LanguageType != "en" {
		t.Fatalf("expected language 'en', got %q", task.TTS.LanguageType)
	}
	if task.TTS.Instructions != "speak slowly" {
		t.Fatalf("expected instructions 'speak slowly', got %q", task.TTS.Instructions)
	}
}

func TestBuildVoiceDesignTask(t *testing.T) {
	params := map[string]interface{}{
		"voice_prompt":   "warm male voice",
		"preview_text":   "hello",
		"target_model":   "cosyvoice-v2",
		"preferred_name": "my-voice",
		"language":       "zh",
	}
	task := buildTask("design_voice", params)
	if task.VoiceDesign == nil {
		t.Fatal("expected VoiceDesign options")
	}
	if task.VoiceDesign.VoicePrompt != "warm male voice" {
		t.Fatalf("unexpected voice_prompt: %q", task.VoiceDesign.VoicePrompt)
	}
	if task.VoiceDesign.PreviewText != "hello" {
		t.Fatalf("unexpected preview_text: %q", task.VoiceDesign.PreviewText)
	}
	if task.VoiceDesign.TargetModel != "cosyvoice-v2" {
		t.Fatalf("unexpected target_model: %q", task.VoiceDesign.TargetModel)
	}
	if task.VoiceDesign.PreferredName != "my-voice" {
		t.Fatalf("unexpected preferred_name: %q", task.VoiceDesign.PreferredName)
	}
}

func TestBuildEditImageTask(t *testing.T) {
	params := map[string]interface{}{
		"prompt":    "remove background",
		"image_url": "https://example.com/photo.jpg",
		"size":      "1024x1024",
	}
	task := buildTask("edit_image", params)
	if task.Prompt != "remove background" {
		t.Fatalf("expected prompt 'remove background', got %q", task.Prompt)
	}
	if task.Size != "1024x1024" {
		t.Fatalf("expected size '1024x1024', got %q", task.Size)
	}
	if len(task.References) != 1 || task.References[0].URL != "https://example.com/photo.jpg" {
		t.Fatalf("expected reference image, got %v", task.References)
	}
}

func TestBuildTranscribeTask(t *testing.T) {
	params := map[string]interface{}{
		"audio_url":       "https://example.com/audio.mp3",
		"language":        "zh",
		"response_format": "json",
	}
	task := buildTask("transcribe_audio", params)
	if task.Prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
}

func TestExecute(t *testing.T) {
	mock := &mockEngine{
		result: engine.Result{Value: "https://example.com/output.png", Kind: engine.OutputURL},
	}

	client := sdk.NewClient()
	if err := client.RegisterEngine("test", mock); err != nil {
		t.Fatal(err)
	}

	at := NewTool(client, tooldef.GenerateImage(), "test")

	result, err := at.Execute(context.Background(), map[string]interface{}{
		"prompt": "a cat wearing a hat",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	if result.Output != "https://example.com/output.png" {
		t.Fatalf("expected output URL, got %q", result.Output)
	}
}

func TestExecuteWithFallback(t *testing.T) {
	fail := &mockEngine{err: context.DeadlineExceeded}
	ok := &mockEngine{
		result: engine.Result{Value: "https://fallback.com/img.png", Kind: engine.OutputURL},
	}

	client := sdk.NewClient()
	_ = client.RegisterEngine("primary", fail)
	_ = client.RegisterEngine("fallback", ok)

	at := &AigoTool{
		client:  client,
		def:     tooldef.GenerateImage(),
		engines: []string{"primary", "fallback"},
	}

	result, err := at.Execute(context.Background(), map[string]interface{}{
		"prompt": "test fallback",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "https://fallback.com/img.png" {
		t.Fatalf("expected fallback output, got %q", result.Output)
	}
}

func TestExecuteWithEngineParam(t *testing.T) {
	mock1 := &mockEngine{
		result: engine.Result{Value: "https://eng1.example.com/out.png", Kind: engine.OutputPlainText},
	}
	mock2 := &mockEngine{
		result: engine.Result{Value: "https://eng2.example.com/out.png", Kind: engine.OutputPlainText},
	}

	client := sdk.NewClient()
	_ = client.RegisterEngine("eng1", mock1)
	_ = client.RegisterEngine("eng2", mock2)

	at := &AigoTool{
		client:  client,
		def:     tooldef.GenerateImage(),
		engines: []string{"eng1", "eng2"},
	}

	result, err := at.Execute(context.Background(), map[string]interface{}{
		"prompt": "test",
		"engine": "eng2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "https://eng2.example.com/out.png" {
		t.Fatalf("expected from-engine2, got %q", result.Output)
	}
}

func TestExecuteError(t *testing.T) {
	mock := &mockEngine{err: context.DeadlineExceeded}

	client := sdk.NewClient()
	if err := client.RegisterEngine("test", mock); err != nil {
		t.Fatal(err)
	}

	at := NewTool(client, tooldef.GenerateImage(), "test")

	_, err := at.Execute(context.Background(), map[string]interface{}{
		"prompt": "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewTools(t *testing.T) {
	client := sdk.NewClient()
	tools := NewTools(client, "test")
	if len(tools) != 9 {
		t.Fatalf("expected 9 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name()] = true
		if tool.Schema() == nil {
			t.Fatalf("tool %s has nil schema", tool.Name())
		}
		if tool.Description() == "" {
			t.Fatalf("tool %s has empty description", tool.Name())
		}
	}

	expected := []string{"generate_image", "generate_video", "text_to_speech", "design_voice", "edit_image", "edit_video", "transcribe_audio"}
	for _, name := range expected {
		if !names[name] {
			t.Fatalf("missing tool %s", name)
		}
	}
}

func TestNilClient(t *testing.T) {
	at := NewTool(nil, tooldef.GenerateImage(), "test")
	_, err := at.Execute(context.Background(), map[string]interface{}{"prompt": "test"})
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestIntParam(t *testing.T) {
	tests := []struct {
		name string
		val  interface{}
		want int
	}{
		{"float64", float64(42), 42},
		{"int", 42, 42},
		{"nil", nil, 0},
		{"string", "42", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := map[string]interface{}{"k": tt.val}
			got := intParam(p, "k")
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseRef(t *testing.T) {
	tests := []struct {
		ref      string
		provider string
		model    string
		wantErr  bool
	}{
		{"ali/qwen-max-vl", "ali", "qwen-max-vl", false},
		{"openai/dall-e-3", "openai", "dall-e-3", false},
		{"newapi/kling-v1", "newapi", "kling-v1", false},
		{"noslash", "", "", true},
		{"/model", "", "", true},
		{"provider/", "", "", true},
		{"", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			p, m, err := ParseRef(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p != tt.provider || m != tt.model {
				t.Fatalf("got %s/%s, want %s/%s", p, m, tt.provider, tt.model)
			}
		})
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("TEST_AIGO_KEY", "sk-test-123")

	if got := expandEnv("${TEST_AIGO_KEY}"); got != "sk-test-123" {
		t.Fatalf("expected sk-test-123, got %q", got)
	}
	if got := expandEnv("plain-string"); got != "plain-string" {
		t.Fatalf("expected plain-string, got %q", got)
	}
	if got := expandEnv(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestInjectEngineParam(t *testing.T) {
	def := tooldef.GenerateImage()
	engines := []string{"ali/qwen", "openai/dalle"}
	injected := injectEngineParam(def, engines)

	ep, ok := injected.Parameters.Properties["engine"]
	if !ok {
		t.Fatal("expected engine property")
	}
	if len(ep.Enum) != 2 {
		t.Fatalf("expected 2 enum values, got %d", len(ep.Enum))
	}
	if ep.Enum[0] != "ali/qwen" || ep.Enum[1] != "openai/dalle" {
		t.Fatalf("unexpected enum values: %v", ep.Enum)
	}
	// Original should be unmodified.
	if _, ok := def.Parameters.Properties["engine"]; ok {
		t.Fatal("original def should not have engine property")
	}
}

func TestNewToolsFromConfig_NilConfig(t *testing.T) {
	tools, err := NewToolsFromConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tools != nil {
		t.Fatal("expected nil tools for nil config")
	}
}

func TestNewToolsFromConfig_EmptyRouting(t *testing.T) {
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{
			"ali": {Type: "aliyun", APIKey: "test"},
		},
		Routing: map[string][]string{},
	}
	tools, err := NewToolsFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tools != nil {
		t.Fatal("expected nil tools for empty routing")
	}
}

func TestNewToolsFromConfig_MissingProvider(t *testing.T) {
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{},
		Routing: map[string][]string{
			"image": {"unknown/model"},
		},
	}
	_, err := NewToolsFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestNewToolsFromConfig_InvalidRef(t *testing.T) {
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{
			"ali": {Type: "aliyun", APIKey: "test"},
		},
		Routing: map[string][]string{
			"image": {"noslash"},
		},
	}
	_, err := NewToolsFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
}

func TestNewToolsFromConfig_UnknownProviderType(t *testing.T) {
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{
			"bad": {Type: "unknown_type", APIKey: "test"},
		},
		Routing: map[string][]string{
			"image": {"bad/model"},
		},
	}
	_, err := NewToolsFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for unknown provider type")
	}
}

func TestNewToolsFromConfig_InvalidTimeout(t *testing.T) {
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{
			"ali": {Type: "aliyun", APIKey: "test"},
		},
		Routing: map[string][]string{
			"image": {"ali/qwen-max-vl"},
		},
		Timeout: "not-a-duration",
	}
	_, err := NewToolsFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid timeout")
	}
}

func TestNewToolsFromConfig_Success(t *testing.T) {
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{
			"ali": {Type: "aliyun", APIKey: "test-key"},
		},
		Routing: map[string][]string{
			"image":      {"ali/qwen-max-vl"},
			"image_edit": {"ali/qwen-max-vl"},
			"video":      {"ali/wanx-video"},
			"video_edit": {"ali/wanx-video"},
			"tts":        {"ali/cosyvoice-v2"},
			"asr":        {"ali/whisper-large-v3"},
		},
		Timeout: "60s",
	}
	tools, err := NewToolsFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 7 tools: generate_music has no capability mapping and is skipped.
	if len(tools) != 7 {
		t.Fatalf("expected 7 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name()] = true
	}
	for _, name := range []string{"generate_image", "edit_image", "generate_video", "edit_video", "text_to_speech", "design_voice", "transcribe_audio"} {
		if !names[name] {
			t.Fatalf("missing tool %s", name)
		}
	}
}

func TestNewToolsFromConfig_PartialRouting(t *testing.T) {
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{
			"ali": {Type: "aliyun", APIKey: "test-key"},
		},
		Routing: map[string][]string{
			"image": {"ali/qwen-max-vl"},
		},
	}
	tools, err := NewToolsFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only generate_image should be registered (edit_image needs "image_edit" routing).
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
}

func TestNewToolsFromConfig_MultiEngine(t *testing.T) {
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{
			"ali":    {Type: "aliyun", APIKey: "ali-key"},
			"openai": {Type: "openai", APIKey: "oai-key"},
		},
		Routing: map[string][]string{
			"image": {"ali/qwen-max-vl", "openai/dall-e-3"},
		},
	}
	tools, err := NewToolsFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only generate_image maps to "image" capability.
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// Tool should have engine param injected for multi-engine routing.
	schema := tools[0].Schema()
	if _, ok := schema.Properties["engine"]; !ok {
		t.Fatalf("tool %s should have engine param for multi-engine routing", tools[0].Name())
	}
}

func TestToolCapabilityMapping(t *testing.T) {
	expected := map[string]string{
		"generate_image":   "image",
		"edit_image":       "image_edit",
		"generate_video":   "video",
		"edit_video":       "video_edit",
		"text_to_speech":   "tts",
		"design_voice":     "tts",
		"transcribe_audio": "asr",
		"generate_3d":      "3d",
		"generate_music":   "music",
	}
	for tool, cap := range expected {
		if got := toolCapability[tool]; got != cap {
			t.Fatalf("tool %s: expected capability %q, got %q", tool, cap, got)
		}
	}
}
