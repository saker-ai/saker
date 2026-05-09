package pipeline_test

import (
	"testing"

	"github.com/cinience/saker/pkg/pipeline"
)

func TestGo2RTCStreamSourceCreation(t *testing.T) {
	src := pipeline.NewGo2RTCStreamSource("rtsp://example.com/stream", pipeline.Go2RTCSourceOptions{
		SampleRate: 5,
		BufferSize: 16,
	})
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	if src.Done() {
		t.Error("new source should not be done")
	}
}

func TestGo2RTCStreamSourceDefaults(t *testing.T) {
	src := pipeline.NewGo2RTCStreamSource("rtsp://example.com/stream", pipeline.Go2RTCSourceOptions{})
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	// Should not panic with zero-value options
	if src.Done() {
		t.Error("new source should not be done")
	}
}

func TestGo2RTCStreamSourceClose(t *testing.T) {
	src := pipeline.NewGo2RTCStreamSource("rtsp://example.com/stream", pipeline.Go2RTCSourceOptions{})
	// Close before any Next() call should not error
	if err := src.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if !src.Done() {
		t.Error("closed source should be done")
	}
}

func TestGo2RTCStreamSourceCloseIdempotent(t *testing.T) {
	src := pipeline.NewGo2RTCStreamSource("rtsp://example.com/stream", pipeline.Go2RTCSourceOptions{})
	_ = src.Close()
	if err := src.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestIsStreamScheme(t *testing.T) {
	tests := []struct {
		uri  string
		want bool
	}{
		// RTSP
		{"rtsp://host/stream", true},
		{"RTSP://HOST/STREAM", true},
		// RTMP
		{"rtmp://host/app/stream", true},
		{"RTMP://host/app/stream", true},
		// ONVIF
		{"onvif://admin:pass@192.168.1.100", true},
		{"ONVIF://host:8080/onvif/device_service", true},
		{"onvif://host", true},
		// HLS
		{"http://example.com/live/stream.m3u8", true},
		{"https://cdn.example.com/path/playlist.m3u8", true},
		{"https://cdn.example.com/path/playlist.m3u8?token=abc", true},
		{"HTTP://EXAMPLE.COM/LIVE.M3U8", true},
		// Non-stream URLs
		{"http://example.com", false},
		{"http://example.com/page.html", false},
		{"https://example.com/api/data", false},
		{"watch:/tmp/frames", false},
		{"/path/to/video.mp4", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := pipeline.IsStreamScheme(tt.uri); got != tt.want {
			t.Errorf("IsStreamScheme(%q) = %v, want %v", tt.uri, got, tt.want)
		}
	}
}

// TestIsGo2RTCScheme verifies the deprecated alias still works.
func TestIsGo2RTCScheme(t *testing.T) {
	if !pipeline.IsGo2RTCScheme("rtsp://host/stream") {
		t.Error("IsGo2RTCScheme should accept rtsp://")
	}
	if !pipeline.IsGo2RTCScheme("onvif://host") {
		t.Error("IsGo2RTCScheme (alias) should accept onvif://")
	}
	if pipeline.IsGo2RTCScheme("http://example.com") {
		t.Error("IsGo2RTCScheme should reject plain http://")
	}
}
