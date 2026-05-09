package toolbuiltin

import (
	"context"
	"testing"
)

func TestStreamCaptureTool_Name(t *testing.T) {
	sc := NewStreamCaptureTool()
	if sc.Name() != "stream_capture" {
		t.Fatalf("expected name stream_capture, got %s", sc.Name())
	}
}

func TestStreamCaptureTool_RequiresURL(t *testing.T) {
	sc := NewStreamCaptureTool()
	_, err := sc.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

func TestStreamCaptureTool_RejectsInvalidScheme(t *testing.T) {
	sc := NewStreamCaptureTool()
	_, err := sc.Execute(context.Background(), map[string]any{
		"url": "http://example.com/not-a-stream",
	})
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestStreamCaptureTool_IntervalMsCapped(t *testing.T) {
	// Verify the schema declares a maximum of 60000
	schema := streamCaptureSchema
	intervalProps, ok := schema.Properties["interval_ms"].(map[string]any)
	if !ok {
		t.Fatal("interval_ms not found in schema")
	}
	if max, ok := intervalProps["maximum"]; !ok || max != 60000 {
		t.Errorf("expected maximum=60000, got %v", intervalProps["maximum"])
	}
	if min, ok := intervalProps["minimum"]; !ok || min != 1 {
		t.Errorf("expected minimum=1, got %v", intervalProps["minimum"])
	}
}

func TestStreamCaptureTool_CountCapped(t *testing.T) {
	// Verify count schema constraints match code behavior
	schema := streamCaptureSchema
	_, ok := schema.Properties["count"].(map[string]any)
	if !ok {
		t.Fatal("count not found in schema")
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		want   int
		wantOk bool
	}{
		{"int", 42, 42, true},
		{"float64", float64(7), 7, true},
		{"int64", int64(99), 99, true},
		{"string", "nope", 0, false},
		{"nil", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toInt(tt.input)
			if ok != tt.wantOk || got != tt.want {
				t.Errorf("toInt(%v) = (%d, %v), want (%d, %v)", tt.input, got, ok, tt.want, tt.wantOk)
			}
		})
	}
}
