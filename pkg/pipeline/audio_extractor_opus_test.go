package pipeline

import (
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/pion/opus"
)

func TestIsOpusCodec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		codec string
		want  bool
	}{
		{"opus", core.CodecOpus, true},
		{"pcmu", core.CodecPCMU, false},
		{"pcma", core.CodecPCMA, false},
		{"aac", core.CodecAAC, false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isOpusCodec(tt.codec); got != tt.want {
				t.Errorf("isOpusCodec(%q) = %v, want %v", tt.codec, got, tt.want)
			}
		})
	}
}

func TestIsPureGoDecodable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		codec string
		want  bool
	}{
		{"pcmu", core.CodecPCMU, true},
		{"pcma", core.CodecPCMA, true},
		{"pcm", core.CodecPCM, true},
		{"pcml", core.CodecPCML, true},
		{"opus", core.CodecOpus, true},
		{"aac", core.CodecAAC, false},
		{"g722", core.CodecG722, false},
		{"mp3", core.CodecMP3, false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isPureGoDecodable(tt.codec); got != tt.want {
				t.Errorf("isPureGoDecodable(%q) = %v, want %v", tt.codec, got, tt.want)
			}
		})
	}
}

func TestCodecSelectionPriority(t *testing.T) {
	t.Parallel()

	// Simulate the codec selection logic from startGo2RTC.
	// Priority: PCM > Opus > unsupported (ffmpeg fallback).
	selectCodec := func(codecs []string) string {
		var opusFallback string
		var otherFallback string
		for _, c := range codecs {
			if isPCMCompatible(c) {
				return c
			}
			if isOpusCodec(c) && opusFallback == "" {
				opusFallback = c
			} else if otherFallback == "" {
				otherFallback = c
			}
		}
		if opusFallback != "" {
			return opusFallback
		}
		return otherFallback
	}

	tests := []struct {
		name   string
		codecs []string
		want   string
	}{
		{"pcmu only", []string{core.CodecPCMU}, core.CodecPCMU},
		{"opus only", []string{core.CodecOpus}, core.CodecOpus},
		{"aac only", []string{core.CodecAAC}, core.CodecAAC},
		{"pcmu beats opus", []string{core.CodecOpus, core.CodecPCMU}, core.CodecPCMU},
		{"opus beats aac", []string{core.CodecAAC, core.CodecOpus}, core.CodecOpus},
		{"pcma beats all", []string{core.CodecAAC, core.CodecOpus, core.CodecPCMA}, core.CodecPCMA},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := selectCodec(tt.codecs); got != tt.want {
				t.Errorf("selectCodec(%v) = %q, want %q", tt.codecs, got, tt.want)
			}
		})
	}
}

func TestOpusDecoderCreation(t *testing.T) {
	t.Parallel()

	// Verify pion/opus decoder can be created with our target config:
	// 16kHz mono (optimal for Whisper).
	decoder, err := opus.NewDecoderWithOutput(16000, 1)
	if err != nil {
		t.Fatalf("NewDecoderWithOutput(16000, 1) failed: %v", err)
	}

	// Decode an empty/invalid packet should return error, not panic.
	out := make([]int16, 1920)
	_, err = decoder.DecodeToInt16([]byte{}, out)
	if err == nil {
		t.Error("expected error decoding empty packet")
	}

	// Single zero byte (invalid but should not panic).
	_, err = decoder.DecodeToInt16([]byte{0x00}, out)
	// Error is expected — just verifying no panic.
	_ = err
}

func TestOpusDecoderSupportedSampleRates(t *testing.T) {
	t.Parallel()

	// All sample rates that pion/opus supports.
	rates := []int{8000, 12000, 16000, 24000, 48000}
	for _, rate := range rates {
		_, err := opus.NewDecoderWithOutput(rate, 1)
		if err != nil {
			t.Errorf("NewDecoderWithOutput(%d, 1) failed: %v", rate, err)
		}
	}

	// Unsupported sample rate should fail.
	_, err := opus.NewDecoderWithOutput(44100, 1)
	if err == nil {
		t.Error("expected error for unsupported sample rate 44100")
	}
}
