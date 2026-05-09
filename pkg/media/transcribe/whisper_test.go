package transcribe

import (
	"os"
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
	// WhisperAvailable uses sync.Once, so the first call determines the result.
	// We just verify it doesn't panic and returns a string.
	result := WhisperAvailable()
	// Result is either empty (no whisper binary) or a binary name.
	// Both are valid outcomes depending on the test environment.
	if result != "" {
		// If a whisper binary is found, it should be one of the known names.
		found := false
		for _, name := range whisperBinaries {
			if result == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("WhisperAvailable returned %q which is not a known binary", result)
		}
	}
	// Calling again should return the same cached result.
	result2 := WhisperAvailable()
	if result2 != result {
		t.Errorf("WhisperAvailable not cached: first=%q second=%q", result, result2)
	}
}

func TestWhisperTranscribeNoBinary(t *testing.T) {
	t.Parallel()
	// Reset the cached whisper binary for this test.
	origOnce := whisperOnce
	origBin := whisperBin
	whisperOnce = sync.Once{} // force re-evaluation
	whisperBin = ""            // simulate no binary found
	defer func() {
		whisperOnce = origOnce
		whisperBin = origBin
	}()

	// Override LookPath to simulate no whisper available.
	origLookPath := execLookPath
	execLookPath = func(_ string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	defer func() { execLookPath = origLookPath }()

	_, err := WhisperTranscribe(context.Background(), "audio.wav")
	if err == nil {
		t.Error("expected error when no whisper binary found")
	}
	if !strings.Contains(err.Error(), "no whisper binary") {
		t.Errorf("error = %q, want it to contain 'no whisper binary'", err.Error())
	}
}

func TestWhisperTranscribeEmptyPath(t *testing.T) {
	t.Parallel()
	// Even if whisper is available, an empty audio path should fail.
	// This test only runs if whisper is actually available in PATH.
	if WhisperAvailable() == "" {
		t.Skip("whisper binary not available")
	}
	_, err := WhisperTranscribe(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty audio path")
	}
}