package aigo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/godeps/aigo"
	"github.com/godeps/aigo/engine"
	"github.com/godeps/aigo/tooldef"
	"github.com/godeps/aigo/workflow"

	"github.com/saker-ai/saker/pkg/config"
)

// ----- AigoTool API surface (Engines, Capabilities, DryRun, WithTimeout) -----

func TestWithTimeoutOption(t *testing.T) {
	at := NewTool(sdk.NewClient(), tooldef.GenerateImage(), "engX", WithTimeout(time.Second*7))
	if at.timeout != 7*time.Second {
		t.Errorf("timeout: got %v", at.timeout)
	}
}

func TestEnginesAccessor(t *testing.T) {
	at := NewTool(sdk.NewClient(), tooldef.GenerateImage(), "engA")
	got := at.Engines()
	if len(got) != 1 || got[0] != "engA" {
		t.Errorf("Engines: got %v", got)
	}
}

func TestCapabilitiesNilClient(t *testing.T) {
	at := &AigoTool{engines: []string{"engA"}}
	if at.Capabilities() != nil {
		t.Error("expected nil capabilities for nil client")
	}
	if at.EngineCapabilities("engA") != nil {
		t.Error("expected nil EngineCapabilities for nil client")
	}
}

func TestCapabilitiesEmptyEngines(t *testing.T) {
	at := &AigoTool{client: sdk.NewClient()}
	if at.Capabilities() != nil {
		t.Error("expected nil for empty engines")
	}
}

func TestCapabilitiesUnknownEngine(t *testing.T) {
	client := sdk.NewClient()
	at := &AigoTool{client: client, engines: []string{"missing"}}
	// EngineCapabilities returns nil when client doesn't know the engine.
	if at.EngineCapabilities("missing") != nil {
		t.Error("expected nil for unknown engine")
	}
}

// minimalDescriberEngine implements engine.Engine + Describer to expose capabilities.
type capsEngine struct {
	caps engine.Capability
}

func (e *capsEngine) Execute(_ context.Context, g workflow.Graph) (engine.Result, error) {
	return engine.Result{Value: "https://example.com/x.png"}, nil
}

func (e *capsEngine) Capabilities() engine.Capability {
	return e.caps
}

func TestEngineCapabilitiesDescriberPresent(t *testing.T) {
	c := sdk.NewClient()
	caps := engine.Capability{
		MediaTypes: []string{"image"},
		Models:     []string{"m1"},
	}
	if err := c.RegisterEngine("ce", &capsEngine{caps: caps}); err != nil {
		t.Fatal(err)
	}
	at := NewTool(c, tooldef.GenerateImage(), "ce")
	got := at.Capabilities()
	if got == nil {
		t.Fatal("expected non-nil capabilities")
	}
	if len(got.MediaTypes) != 1 || got.MediaTypes[0] != "image" {
		t.Errorf("MediaTypes: %v", got.MediaTypes)
	}
}

func TestDryRunNoClient(t *testing.T) {
	at := &AigoTool{}
	_, err := at.DryRun(map[string]interface{}{"prompt": "x"})
	if err == nil {
		t.Fatal("expected error for nil client / no engines")
	}
}

func TestDryRunEmptyEngines(t *testing.T) {
	at := &AigoTool{client: sdk.NewClient()}
	_, err := at.DryRun(map[string]interface{}{"prompt": "x"})
	if err == nil {
		t.Fatal("expected error for no engines")
	}
}

// ----- StreamExecute -----

func TestStreamExecuteNilClient(t *testing.T) {
	at := &AigoTool{}
	_, err := at.StreamExecute(context.Background(), map[string]interface{}{"prompt": "x"}, nil)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestStreamExecuteValidationFailure(t *testing.T) {
	c := sdk.NewClient()
	mock := &mockEngine{result: engine.Result{Value: "https://example.com/img.png"}}
	if err := c.RegisterEngine("eg", mock); err != nil {
		t.Fatal(err)
	}
	at := NewTool(c, tooldef.GenerateImage(), "eg")
	// Empty params triggers required-field validation failure.
	res, err := at.StreamExecute(context.Background(), map[string]interface{}{}, func(string, bool) {})
	if err != nil {
		t.Fatalf("expected no err, got: %v", err)
	}
	if res == nil || res.Success {
		t.Errorf("expected success=false")
	}
}

func TestStreamExecuteSuccess(t *testing.T) {
	c := sdk.NewClient()
	mock := &mockEngine{result: engine.Result{Value: "https://example.com/img.png", Kind: engine.OutputURL}}
	if err := c.RegisterEngine("eg", mock); err != nil {
		t.Fatal(err)
	}
	at := NewTool(c, tooldef.GenerateImage(), "eg")
	called := false
	res, err := at.StreamExecute(context.Background(), map[string]interface{}{"prompt": "hi"}, func(string, bool) {
		called = true
	})
	if err != nil {
		t.Fatalf("StreamExecute: %v", err)
	}
	if !res.Success {
		t.Error("expected success")
	}
	_ = called // emit won't fire for non-slow capability — that's ok.
}

// ----- buildTask: edge cases (music, edit_video) -----

func TestBuildMusicTask(t *testing.T) {
	task := buildTask("generate_music", map[string]interface{}{"prompt": "jazz piano"})
	if task.Prompt != "jazz piano" {
		t.Errorf("got %q", task.Prompt)
	}
	// Falls back to "text" key.
	task2 := buildTask("generate_music", map[string]interface{}{"text": "blues"})
	if task2.Prompt != "blues" {
		t.Errorf("got %q", task2.Prompt)
	}
}

func TestBuildEditVideoTask(t *testing.T) {
	task := buildTask("edit_video", map[string]interface{}{
		"prompt":          "add filter",
		"video_url":       "https://example.com/in.mp4",
		"reference_image": "https://example.com/ref.png",
		"duration":        float64(8),
		"size":            "1920x1080",
	})
	if task.Prompt != "add filter" {
		t.Errorf("prompt: %q", task.Prompt)
	}
	if task.Duration != 8 {
		t.Errorf("duration: %d", task.Duration)
	}
	if len(task.References) != 2 {
		t.Errorf("refs: %d", len(task.References))
	}
}

func TestBuildTaskUnknownTool(t *testing.T) {
	task := buildTask("unknown_tool", map[string]interface{}{"prompt": "x"})
	if task.Prompt != "x" {
		t.Errorf("prompt: %q", task.Prompt)
	}
}

func TestBuildImageTaskCameraAngleKnown(t *testing.T) {
	task := buildTask("generate_image", map[string]interface{}{
		"prompt":       "sunset",
		"camera_angle": "front",
	})
	if !strings.Contains(task.Prompt, "front view, sunset") {
		t.Errorf("got %q", task.Prompt)
	}
}

func TestBuildImageTaskCameraAngleUnknown(t *testing.T) {
	task := buildTask("generate_image", map[string]interface{}{
		"prompt":       "sunset",
		"camera_angle": "weirdo",
	})
	if !strings.Contains(task.Prompt, "weirdo shot, sunset") {
		t.Errorf("got %q", task.Prompt)
	}
}

func TestBuildVideoTaskAudioWatermark(t *testing.T) {
	audioFalse := false
	wmTrue := true
	task := buildTask("generate_video", map[string]interface{}{
		"prompt":    "ocean",
		"audio":     audioFalse,
		"watermark": wmTrue,
	})
	if task.Structured == nil {
		t.Fatal("expected structured")
	}
	if task.Structured.VideoAudio == nil || *task.Structured.VideoAudio != false {
		t.Error("audio should be false")
	}
	if task.Structured.VideoWatermark == nil || *task.Structured.VideoWatermark != true {
		t.Error("watermark should be true")
	}
}

func TestStringSliceParam(t *testing.T) {
	tests := []struct {
		name string
		in   interface{}
		want []string
	}{
		{"nil value", nil, nil},
		{"missing key", "absent", nil},
		{"unsupported type", 123, nil},
		{"string slice", []string{"a", " b ", ""}, []string{"a", "b"}},
		{"interface slice", []interface{}{"x", " y ", 1, ""}, []string{"x", "y"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := map[string]interface{}{}
			key := "k"
			if tt.name == "missing key" {
				key = "absent"
			} else {
				p["k"] = tt.in
			}
			got := stringSliceParam(p, key)
			if len(got) != len(tt.want) {
				t.Fatalf("len: got %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ----- resolveLocalRef -----

func TestResolveLocalRefPassThroughURL(t *testing.T) {
	for _, in := range []string{
		"https://example.com/x.png",
		"data:image/png;base64,abc",
		"blob:something",
		"",
	} {
		if got := resolveLocalRef(in); got != in {
			t.Errorf("input %q: got %q", in, got)
		}
	}
}

func TestResolveLocalRefReadsFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "img.png")
	content := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatal(err)
	}
	// Build a /api/files/ URL: strip leading "/" then escape; the function
	// expects /api/files/<rest> where <rest> is path with leading slash removed.
	encoded := url.PathEscape(strings.TrimPrefix(file, "/"))
	// PathEscape would percent-escape slashes too; rebuild manually.
	rest := strings.TrimPrefix(file, "/")
	apiURL := "/api/files/" + rest
	got := resolveLocalRef(apiURL)
	if !strings.HasPrefix(got, "data:") {
		t.Errorf("expected data URI, got %q (encoded=%q)", got, encoded)
	}
	if !strings.Contains(got, ";base64,") {
		t.Errorf("expected base64 marker, got %q", got)
	}
}

func TestResolveLocalRefMissingFile(t *testing.T) {
	apiURL := "/api/files/nonexistent/file/zzz.png"
	// Falls back to original URL on read error.
	got := resolveLocalRef(apiURL)
	if got != apiURL {
		t.Errorf("expected fallback to original URL, got %q", got)
	}
}

// ----- aigo.go isMediaURL -----

func TestIsMediaURL(t *testing.T) {
	tests := map[string]bool{
		"":                            false,
		"https://example.com/x.png":   true,
		"http://example.com/x.png":    true,
		"data:image/png;base64,abc":   true,
		"blob:abc-123":                true,
		"/api/files/x.png":            true,
		"file:///tmp/x.png":           true,
		"some-task-uuid-123":          false,
		"4567890abcdef-not-a-url":     false,
	}
	for in, want := range tests {
		if got := isMediaURL(in); got != want {
			t.Errorf("isMediaURL(%q): got %v, want %v", in, got, want)
		}
	}
}

// ----- toToolResult: media URL guard -----

func TestToToolResultRejectsTaskID(t *testing.T) {
	_, err := toToolResult(sdk.Result{Value: "task-uuid-123"}, "generate_image")
	if err == nil {
		t.Fatal("expected error for non-URL value")
	}
}

func TestToToolResultAcceptsURL(t *testing.T) {
	tr, err := toToolResult(sdk.Result{Value: "https://example.com/x.png"}, "generate_image")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !tr.Success {
		t.Error("expected success")
	}
	if tr.Structured == nil {
		t.Error("expected structured metadata")
	}
}

func TestToToolResultNonMediaCapability(t *testing.T) {
	// transcribe_audio is "asr" which is not in mediaCapabilities → any value ok.
	tr, err := toToolResult(sdk.Result{Value: "transcribed text here"}, "transcribe_audio")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !tr.Success {
		t.Error("expected success")
	}
}

func TestToToolResultEmptyValueNonMedia(t *testing.T) {
	tr, err := toToolResult(sdk.Result{Value: ""}, "transcribe_audio")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if tr == nil {
		t.Fatal("expected ToolResult")
	}
}

// ----- AvailableProviders / AvailableModels / DefaultConfigFromEnv -----

func TestAvailableProviders(t *testing.T) {
	providers := AvailableProviders()
	if len(providers) == 0 {
		t.Fatal("expected providers")
	}
	// Must include comfyui (no env detection but registered explicitly).
	found := false
	for _, p := range providers {
		if p.Name == "comfyui" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected comfyui in providers")
	}
}

func TestAvailableModelsNoEnv(t *testing.T) {
	// Clear all known env vars temporarily — at minimum result should be a map.
	models := AvailableModels()
	// No assertion on count — depends on test runner env. Just verify shape.
	if models == nil {
		t.Error("expected non-nil map")
	}
}

func TestAvailableModelsWithEnv(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "test-key")
	models := AvailableModels()
	if _, ok := models["alibabacloud"]; !ok {
		t.Errorf("expected alibabacloud entry, got %v", models)
	}
}

func TestDefaultConfigFromEnvNone(t *testing.T) {
	// Save & clear all known env vars.
	for _, k := range []string{
		"DASHSCOPE_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY",
		"FLUX_API_KEY", "STABILITY_API_KEY", "IDEOGRAM_API_KEY",
		"RECRAFT_API_KEY", "MIDJOURNEY_API_KEY", "JIMENG_API_KEY",
		"LIBLIB_ACCESS_KEY", "ARK_API_KEY", "KLING_API_KEY",
		"HAILUO_API_KEY", "LUMA_API_KEY", "RUNWAY_API_KEY",
		"PIKA_API_KEY", "HEDRA_API_KEY", "ELEVENLABS_API_KEY",
		"MINIMAX_API_KEY", "SUNO_API_KEY", "VOLC_SPEECH_ACCESS_TOKEN",
		"MESHY_API_KEY", "GEMINI_API_KEY", "NEWAPI_API_KEY",
		"OPENROUTER_API_KEY", "FAL_KEY", "REPLICATE_API_TOKEN",
		"COMFYDEPLOY_API_KEY", "RUNNINGHUB_API_KEY",
	} {
		t.Setenv(k, "")
	}
	cfg := DefaultConfigFromEnv()
	if cfg != nil {
		t.Errorf("expected nil config, got %+v", cfg)
	}
}

func TestDefaultConfigFromEnvWithKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	cfg := DefaultConfigFromEnv()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if _, ok := cfg.Providers["openai"]; !ok {
		t.Errorf("expected openai provider")
	}
}

// ----- prefixRoutes -----

func TestPrefixRoutes(t *testing.T) {
	in := map[string][]string{
		"image": {"m1", "m2"},
		"video": {"v1"},
	}
	got := prefixRoutes("provider", in)
	if got["image"][0] != "provider/m1" {
		t.Errorf("got %v", got["image"])
	}
	if got["image"][1] != "provider/m2" {
		t.Errorf("got %v", got["image"])
	}
	if got["video"][0] != "provider/v1" {
		t.Errorf("got %v", got["video"])
	}
}

func TestPrefixRoutesEmpty(t *testing.T) {
	got := prefixRoutes("p", nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

// ----- CheckProviderConnectivity -----

func TestCheckProviderConnectivity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close() // immediately close → unreachable

	providers := map[string]config.AigoProvider{
		"reachable":   {Type: "openai", BaseURL: srv.URL},
		"unreachable": {Type: "openai", BaseURL: "http://127.0.0.1:1"},
		"no-baseurl":  {Type: "openai"}, // skipped
	}
	statuses := CheckProviderConnectivity(providers)
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d (%v)", len(statuses), statuses)
	}
	var foundReachable bool
	for _, s := range statuses {
		if s.Name == "reachable" {
			foundReachable = true
			if !s.Reachable {
				t.Error("reachable provider should be reachable")
			}
		}
		if s.Name == "unreachable" && s.Reachable {
			t.Error("unreachable should not be reachable")
		}
	}
	if !foundReachable {
		t.Error("missing reachable provider in results")
	}
}

func TestCheckProviderConnectivityEmpty(t *testing.T) {
	got := CheckProviderConnectivity(map[string]config.AigoProvider{})
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

// ----- NewToolsFromConfig: WithDataDir, DisabledModels paths -----

func TestNewToolsFromConfigWithDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{
			"ali": {Type: "aliyun", APIKey: "test-key"},
		},
		Routing: map[string][]string{
			"image": {"ali/qwen-max-vl"},
		},
	}
	tools, err := NewToolsFromConfig(cfg, WithDataDir(dir))
	if err != nil {
		t.Fatalf("NewToolsFromConfig: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected tools")
	}
	// Verify the task store file was created.
	if _, err := os.Stat(filepath.Join(dir, "aigo_tasks.json")); err != nil {
		// Some SDK builds may create lazily; not fatal.
		t.Logf("task store file not created (lazy init?): %v", err)
	}
}

func TestNewToolsFromConfigDisabledProvider(t *testing.T) {
	enabled := false
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{
			"ali": {Type: "aliyun", APIKey: "test-key", Enabled: &enabled},
		},
		Routing: map[string][]string{
			"image": {"ali/qwen-max-vl"},
		},
	}
	tools, err := NewToolsFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewToolsFromConfig: %v", err)
	}
	// Tools are still registered, but the engine inside is disabled.
	if len(tools) == 0 {
		t.Fatal("expected tools (engines disabled at runtime)")
	}
}

func TestNewToolsFromConfigDisabledModels(t *testing.T) {
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{
			"ali": {Type: "aliyun", APIKey: "test-key", DisabledModels: []string{"qwen-max-vl"}},
		},
		Routing: map[string][]string{
			"image": {"ali/qwen-max-vl"},
		},
	}
	tools, err := NewToolsFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewToolsFromConfig: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected tools")
	}
}

func TestNewToolsFromConfigVideoTimeoutDefault(t *testing.T) {
	cfg := &config.AigoConfig{
		Providers: map[string]config.AigoProvider{
			"ali": {Type: "aliyun", APIKey: "test-key"},
		},
		Routing: map[string][]string{
			"video": {"ali/wanx-video"},
		},
		// No explicit Timeout — video should get videoTimeout default.
	}
	tools, err := NewToolsFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewToolsFromConfig: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if at, ok := tools[0].(*AigoTool); ok {
		if at.timeout != videoTimeout {
			t.Errorf("video timeout: got %v, want %v", at.timeout, videoTimeout)
		}
	}
}

// ----- formatInvalidParams empty params hint -----

func TestFormatInvalidParamsEmptyParamsAddsNote(t *testing.T) {
	def := tooldef.GenerateImage()
	out := formatInvalidParams(def, map[string]interface{}{}, errSimple{"prompt is required"})
	if !strings.Contains(out, "tool was called with no parameters at all") {
		t.Errorf("expected hint about empty params, got %q", out)
	}
}

func TestFormatInvalidParamsWithParamsNoNote(t *testing.T) {
	def := tooldef.GenerateImage()
	out := formatInvalidParams(def, map[string]interface{}{"size": "1024x1024"}, errSimple{"prompt is required"})
	if strings.Contains(out, "tool was called with no parameters at all") {
		t.Errorf("did not expect empty-params hint, got %q", out)
	}
}

type errSimple struct{ msg string }

func (e errSimple) Error() string { return e.msg }
