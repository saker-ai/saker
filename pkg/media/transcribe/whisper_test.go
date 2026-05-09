package transcribe

import (
	"context"
	"testing"
)

func TestWhisperBinaries(t *testing.T) {
	t.Parallel()
	expected := []string{"whisper", "whisper-cli", "whisper.cpp"}
	if len(whisperBinaries) != len(expected) {
		t.Fatalf("whisperBinaries has %d entries, expected %d", len(whisperBinaries), len(expected))
	}
	for i, name := range expected {
		if whisperBinaries[i] != name {
			t.Errorf("whisperBinaries[%d] = %q, want %q", i, whisperBinaries[i], name)
		}
	}
}

func TestWhisperAvailableCached(t *testing.T) {
	// WhisperAvailable uses sync.Once, so the result is cached.
	// We verify it returns the same value on successive calls.
	result := WhisperAvailable()
	result2 := WhisperAvailable()
	if result2 != result {
		t.Errorf("WhisperAvailable not cached: first=%q second=%q", result, result2)
	}
}

func TestWhisperTranscribeNoBinaryAvailable(t *testing.T) {
	bin := WhisperAvailable()
	if bin != "" {
		t.Skipf("whisper binary %q found in PATH; cannot test no-binary scenario", bin)
	}

	_, err := WhisperTranscribe(context.Background(), "audio.wav")
	if err == nil {
		t.Error("expected error when no whisper binary found")
	}
}

func TestWhisperTranscribeWithBinary(t *testing.T) {
	bin := WhisperAvailable()
	if bin == "" {
		t.Skip("whisper binary not available in PATH")
	}

	// With a binary available but nonexistent audio path, the command should fail.
	_, err := WhisperTranscribe(context.Background(), "/nonexistent/audio.wav")
	if err == nil {
		t.Error("expected error for nonexistent audio file")
	}
}