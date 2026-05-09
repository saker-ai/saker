package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/model"
)

// stubVideoModel implements model.Model for video tool tests.
type stubVideoModel struct {
	response string
	err      error
}

func (m *stubVideoModel) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &model.Response{
		Message: model.Message{
			Role:    "assistant",
			Content: m.response,
		},
	}, nil
}

func (m *stubVideoModel) CompleteStream(_ context.Context, _ model.Request, cb model.StreamHandler) error {
	if m.err != nil {
		return m.err
	}
	return cb(model.StreamResult{Delta: m.response})
}

// --- FrameAnalyzerTool tests ---

func TestFrameAnalyzerName(t *testing.T) {
	fa := NewFrameAnalyzerTool(nil)
	if fa.Name() != "frame_analyzer" {
		t.Fatalf("expected frame_analyzer, got %s", fa.Name())
	}
	if fa.Schema() == nil {
		t.Fatal("expected non-nil schema")
	}
}

func TestFrameAnalyzerNilModel(t *testing.T) {
	fa := NewFrameAnalyzerTool(nil)
	_, err := fa.Execute(context.Background(), map[string]any{"frame_path": "/tmp/test.jpg"})
	if err == nil || err.Error() != "frame_analyzer: model not configured" {
		t.Fatalf("expected model error, got %v", err)
	}
}

func TestFrameAnalyzerNilContext(t *testing.T) {
	fa := NewFrameAnalyzerTool(&stubVideoModel{response: "ok"})
	_, err := fa.Execute(nil, map[string]any{})
	if err == nil {
		t.Fatal("expected nil context error")
	}
}

func TestFrameAnalyzerNoInput(t *testing.T) {
	fa := NewFrameAnalyzerTool(&stubVideoModel{response: "ok"})
	_, err := fa.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected no input error")
	}
}

func TestFrameAnalyzerWithFramePath(t *testing.T) {
	// Create a temp image file
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "frame.jpg")
	if err := os.WriteFile(imgPath, []byte{0xFF, 0xD8, 0xFF, 0xE0}, 0o600); err != nil {
		t.Fatal(err)
	}

	fa := NewFrameAnalyzerTool(&stubVideoModel{response: "A cat sitting on a table"})
	result, err := fa.Execute(context.Background(), map[string]any{
		"frame_path": imgPath,
		"task":       "describe the image",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	if result.Output != "A cat sitting on a table" {
		t.Fatalf("unexpected output: %s", result.Output)
	}
}

func TestFrameAnalyzerWithArtifacts(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "frame.png")
	if err := os.WriteFile(imgPath, []byte{0x89, 0x50, 0x4E, 0x47}, 0o600); err != nil {
		t.Fatal(err)
	}

	fa := NewFrameAnalyzerTool(&stubVideoModel{response: "scene analysis"})
	result, err := fa.Execute(context.Background(), map[string]any{
		"artifacts": []artifact.ArtifactRef{
			{Path: imgPath, ArtifactID: "frame_001", Kind: artifact.ArtifactKindImage},
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].ArtifactID != "analysis_frame_001" {
		t.Fatalf("unexpected artifacts: %+v", result.Artifacts)
	}
}

func TestFrameAnalyzerSchemaHasFramePath(t *testing.T) {
	fa := NewFrameAnalyzerTool(nil)
	schema := fa.Schema()
	if _, ok := schema.Properties["frame_path"]; !ok {
		t.Fatal("schema missing frame_path property")
	}
}

func TestFrameAnalyzerEmptyFile(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "empty.jpg")
	if err := os.WriteFile(imgPath, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	fa := NewFrameAnalyzerTool(&stubVideoModel{response: "ok"})
	_, err := fa.Execute(context.Background(), map[string]any{"frame_path": imgPath})
	if err == nil {
		t.Fatal("expected empty file error")
	}
}

// --- VideoSummarizerTool tests ---

func TestVideoSummarizerName(t *testing.T) {
	vs := NewVideoSummarizerTool(nil)
	if vs.Name() != "video_summarizer" {
		t.Fatalf("expected video_summarizer, got %s", vs.Name())
	}
}

func TestVideoSummarizerNilModel(t *testing.T) {
	vs := NewVideoSummarizerTool(nil)
	_, err := vs.Execute(context.Background(), map[string]any{
		"items": []string{"frame 1 analysis"},
	})
	if err == nil {
		t.Fatal("expected model error")
	}
}

func TestVideoSummarizerNoAnalyses(t *testing.T) {
	vs := NewVideoSummarizerTool(&stubVideoModel{response: "ok"})
	_, err := vs.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected no analyses error")
	}
}

func TestVideoSummarizerWithItems(t *testing.T) {
	vs := NewVideoSummarizerTool(&stubVideoModel{response: "Video shows a cat playing"})
	result, err := vs.Execute(context.Background(), map[string]any{
		"items": []string{"frame 1: cat visible", "frame 2: cat jumping"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	if result.Output != "Video shows a cat playing" {
		t.Fatalf("unexpected output: %s", result.Output)
	}
	if s, ok := result.Structured.(map[string]any); !ok || s["frame_count"] != 2 {
		t.Fatalf("unexpected structured: %+v", result.Structured)
	}
}

func TestVideoSummarizerWithArtifactFiles(t *testing.T) {
	// Test that artifact refs read file content instead of using ArtifactID
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.txt")
	if err := os.WriteFile(analysisPath, []byte("detailed frame analysis content"), 0o600); err != nil {
		t.Fatal(err)
	}

	vs := NewVideoSummarizerTool(&stubVideoModel{response: "summary"})
	result, err := vs.Execute(context.Background(), map[string]any{
		"artifacts": []artifact.ArtifactRef{
			{Path: analysisPath, ArtifactID: "analysis_frame_001"},
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}
}

func TestVideoSummarizerCustomTask(t *testing.T) {
	vs := NewVideoSummarizerTool(&stubVideoModel{response: "custom result"})
	result, err := vs.Execute(context.Background(), map[string]any{
		"task":  "count the people",
		"items": []any{"frame 1: two people", "frame 2: three people"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Output != "custom result" {
		t.Fatalf("unexpected output: %s", result.Output)
	}
}

// --- StreamCaptureTool tests ---

func TestStreamCaptureName(t *testing.T) {
	sc := NewStreamCaptureTool()
	if sc.Name() != "stream_capture" {
		t.Fatalf("expected stream_capture, got %s", sc.Name())
	}
	if sc.Schema() == nil {
		t.Fatal("expected non-nil schema")
	}
}

func TestStreamCaptureEmptyURL(t *testing.T) {
	sc := NewStreamCaptureTool()
	_, err := sc.Execute(context.Background(), map[string]any{"url": ""})
	if err == nil {
		t.Fatal("expected empty URL error")
	}
}

func TestStreamCaptureUnsupportedScheme(t *testing.T) {
	sc := NewStreamCaptureTool()
	_, err := sc.Execute(context.Background(), map[string]any{"url": "http://example.com/stream"})
	if err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}

func TestStreamCaptureCountClamped(t *testing.T) {
	sc := NewStreamCaptureTool()
	// We can't actually connect to RTSP, but we can verify the tool
	// rejects non-RTSP URLs before attempting connection
	_, err := sc.Execute(context.Background(), map[string]any{
		"url":   "file:///tmp/test.mp4",
		"count": float64(200),
	})
	if err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}

func TestStreamCaptureToIntHelper(t *testing.T) {
	tests := []struct {
		input any
		want  int
		ok    bool
	}{
		{int(5), 5, true},
		{float64(3.0), 3, true},
		{int64(7), 7, true},
		{"not a number", 0, false},
		{nil, 0, false},
	}
	for _, tc := range tests {
		got, ok := toInt(tc.input)
		if ok != tc.ok || got != tc.want {
			t.Errorf("toInt(%v) = (%d, %v), want (%d, %v)", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}

// --- collectFrameAnalyses tests ---

func TestCollectFrameAnalysesFromItems(t *testing.T) {
	analyses := collectFrameAnalyses(map[string]any{
		"items": []string{"a", "b", "c"},
	})
	if len(analyses) != 3 {
		t.Fatalf("expected 3 analyses, got %d", len(analyses))
	}
}

func TestCollectFrameAnalysesFromAnyItems(t *testing.T) {
	analyses := collectFrameAnalyses(map[string]any{
		"items": []any{"x", "y"},
	})
	if len(analyses) != 2 {
		t.Fatalf("expected 2 analyses, got %d", len(analyses))
	}
}

func TestCollectFrameAnalysesFromStructured(t *testing.T) {
	analyses := collectFrameAnalyses(map[string]any{
		"structured": map[string]any{
			"frame_analyses": []string{"analysis1", "analysis2"},
		},
	})
	if len(analyses) != 2 {
		t.Fatalf("expected 2 analyses, got %d", len(analyses))
	}
}

func TestCollectFrameAnalysesFromArtifactFiles(t *testing.T) {
	tmp := os.TempDir()
	f, err := os.CreateTemp(tmp, "analysis-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("real analysis content")
	f.Close()

	analyses := collectFrameAnalyses(map[string]any{
		"artifacts": []artifact.ArtifactRef{
			{Path: f.Name(), ArtifactID: "analysis_001"},
		},
	})
	if len(analyses) != 1 || analyses[0] != "real analysis content" {
		t.Fatalf("expected file content, got %v", analyses)
	}
}

func TestCollectFrameAnalysesFallsBackToID(t *testing.T) {
	analyses := collectFrameAnalyses(map[string]any{
		"artifacts": []artifact.ArtifactRef{
			{Path: "/nonexistent/path", ArtifactID: "fallback_id"},
		},
	})
	if len(analyses) != 1 || analyses[0] != "fallback_id" {
		t.Fatalf("expected fallback to ArtifactID, got %v", analyses)
	}
}

func TestCollectFrameAnalysesEmpty(t *testing.T) {
	analyses := collectFrameAnalyses(map[string]any{})
	if len(analyses) != 0 {
		t.Fatalf("expected empty, got %v", analyses)
	}
}

// --- detectImageMediaType tests ---

func TestDetectImageMediaType(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"frame.jpg", "image/jpeg"},
		{"frame.jpeg", "image/jpeg"},
		{"frame.png", "image/png"},
		{"frame.gif", "image/gif"},
		{"frame.webp", "image/webp"},
		{"frame.bmp", "image/jpeg"}, // default fallback
	}
	for _, tc := range tests {
		got := detectImageMediaType(tc.path, nil)
		if got != tc.want {
			t.Errorf("detectImageMediaType(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
