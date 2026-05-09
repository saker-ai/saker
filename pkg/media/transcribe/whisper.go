// Package transcribe provides audio transcription via external ASR tools.
package transcribe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// whisperBinaries lists known whisper CLI binary names in preference order.
var whisperBinaries = []string{"whisper", "whisper-cli", "whisper.cpp"}

// Cached whisper binary lookup.
var (
	whisperOnce sync.Once
	whisperBin  string
)

// WhisperAvailable checks if any whisper CLI binary is available in PATH.
// The result is cached after the first call.
func WhisperAvailable() string {
	whisperOnce.Do(func() {
		for _, bin := range whisperBinaries {
			if _, err := exec.LookPath(bin); err == nil {
				whisperBin = bin
				return
			}
		}
	})
	return whisperBin
}

// WhisperTranscribe transcribes an audio file using the whisper CLI.
// Returns the transcribed text. Compatible with the TranscribeFunc signature
// used by AnalyzeVideoTool.
func WhisperTranscribe(ctx context.Context, audioPath string) (string, error) {
	bin := WhisperAvailable()
	if bin == "" {
		return "", fmt.Errorf("transcribe: no whisper binary found in PATH")
	}

	outDir, err := os.MkdirTemp("", "whisper-out-*")
	if err != nil {
		return "", fmt.Errorf("transcribe: create temp dir: %w", err)
	}
	defer os.RemoveAll(outDir)

	args := []string{
		audioPath,
		"--model", "small",
		"--language", "auto",
		"--output_format", "txt",
		"--output_dir", outDir,
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("transcribe: whisper failed: %w\n%s", err, string(out))
	}

	// Whisper outputs <basename>.txt in the output directory.
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	txtPath := filepath.Join(outDir, base+".txt")
	data, err := os.ReadFile(txtPath)
	if err != nil {
		// Fallback: try reading any .txt file in the output dir.
		entries, _ := os.ReadDir(outDir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".txt") {
				data, err = os.ReadFile(filepath.Join(outDir, e.Name()))
				if err == nil {
					break
				}
			}
		}
		if err != nil {
			return "", fmt.Errorf("transcribe: read output: %w", err)
		}
	}

	return strings.TrimSpace(string(data)), nil
}
