package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// audio_extractor_ffmpeg.go is the fallback path used when the source codec
// (e.g. AAC) cannot be decoded by the pure-Go pipeline in
// audio_extractor_pcm.go. It shells out to ffmpeg, captures bounded stderr,
// and surfaces produced WAV chunks via the same audio channel.

// startFFmpeg uses ffmpeg to extract audio segments as WAV files.
// This is the fallback path for codecs that cannot be decoded in pure Go (AAC).
func (a *AudioExtractor) startFFmpeg(ctx context.Context) error {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("audio_extractor: audio codec requires ffmpeg but ffmpeg not found: %w", err)
	}

	intervalSecs := fmt.Sprintf("%d", int(a.interval.Seconds()))
	if a.interval.Seconds() < 1 {
		intervalSecs = "1"
	}

	outPattern := filepath.Join(a.tmpDir, "chunk_%03d.wav")
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-i", a.url,
		"-f", "segment",
		"-segment_time", intervalSecs,
		"-ac", "1",
		"-ar", "16000",
		"-acodec", "pcm_s16le",
		"-y", outPattern,
	)

	// Capture stderr so ffmpeg connection/decoding errors are logged.
	stderrBuf := &limitedBuffer{max: 4096}
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("audio_extractor: start ffmpeg: %w", err)
	}

	slog.Info("audio_extractor: started ffmpeg extraction",
		"url", a.url, "interval", a.interval)

	go a.watchFFmpegOutput(ctx, cmd, stderrBuf)
	return nil
}

// limitedBuffer is a bytes.Buffer that stops growing after max bytes.
// Used to capture ffmpeg stderr without unbounded memory growth.
type limitedBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	remaining := b.max - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	b.mu.Unlock()
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

// watchFFmpegOutput monitors the output directory for new WAV files produced
// by ffmpeg and sends them to the audioCh channel. It also detects early
// ffmpeg exit (e.g. connection failure) and logs the stderr output.
func (a *AudioExtractor) watchFFmpegOutput(ctx context.Context, cmd *exec.Cmd, stderrBuf *limitedBuffer) {
	defer func() {
		close(a.audioCh)
		close(a.done)
	}()

	// Monitor ffmpeg process exit in background.
	ffmpegDone := make(chan error, 1)
	go func() {
		ffmpegDone <- cmd.Wait()
	}()

	seen := make(map[string]struct{})
	chunkIdx := 0
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-ffmpegDone // wait for process to exit
			return
		case err := <-ffmpegDone:
			// ffmpeg exited — log stderr and return.
			if err != nil {
				stderr := strings.TrimSpace(stderrBuf.String())
				if stderr != "" {
					slog.Error("audio_extractor: ffmpeg exited with error",
						"url", a.url, "error", err, "stderr", stderr)
				} else {
					slog.Error("audio_extractor: ffmpeg exited with error",
						"url", a.url, "error", err)
				}
			} else if chunkIdx == 0 {
				slog.Warn("audio_extractor: ffmpeg exited without producing audio",
					"url", a.url)
			}
			return
		case <-ticker.C:
			entries, err := os.ReadDir(a.tmpDir)
			if err != nil {
				continue
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Name() < entries[j].Name()
			})

			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".wav") {
					continue
				}
				path := filepath.Join(a.tmpDir, e.Name())
				if _, ok := seen[path]; ok {
					continue
				}
				// Wait for file to be at least partially written (>1KB).
				info, err := e.Info()
				if err != nil || info.Size() < 1024 {
					continue
				}
				seen[path] = struct{}{}
				ts := time.Duration(chunkIdx) * a.interval
				select {
				case a.audioCh <- AudioChunk{Path: path, Index: chunkIdx, Timestamp: ts}:
					chunkIdx++
				case <-ctx.Done():
					_ = cmd.Process.Kill()
					<-ffmpegDone
					return
				}
			}
		}
	}
}
