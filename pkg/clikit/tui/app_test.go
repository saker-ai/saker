package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/clikit"
)

// ---------------------------------------------------------------------------
// mockReplEngine implements clikit.ReplEngine for App construction in tests.
// ---------------------------------------------------------------------------
type mockReplEngine struct {
	modelName   string
	skills      []clikit.SkillMeta
	sandbox     string
	repoRoot    string
	setModelErr error
}

func (m *mockReplEngine) RunStream(_ context.Context, _, _ string) (<-chan api.StreamEvent, error) {
	ch := make(chan api.StreamEvent)
	close(ch)
	return ch, nil
}
func (m *mockReplEngine) RunStreamForked(_ context.Context, _, _, _ string) (<-chan api.StreamEvent, error) {
	ch := make(chan api.StreamEvent)
	close(ch)
	return ch, nil
}
func (m *mockReplEngine) ModelTurnCount(_ string) int                            { return 0 }
func (m *mockReplEngine) ModelTurnsSince(_ string, _ int) []clikit.ModelTurnStat { return nil }
func (m *mockReplEngine) RepoRoot() string                                       { return m.repoRoot }
func (m *mockReplEngine) ModelName() string                                      { return m.modelName }
func (m *mockReplEngine) SetModel(_ context.Context, name string) error {
	if m.setModelErr != nil {
		return m.setModelErr
	}
	m.modelName = name
	return nil
}
func (m *mockReplEngine) Skills() []clikit.SkillMeta { return m.skills }
func (m *mockReplEngine) SandboxBackend() string     { return m.sandbox }

func newTestApp() *App {
	return New(context.Background(), AppConfig{
		Engine: &mockReplEngine{
			modelName: "test-model",
			skills:    []clikit.SkillMeta{{Name: "alpha"}, {Name: "beta"}},
			sandbox:   "gvisor",
			repoRoot:  "/tmp/test",
		},
	})
}

// setInputText sets the textarea value for testing slash command submission.
// Since tests are in the same package we can access the unexported textarea field.
func setInputText(a *App, text string) {
	a.input.textarea.SetValue(text)
}

// ---------------------------------------------------------------------------
// extractToolParamsFromJSON -- table-driven tests
// ---------------------------------------------------------------------------
func TestExtractToolParamsFromJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		toolName  string
		inputJSON string
		want      string
	}{
		{
			name:      "empty JSON returns empty",
			toolName:  "BashTool",
			inputJSON: "",
			want:      "",
		},
		{
			name:      "invalid JSON returns empty",
			toolName:  "BashTool",
			inputJSON: "{not json}",
			want:      "",
		},
		{
			name:      "read tool extracts file_path base",
			toolName:  "read",
			inputJSON: `{"file_path":"/home/user/src/main.go"}`,
			want:      "main.go",
		},
		{
			name:      "write tool extracts file_path base",
			toolName:  "write",
			inputJSON: `{"file_path":"/tmp/output/result.txt"}`,
			want:      "result.txt",
		},
		{
			name:      "edit tool extracts file_path base",
			toolName:  "edit",
			inputJSON: `{"file_path":"/app/config.yaml"}`,
			want:      "config.yaml",
		},
		{
			name:      "read tool without file_path returns empty",
			toolName:  "read",
			inputJSON: `{"content":"hello"}`,
			want:      "",
		},
		{
			name:      "bash tool short command",
			toolName:  "bash",
			inputJSON: `{"command":"ls -la"}`,
			want:      "ls -la",
		},
		{
			name:      "bash tool long command truncated",
			toolName:  "bash",
			inputJSON: fmt.Sprintf(`{"command":"%s"}`, strings.Repeat("a", 90)),
			want:      strings.Repeat("a", 77) + "…",
		},
		{
			name:      "bash tool multiline joins first two lines",
			toolName:  "bash",
			inputJSON: `{"command":"echo hello\necho world"}`,
			want:      "echo hello echo world",
		},
		{
			name:      "bash tool without command returns empty",
			toolName:  "bash",
			inputJSON: `{"timeout":30}`,
			want:      "",
		},
		{
			name:      "grep tool short pattern",
			toolName:  "grep",
			inputJSON: `{"pattern":"TODO"}`,
			want:      "TODO",
		},
		{
			name:      "grep tool long pattern truncated",
			toolName:  "grep",
			inputJSON: fmt.Sprintf(`{"pattern":"%s"}`, strings.Repeat("x", 65)),
			want:      strings.Repeat("x", 57) + "…",
		},
		{
			name:      "glob tool pattern",
			toolName:  "glob",
			inputJSON: `{"pattern":"**/*.go"}`,
			want:      "**/*.go",
		},
		{
			name:      "glob tool without pattern returns empty",
			toolName:  "glob",
			inputJSON: `{"path":"/src"}`,
			want:      "",
		},
		{
			name:      "unknown tool returns empty",
			toolName:  "CustomTool",
			inputJSON: `{"arg":"val"}`,
			want:      "",
		},
		{
			name:      "case insensitive tool name matching",
			toolName:  "bash_tool_executor",
			inputJSON: `{"command":"pwd"}`,
			want:      "pwd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractToolParamsFromJSON(tt.toolName, tt.inputJSON)
			if got != tt.want {
				t.Errorf("extractToolParamsFromJSON(%q, %q) = %q, want %q", tt.toolName, tt.inputJSON, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// summarizeOutput -- table-driven tests
// ---------------------------------------------------------------------------
func TestSummarizeOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		toolName string
		output   string
		want     string
	}{
		{
			name:     "empty output returns empty",
			toolName: "bash",
			output:   "",
			want:     "",
		},
		{
			name:     "whitespace only returns empty",
			toolName: "bash",
			output:   "   \n  \t  ",
			want:     "",
		},
		{
			name:     "bash single line",
			toolName: "bash",
			output:   "hello world",
			want:     "hello world",
		},
		{
			name:     "bash two lines shown fully",
			toolName: "bash",
			output:   "line1\nline2",
			want:     "line1\nline2",
		},
		{
			name:     "bash multi-line shows last line and count",
			toolName: "bash",
			output:   "a\nb\nc\nlast",
			want:     "… last (4 lines)",
		},
		{
			name:     "bash multi-line last line empty uses previous",
			toolName: "bash",
			output:   "a\nb\nresult\n",
			want:     "… result (3 lines)",
		},
		{
			name:     "non-bash short output shown fully",
			toolName: "read",
			output:   "ok",
			want:     "ok",
		},
		{
			name:     "non-bash multi-line shows line count",
			toolName: "glob",
			output:   "f1\nf2\nf3\nf4\nf5",
			want:     "5 lines",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := summarizeOutput(tt.toolName, tt.output)
			if got != tt.want {
				t.Errorf("summarizeOutput(%q, %q) = %q, want %q", tt.toolName, tt.output, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// summarizeToolResult -- table-driven tests
// ---------------------------------------------------------------------------
func TestSummarizeToolResult(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		toolName string
		output   string
		want     string
	}{
		{
			name:     "empty output returns empty",
			toolName: "read",
			output:   "",
			want:     "",
		},
		{
			name:     "read tool reports line count singular",
			toolName: "read",
			output:   "single line",
			want:     "Read 1 line",
		},
		{
			name:     "read tool reports line count plural",
			toolName: "read",
			output:   "a\nb\nc",
			want:     "Read 3 lines",
		},
		{
			name:     "write tool reports line count",
			toolName: "write",
			output:   "a\nb",
			want:     "Wrote 2 lines",
		},
		{
			name:     "edit tool always says Applied changes",
			toolName: "edit",
			output:   "some diff output",
			want:     "Applied changes",
		},
		{
			name:     "grep tool counts matches singular",
			toolName: "grep",
			output:   "match1",
			want:     "1 match",
		},
		{
			name:     "grep tool counts matches plural",
			toolName: "grep",
			output:   "m1\nm2\nm3",
			want:     "3 matches",
		},
		{
			name:     "grep skips blank lines",
			toolName: "grep",
			output:   "m1\n\nm2\n  \n",
			want:     "2 matches",
		},
		{
			name:     "glob tool counts files singular",
			toolName: "glob",
			output:   "a.go",
			want:     "1 file",
		},
		{
			name:     "glob tool counts files plural",
			toolName: "glob",
			output:   "a.go\nb.go\nc.go",
			want:     "3 files",
		},
		{
			name:     "bash tool short output shown fully",
			toolName: "bash",
			output:   "done",
			want:     "done",
		},
		{
			name:     "bash tool multi-line shows last and count",
			toolName: "bash",
			output:   "a\nb\nc\nresult",
			want:     "… result (4 lines)",
		},
		{
			name:     "unknown tool single line shown",
			toolName: "CustomTool",
			output:   "output text",
			want:     "output text",
		},
		{
			name:     "unknown tool multi-line shows count",
			toolName: "CustomTool",
			output:   "a\nb\nc\n",
			want:     "3 lines",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := summarizeToolResult(tt.toolName, tt.output)
			if got != tt.want {
				t.Errorf("summarizeToolResult(%q, %q) = %q, want %q", tt.toolName, tt.output, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// truncLine -- table-driven tests
// ---------------------------------------------------------------------------
func TestTruncLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{
			name: "short string unchanged",
			s:    "hello",
			max:  100,
			want: "hello",
		},
		{
			name: "string at max unchanged",
			s:    "hello",
			max:  5,
			want: "hello",
		},
		{
			name: "string beyond max truncated",
			s:    "hello world",
			max:  6,
			want: "hello…",
		},
		{
			name: "leading/trailing whitespace trimmed",
			s:    "  hello  ",
			max:  100,
			want: "hello",
		},
		{
			name: "whitespace only trimmed to empty",
			s:    "   ",
			max:  100,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncLine(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("truncLine(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// lastNonEmpty -- table-driven tests
// ---------------------------------------------------------------------------
func TestLastNonEmpty(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name:  "single non-empty line",
			lines: []string{"hello"},
			want:  "hello",
		},
		{
			name:  "trailing empty lines skipped",
			lines: []string{"a", "b", "", "  ", ""},
			want:  "b",
		},
		{
			name:  "all empty returns empty",
			lines: []string{"", "  ", "\t"},
			want:  "",
		},
		{
			name:  "empty slice returns empty",
			lines: []string{},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := lastNonEmpty(tt.lines)
			if got != tt.want {
				t.Errorf("lastNonEmpty(%v) = %q, want %q", tt.lines, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// pluralize -- table-driven tests
// ---------------------------------------------------------------------------
func TestPluralize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		n        int
		singular string
		plural   string
		want     string
	}{
		{
			name:     "one returns singular",
			n:        1,
			singular: "file",
			plural:   "files",
			want:     "file",
		},
		{
			name:     "zero returns plural",
			n:        0,
			singular: "file",
			plural:   "files",
			want:     "files",
		},
		{
			name:     "many returns plural",
			n:        5,
			singular: "match",
			plural:   "matches",
			want:     "matches",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pluralize(tt.n, tt.singular, tt.plural)
			if got != tt.want {
				t.Errorf("pluralize(%d, %q, %q) = %q, want %q", tt.n, tt.singular, tt.plural, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// displaySandbox -- table-driven tests
// ---------------------------------------------------------------------------
func TestDisplaySandbox(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		s    string
		want string
	}{
		{
			name: "empty string defaults to host",
			s:    "",
			want: "host",
		},
		{
			name: "whitespace defaults to host",
			s:    "   ",
			want: "host",
		},
		{
			name: "non-empty value returned",
			s:    "gvisor",
			want: "gvisor",
		},
		{
			name: "docker sandbox returned",
			s:    "docker",
			want: "docker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := displaySandbox(tt.s)
			if got != tt.want {
				t.Errorf("displaySandbox(%q) = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatToolOutput -- table-driven tests
// ---------------------------------------------------------------------------
func TestFormatToolOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		evt  api.StreamEvent
		want string
	}{
		{
			name: "nil output returns empty",
			evt:  api.StreamEvent{Name: "bash", Output: nil},
			want: "",
		},
		{
			name: "bash output summarized",
			evt:  api.StreamEvent{Name: "bash", Output: "a\nb\nc\nresult"},
			want: "… result (4 lines)",
		},
		{
			name: "read output summarized",
			evt:  api.StreamEvent{Name: "read", Output: "a\nb\nc\n\nd\n"},
			want: "5 lines",
		},
		{
			name: "single line output shown",
			evt:  api.StreamEvent{Name: "Custom", Output: "ok"},
			want: "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatToolOutput(tt.evt)
			if got != tt.want {
				t.Errorf("formatToolOutput(%v) = %q, want %q", tt.evt, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatToolResult -- table-driven tests
// ---------------------------------------------------------------------------
func TestFormatToolResult(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		evt     api.StreamEvent
		want    string
		wantErr bool
	}{
		{
			name:    "nil output returns empty no error",
			evt:     api.StreamEvent{Name: "bash", Output: nil},
			want:    "",
			wantErr: false,
		},
		{
			name:    "non-map output returns empty no error",
			evt:     api.StreamEvent{Name: "bash", Output: "string not map"},
			want:    "",
			wantErr: false,
		},
		{
			name: "map output without metadata",
			evt: api.StreamEvent{
				Name:   "read",
				Output: map[string]any{"output": "a\nb\nc"},
			},
			want:    "Read 3 lines",
			wantErr: false,
		},
		{
			name: "map output with is_error metadata",
			evt: api.StreamEvent{
				Name: "bash",
				Output: map[string]any{
					"output":   "exit code 1",
					"metadata": map[string]any{"is_error": true},
				},
			},
			want:    "exit code 1",
			wantErr: true,
		},
		{
			name: "map output without is_error flag",
			evt: api.StreamEvent{
				Name: "bash",
				Output: map[string]any{
					"output":   "success",
					"metadata": map[string]any{},
				},
			},
			want:    "success",
			wantErr: false,
		},
		{
			name: "empty output string returns empty",
			evt: api.StreamEvent{
				Name:   "read",
				Output: map[string]any{"output": ""},
			},
			want:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, isErr := formatToolResult(tt.evt)
			if got != tt.want {
				t.Errorf("formatToolResult() summary = %q, want %q", got, tt.want)
			}
			if isErr != tt.wantErr {
				t.Errorf("formatToolResult() isErr = %v, want %v", isErr, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractImagePaths -- additional coverage beyond image_test.go
// ---------------------------------------------------------------------------
func TestExtractImagePaths_ArtifactRefSlice(t *testing.T) {
	t.Parallel()
	evt := api.StreamEvent{
		Output: map[string]any{
			"metadata": map[string]any{
				"artifacts": []artifact.ArtifactRef{
					{Path: "/tmp/frame.png", Kind: artifact.ArtifactKindImage},
					{Path: "/tmp/data.json", Kind: artifact.ArtifactKindDocument},
					{Path: "/tmp/photo.jpg", Kind: ""}, // kind empty but ext is image
				},
			},
		},
	}

	paths := extractImagePaths(evt)
	if len(paths) != 2 {
		t.Fatalf("expected 2 image paths, got %d: %v", len(paths), paths)
	}
	if paths[0] != "/tmp/frame.png" {
		t.Errorf("expected /tmp/frame.png, got %s", paths[0])
	}
	if paths[1] != "/tmp/photo.jpg" {
		t.Errorf("expected /tmp/photo.jpg, got %s", paths[1])
	}
}

func TestExtractImagePaths_NonMapOutput(t *testing.T) {
	t.Parallel()
	evt := api.StreamEvent{Output: "not a map"}
	paths := extractImagePaths(evt)
	if paths != nil {
		t.Fatalf("expected nil for non-map output, got %v", paths)
	}
}

func TestExtractImagePaths_MetadataWithoutArtifacts(t *testing.T) {
	t.Parallel()
	// Renamed from TestExtractImagePaths_NoArtifacts to avoid collision with image_test.go.
	evt := api.StreamEvent{
		Output: map[string]any{
			"metadata": map[string]any{},
		},
	}
	paths := extractImagePaths(evt)
	if paths != nil {
		t.Fatalf("expected nil for empty metadata, got %v", paths)
	}
}

func TestExtractImagePaths_NonImageArtifactsFiltered(t *testing.T) {
	t.Parallel()
	evt := api.StreamEvent{
		Output: map[string]any{
			"metadata": map[string]any{
				"artifacts": []any{
					map[string]any{"path": "/tmp/doc.pdf", "kind": "document"},
				},
			},
		},
	}
	paths := extractImagePaths(evt)
	if len(paths) != 0 {
		t.Fatalf("expected 0 paths for non-image artifacts, got %d", len(paths))
	}
}

func TestExtractImagePaths_ImageByExtensionOnly(t *testing.T) {
	t.Parallel()
	evt := api.StreamEvent{
		Output: map[string]any{
			"metadata": map[string]any{
				"artifacts": []any{
					map[string]any{"path": "/tmp/frame.webp", "kind": ""}, // webp by ext
					map[string]any{"path": "/tmp/icon.bmp", "kind": ""},   // bmp by ext
					map[string]any{"path": "/tmp/text.txt", "kind": ""},   // not image ext
				},
			},
		},
	}
	paths := extractImagePaths(evt)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(paths), paths)
	}
	if paths[0] != "/tmp/frame.webp" {
		t.Errorf("expected /tmp/frame.webp, got %s", paths[0])
	}
	if paths[1] != "/tmp/icon.bmp" {
		t.Errorf("expected /tmp/icon.bmp, got %s", paths[1])
	}
}

// ---------------------------------------------------------------------------
// App construction -- state initialization
// ---------------------------------------------------------------------------
func TestAppNew_GeneratesSessionID(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	if a.sessionID == "" {
		t.Error("expected non-empty sessionID")
	}
}

func TestAppNew_UsesProvidedSessionID(t *testing.T) {
	t.Parallel()
	a := New(context.Background(), AppConfig{
		Engine:           &mockReplEngine{modelName: "m"},
		InitialSessionID: "custom-session-42",
	})
	if a.sessionID != "custom-session-42" {
		t.Errorf("expected sessionID=custom-session-42, got %q", a.sessionID)
	}
}

func TestAppNew_TrimsProvidedSessionID(t *testing.T) {
	t.Parallel()
	a := New(context.Background(), AppConfig{
		Engine:           &mockReplEngine{modelName: "m"},
		InitialSessionID: "  trimmed-id  ",
	})
	if a.sessionID != "trimmed-id" {
		t.Errorf("expected sessionID=trimmed-id, got %q", a.sessionID)
	}
}

func TestAppNew_SetsHeaderModel(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	if a.header.modelName != "test-model" {
		t.Errorf("expected header modelName=test-model, got %q", a.header.modelName)
	}
}

func TestAppNew_SetsStatusModel(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	if a.status.modelName != "test-model" {
		t.Errorf("expected status modelName=test-model, got %q", a.status.modelName)
	}
}

func TestAppNew_SetsSkillCount(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	if a.header.skillCount != 2 {
		t.Errorf("expected header skillCount=2, got %d", a.header.skillCount)
	}
}

func TestAppNew_InitializesComponents(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	if a.chat == nil {
		t.Error("expected chat to be initialized")
	}
	if a.input == nil {
		t.Error("expected input to be initialized")
	}
	if a.status == nil {
		t.Error("expected status to be initialized")
	}
	if a.header == nil {
		t.Error("expected header to be initialized")
	}
}

func TestAppNew_UpdateNoticeInHeader(t *testing.T) {
	t.Parallel()
	a := New(context.Background(), AppConfig{
		Engine:       &mockReplEngine{modelName: "m"},
		UpdateNotice: "v2.0 available",
	})
	if a.header.updateNotice != "v2.0 available" {
		t.Errorf("expected header updateNotice=v2.0 available, got %q", a.header.updateNotice)
	}
}

// ---------------------------------------------------------------------------
// Key binding: ctrl+d quits
// ---------------------------------------------------------------------------
func TestHandleKey_CtrlDQuits(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.handleKey(ctrlDKey())
	if cmd == nil {
		t.Error("expected non-nil cmd for ctrl+d")
	}
}

// ---------------------------------------------------------------------------
// Key binding: ctrl+c while streaming cancels
// ---------------------------------------------------------------------------
func TestAppHandleKey_CtrlCCancelsStream(t *testing.T) {
	a := newTestApp()
	// Simulate an active stream by setting runCancel.
	runCtx, runCancel := context.WithCancel(a.ctx)
	a.runCancel = runCancel
	a.spinning = true
	a.input.SetEnabled(false)

	_, _ = a.handleKey(ctrlCKey())

	if a.runCancel != nil {
		t.Error("expected runCancel to be nil after ctrl+c")
	}
	if runCtx.Err() == nil {
		t.Error("expected context to be cancelled")
	}
	if a.spinning {
		t.Error("expected spinning to be false after cancel")
	}
	if !a.input.enabled {
		t.Error("expected input to be enabled after cancel")
	}
	if a.status.text != "Interrupted (press Ctrl+C again to exit)" {
		t.Errorf("expected interrupted status, got %q", a.status.text)
	}
	// Note: flushChat may return nil if no messages are pending; the key
	// behavior tested here is the state transition, not the flush cmd.
}

// ---------------------------------------------------------------------------
// Key binding: double ctrl+c quits
// ---------------------------------------------------------------------------
func TestAppHandleKey_DoubleCtrlCQuits(t *testing.T) {
	a := newTestApp()
	// First ctrl+c (no stream running): just sets lastInterrupt time.
	_, _ = a.handleKey(ctrlCKey())
	if a.status.text != "Press Ctrl+C again to exit" {
		t.Errorf("expected first ctrl+c status, got %q", a.status.text)
	}

	// Second ctrl+c within 1 second: quits.
	_, cmd := a.handleKey(ctrlCKey())
	if cmd == nil {
		t.Error("expected quit cmd on double ctrl+c")
	}
}

// ---------------------------------------------------------------------------
// Key binding: ctrl+c then delayed ctrl+c does not quit
// ---------------------------------------------------------------------------
func TestAppHandleKey_SlowDoubleCtrlCDoesNotQuit(t *testing.T) {
	a := newTestApp()
	// First ctrl+c.
	_, _ = a.handleKey(ctrlCKey())
	// Simulate time passing beyond 1 second.
	a.lastInterrupt = a.lastInterrupt.Add(-2 * time.Second)

	_, cmd := a.handleKey(ctrlCKey())
	// Should NOT quit -- just reset the interrupt timer.
	if cmd != nil {
		t.Error("expected nil cmd (no quit) for slow double ctrl+c")
	}
}

// ---------------------------------------------------------------------------
// Key binding: enter with empty input does nothing
// ---------------------------------------------------------------------------
func TestAppHandleKey_EnterEmptyInput(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	a.input.Reset() // ensure empty
	_, cmd := a.handleKey(enterKey())
	if cmd != nil {
		t.Errorf("expected nil cmd for enter with empty input, got %v", cmd)
	}
}

// ---------------------------------------------------------------------------
// handleSubmit -- slash command state transitions
// ---------------------------------------------------------------------------
func TestHandleSubmit_SlashQuit(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.handleSubmit("/quit")
	if cmd == nil {
		t.Error("expected quit cmd for /quit")
	}
}

func TestHandleSubmit_SlashExit(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.handleSubmit("/exit")
	if cmd == nil {
		t.Error("expected quit cmd for /exit")
	}
}

func TestHandleSubmit_SlashQ(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.handleSubmit("/q")
	if cmd == nil {
		t.Error("expected quit cmd for /q")
	}
}

func TestHandleSubmit_SlashNew(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	oldSessionID := a.sessionID
	_, _ = a.handleSubmit("/new")

	if a.sessionID == oldSessionID {
		t.Error("expected new sessionID after /new")
	}
	if a.status.inputTokens != 0 || a.status.outputTokens != 0 {
		t.Error("expected token counters reset after /new")
	}
}

func TestHandleSubmit_SlashModelNoArg(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.handleSubmit("/model")
	if cmd == nil {
		t.Error("expected flush cmd for /model display")
	}
}

func TestHandleSubmit_SlashModelSwitch(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, _ = a.handleSubmit("/model new-model")

	if a.cfg.Engine.ModelName() != "new-model" {
		t.Errorf("expected model name new-model, got %q", a.cfg.Engine.ModelName())
	}
	if a.header.modelName != "new-model" {
		t.Errorf("expected header modelName=new-model, got %q", a.header.modelName)
	}
	if a.status.modelName != "new-model" {
		t.Errorf("expected status modelName=new-model, got %q", a.status.modelName)
	}
}

func TestHandleSubmit_SlashModelSwitchError(t *testing.T) {
	t.Parallel()
	a := New(context.Background(), AppConfig{
		Engine: &mockReplEngine{
			modelName:   "old-model",
			setModelErr: errors.New("model not found"),
		},
	})
	_, cmd := a.handleSubmit("/model bad-model")
	if cmd == nil {
		t.Error("expected flush cmd even on model switch failure")
	}
	if a.cfg.Engine.ModelName() != "old-model" {
		t.Errorf("expected model name unchanged, got %q", a.cfg.Engine.ModelName())
	}
}

func TestHandleSubmit_SlashSession(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.handleSubmit("/session")
	if cmd == nil {
		t.Error("expected flush cmd for /session")
	}
}

func TestHandleSubmit_SlashBtwNoArg(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.handleSubmit("/btw")
	if cmd == nil {
		t.Error("expected flush cmd for /btw usage error")
	}
	if a.sidePanel != nil {
		t.Error("expected no side panel for empty /btw")
	}
}

func TestHandleSubmit_SlashBtwWithQuestion(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, _ = a.handleSubmit("/btw what is middleware?")
	if a.sidePanel == nil {
		t.Error("expected side panel for /btw with question")
	}
	if a.sidePanel.panelType != "btw" {
		t.Errorf("expected panelType=btw, got %q", a.sidePanel.panelType)
	}
	if a.sidePanel.interactive {
		t.Error("expected non-interactive panel for /btw")
	}
}

func TestHandleSubmit_SlashImNoArg(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, _ = a.handleSubmit("/im")
	if a.sidePanel == nil {
		t.Error("expected side panel to be created for /im")
	}
	if !a.sidePanel.IsInteractive() {
		t.Error("expected interactive side panel for /im")
	}
}

func TestHandleSubmit_SlashHelp(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.handleSubmit("/help")
	if cmd == nil {
		t.Error("expected flush cmd for /help")
	}
}

func TestHandleSubmit_SlashSkills(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.handleSubmit("/skills")
	if cmd == nil {
		t.Error("expected flush cmd for /skills")
	}
}

func TestHandleSubmit_SlashStatus(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.handleSubmit("/status")
	if cmd == nil {
		t.Error("expected flush cmd for /status")
	}
}

func TestHandleSubmit_SlashStatusWithTokens(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	a.status.AddTokens(1000, 500)
	_, cmd := a.handleSubmit("/status")
	if cmd == nil {
		t.Error("expected flush cmd for /status with tokens")
	}
}

// ---------------------------------------------------------------------------
// Custom commands -- handled and not handled
// ---------------------------------------------------------------------------
func TestHandleSubmit_CustomCommandHandled(t *testing.T) {
	t.Parallel()
	a := New(context.Background(), AppConfig{
		Engine: &mockReplEngine{modelName: "m"},
		CustomCommands: func(input string, out io.Writer) (handled, quit bool) {
			if strings.HasPrefix(input, "/custom") {
				fmt.Fprint(out, "custom result")
				return true, false
			}
			return false, false
		},
	})
	_, cmd := a.handleSubmit("/custom do-stuff")
	if cmd == nil {
		t.Error("expected flush cmd for handled custom command")
	}
}

func TestHandleSubmit_CustomCommandQuit(t *testing.T) {
	t.Parallel()
	a := New(context.Background(), AppConfig{
		Engine: &mockReplEngine{modelName: "m"},
		CustomCommands: func(input string, _ io.Writer) (handled, quit bool) {
			return input == "/bye", true
		},
	})
	_, cmd := a.handleSubmit("/bye")
	if cmd == nil {
		t.Error("expected quit cmd for custom command that signals quit")
	}
}

func TestHandleSubmit_CustomCommandNotHandledFallsThrough(t *testing.T) {
	t.Parallel()
	a := New(context.Background(), AppConfig{
		Engine: &mockReplEngine{modelName: "m"},
		CustomCommands: func(_ string, _ io.Writer) (handled, quit bool) {
			return false, false // not handled
		},
	})
	_, cmd := a.handleSubmit("/quit")
	if cmd == nil {
		t.Error("expected quit cmd -- custom command did not handle, /quit should be processed")
	}
}

// ---------------------------------------------------------------------------
// Update: StreamDoneMsg state transitions
// ---------------------------------------------------------------------------
func TestAppUpdate_StreamDoneMsg(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	a.spinning = true
	a.input.SetEnabled(false)
	a.chat.StartStreaming()

	_, _ = a.Update(StreamDoneMsg{})

	if a.spinning {
		t.Error("expected spinning=false after StreamDoneMsg")
	}
	if !a.input.enabled {
		t.Error("expected input enabled after StreamDoneMsg")
	}
	if a.status.text != "Ready" {
		t.Errorf("expected status=Ready, got %q", a.status.text)
	}
	// Note: flushChat may return nil if no messages are pending.
}

// ---------------------------------------------------------------------------
// Update: StreamErrorMsg state transitions
// ---------------------------------------------------------------------------
func TestAppUpdate_StreamErrorMsg(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	a.spinning = true
	a.input.SetEnabled(false)
	a.chat.StartStreaming()

	_, cmd := a.Update(StreamErrorMsg{Err: errors.New("timeout")})

	if a.spinning {
		t.Error("expected spinning=false after StreamErrorMsg")
	}
	if !a.input.enabled {
		t.Error("expected input enabled after StreamErrorMsg")
	}
	if a.status.text != "Ready" {
		t.Errorf("expected status=Ready, got %q", a.status.text)
	}
	if cmd == nil {
		t.Error("expected flush cmd after StreamErrorMsg")
	}
}

func TestAppUpdate_StreamErrorMsgWithNilErr(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	a.spinning = true
	a.input.SetEnabled(false)
	a.chat.StartStreaming()

	_, _ = a.Update(StreamErrorMsg{}) // Err is nil

	if a.spinning {
		t.Error("expected spinning=false after StreamErrorMsg with nil err")
	}
	if !a.input.enabled {
		t.Error("expected input enabled after StreamErrorMsg with nil err")
	}
	// Note: flushChat may return nil if no messages are pending.
}

// ---------------------------------------------------------------------------
// Update: CommandResultMsg with Quit
// ---------------------------------------------------------------------------
func TestAppUpdate_CommandResultMsgQuit(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.Update(CommandResultMsg{Quit: true})
	if cmd == nil {
		t.Error("expected quit cmd for CommandResultMsg with Quit=true")
	}
}

// ---------------------------------------------------------------------------
// Update: CommandResultMsg with text adds error
// ---------------------------------------------------------------------------
func TestAppUpdate_CommandResultMsgText(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.Update(CommandResultMsg{Text: "something happened"})
	if cmd == nil {
		t.Error("expected flush cmd for CommandResultMsg with text")
	}
}

// ---------------------------------------------------------------------------
// Update: WindowSizeMsg sets dimensions
// ---------------------------------------------------------------------------
func TestAppUpdate_WindowSizeMsg(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	_, cmd := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	if a.width != 120 {
		t.Errorf("expected width=120, got %d", a.width)
	}
	if a.height != 40 {
		t.Errorf("expected height=40, got %d", a.height)
	}
	if cmd != nil {
		t.Error("expected nil cmd for WindowSizeMsg (layout only)")
	}
}

// ---------------------------------------------------------------------------
// App: dismissSidePanel clears panel and restores state
// ---------------------------------------------------------------------------
func TestAppDismissSidePanel(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	a.sidePanel = NewSidePanel(a.styles, "btw", "/btw what?")
	a.sidePanel.content.WriteString("answer text")

	cmd := a.dismissSidePanel()

	if a.sidePanel != nil {
		t.Error("expected sidePanel to be nil after dismiss")
	}
	if a.sidePanelCancel != nil {
		t.Error("expected sidePanelCancel to be nil after dismiss")
	}
	if a.spinning {
		t.Error("expected spinning=false after dismiss when no main stream")
	}
	if !a.input.enabled {
		t.Error("expected input enabled after dismiss when no main stream")
	}
	if cmd == nil {
		t.Error("expected flush cmd after dismiss (chat has content)")
	}
}

// ---------------------------------------------------------------------------
// App: dismissSidePanel with im panel type
// ---------------------------------------------------------------------------
func TestAppDismissSidePanel_ImPanel(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	a.sidePanel = NewInteractiveSidePanel(a.styles, "im", "/im connect", "im-session-1")
	a.sidePanel.content.WriteString("connected")

	cmd := a.dismissSidePanel()

	if a.sidePanel != nil {
		t.Error("expected sidePanel to be nil after dismiss")
	}
	if cmd == nil {
		t.Error("expected flush cmd after dismiss (chat has content)")
	}
}

// ---------------------------------------------------------------------------
// App: dismissSidePanel with error
// ---------------------------------------------------------------------------
func TestAppDismissSidePanel_WithError(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	a.sidePanel = NewSidePanel(a.styles, "btw", "/btw question")
	a.sidePanel.content.WriteString("answer")
	a.sidePanel.SetError(errors.New("stream failed"))

	cmd := a.dismissSidePanel()

	if a.sidePanel != nil {
		t.Error("expected sidePanel to be nil after dismiss")
	}
	if cmd == nil {
		t.Error("expected flush cmd after dismiss with error")
	}
}

// ---------------------------------------------------------------------------
// App: dismissSidePanel nil panel returns nil cmd
// ---------------------------------------------------------------------------
func TestAppDismissSidePanel_NilPanel(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	cmd := a.dismissSidePanel()
	if cmd != nil {
		t.Error("expected nil cmd when dismissing nil side panel")
	}
}

// ---------------------------------------------------------------------------
// App: layout propagates width to components
// ---------------------------------------------------------------------------
func TestAppLayout(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	a.width = 100
	a.height = 50
	a.layout()

	if a.chat.width != 100 {
		t.Errorf("expected chat width=100, got %d", a.chat.width)
	}
	if a.input.width != 100 {
		t.Errorf("expected input width=100, got %d", a.input.width)
	}
	if a.status.width != 100 {
		t.Errorf("expected status width=100, got %d", a.status.width)
	}
}

// ---------------------------------------------------------------------------
// App: flushChat returns nil when no messages to flush
// ---------------------------------------------------------------------------
func TestAppFlushChat_NoMessages(t *testing.T) {
	t.Parallel()
	a := newTestApp()
	cmd := a.flushChat()
	if cmd != nil {
		t.Error("expected nil cmd when chat has no flushed messages")
	}
}

// ---------------------------------------------------------------------------
// formatTokenCount -- from status.go (related helper)
// ---------------------------------------------------------------------------
func TestFormatTokenCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		n    int
		want string
	}{
		{name: "zero", n: 0, want: "0"},
		{name: "small number", n: 42, want: "42"},
		{name: "thousands", n: 1500, want: "1.5k"},
		{name: "exact thousand", n: 1000, want: "1.0k"},
		{name: "millions", n: 2500000, want: "2.5M"},
		{name: "exact million", n: 1000000, want: "1.0M"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatTokenCount(tt.n)
			if got != tt.want {
				t.Errorf("formatTokenCount(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatCost -- from status.go (related helper)
// ---------------------------------------------------------------------------
func TestFormatCost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cost     float64
		currency string
		want     string
	}{
		{name: "USD over 1", cost: 1.50, currency: "USD", want: "$1.50"},
		{name: "USD medium", cost: 0.05, currency: "USD", want: "$0.050"},
		{name: "USD tiny", cost: 0.001, currency: "USD", want: "$0.0010"},
		{name: "CNY over 1", cost: 2.00, currency: "CNY", want: "¥2.00"},
		{name: "CNY medium", cost: 0.03, currency: "CNY", want: "¥0.030"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatCost(tt.cost, tt.currency)
			if got != tt.want {
				t.Errorf("formatCost(%v, %q) = %q, want %q", tt.cost, tt.currency, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// abbreviateModel -- from status.go (related helper)
// ---------------------------------------------------------------------------
func TestAbbreviateModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "short name unchanged", in: "claude-3", want: "claude-3"},
		{name: "name at max length unchanged", in: strings.Repeat("a", 24), want: strings.Repeat("a", 24)},
		{name: "long name truncated", in: strings.Repeat("a", 30), want: strings.Repeat("a", 21) + "…"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := abbreviateModel(tt.in)
			if got != tt.want {
				t.Errorf("abbreviateModel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Key message construction helpers
// ---------------------------------------------------------------------------
func ctrlCKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'c'}
}

func ctrlDKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'd'}
}

func enterKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyEnter}
}

func escKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyEsc}
}
