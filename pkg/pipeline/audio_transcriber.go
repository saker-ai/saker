package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// TranscribeFunc transcribes an audio file at the given path and returns the
// text. This abstraction avoids importing pkg/tool into pkg/pipeline.
type TranscribeFunc func(ctx context.Context, audioPath string) (string, error)

// TranscriptEntry holds a single transcription result.
type TranscriptEntry struct {
	Text      string
	Timestamp time.Duration
	ChunkIdx  int
}

// AudioTranscriber continuously transcribes audio chunks from an AudioExtractor
// and maintains a sliding window of recent transcription results.
type AudioTranscriber struct {
	transcribe TranscribeFunc
	extractor  *AudioExtractor
	maxWindow  int
	timeout    time.Duration

	mu     sync.RWMutex
	recent []TranscriptEntry
	cancel context.CancelFunc
	done   chan struct{}
}

// NewAudioTranscriber creates a transcriber that reads chunks from the extractor
// and transcribes them using the provided function.
// windowSize controls how many recent transcriptions are kept (default: 5).
func NewAudioTranscriber(transcribe TranscribeFunc, extractor *AudioExtractor, windowSize int) *AudioTranscriber {
	if windowSize <= 0 {
		windowSize = 5
	}
	return &AudioTranscriber{
		transcribe: transcribe,
		extractor:  extractor,
		maxWindow:  windowSize,
		timeout:    30 * time.Second,
		done:       make(chan struct{}),
	}
}

// Run starts the transcription loop. It blocks until the context is cancelled
// or the extractor is exhausted. Call this in a goroutine.
func (at *AudioTranscriber) Run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	at.mu.Lock()
	at.cancel = cancel
	at.mu.Unlock()

	defer func() {
		cancel()
		close(at.done)
	}()

	for {
		chunk, err := at.extractor.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return
			}
			slog.Error("audio_transcriber: extractor error", "error", err)
			return
		}

		// Transcribe with a per-chunk timeout.
		transcribeCtx, transcribeCancel := context.WithTimeout(ctx, at.timeout)
		text, err := at.transcribe(transcribeCtx, chunk.Path)
		transcribeCancel()

		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("audio_transcriber: transcription failed",
				"chunk", chunk.Index, "error", err)
			continue
		}

		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}

		entry := TranscriptEntry{
			Text:      text,
			Timestamp: chunk.Timestamp,
			ChunkIdx:  chunk.Index,
		}

		at.mu.Lock()
		at.recent = append(at.recent, entry)
		if len(at.recent) > at.maxWindow {
			at.recent = at.recent[len(at.recent)-at.maxWindow:]
		}
		at.mu.Unlock()

		slog.Debug("audio_transcriber: transcribed chunk",
			"chunk", chunk.Index, "text_len", len(text))
	}
}

// RecentTranscript returns the concatenated text from the sliding window
// of recent transcriptions, formatted with timestamps.
func (at *AudioTranscriber) RecentTranscript() string {
	at.mu.RLock()
	defer at.mu.RUnlock()

	if len(at.recent) == 0 {
		return ""
	}

	var b strings.Builder
	for _, entry := range at.recent {
		ts := entry.Timestamp.Round(time.Second)
		fmt.Fprintf(&b, "[%s] %s\n", ts, entry.Text)
	}
	return strings.TrimSpace(b.String())
}

// Close stops the transcriber and waits for the run loop to finish.
// Safe to call even if Run() was never started.
func (at *AudioTranscriber) Close() {
	at.mu.RLock()
	cancel := at.cancel
	at.mu.RUnlock()

	if cancel != nil {
		cancel()
		<-at.done
	}
	// If cancel is nil, Run() was never called — nothing to wait for.
}
