package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// audio_extractor.go is the public surface for the audio extraction pipeline.
// Implementation is split across:
//   - audio_extractor_pcm.go: pure-Go go2rtc connection, PCM/Opus chunking,
//     WAV serialisation, and the shared pcmChunker helper.
//   - audio_extractor_ffmpeg.go: ffmpeg fallback for codecs (e.g. AAC) that
//     can't be decoded in pure Go, plus its bounded stderr buffer.

// ErrCodecNotSupported is returned when the audio codec cannot be decoded
// in pure Go and requires an external tool (ffmpeg).
var ErrCodecNotSupported = errors.New("audio codec not supported for pure Go decoding")

// AudioChunk represents a segment of extracted audio saved as a WAV file.
type AudioChunk struct {
	Path      string        // WAV file path
	Index     int           // chunk sequence number
	Timestamp time.Duration // estimated timestamp from stream start
}

// AudioExtractorOptions configures the AudioExtractor.
type AudioExtractorOptions struct {
	// Interval is the duration of each audio chunk (default: 5s).
	Interval time.Duration
	// ConnectTimeout is the maximum time to establish the stream connection.
	ConnectTimeout time.Duration
	// HTTPClient is used for HLS playlist fetches. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// AudioExtractor extracts periodic audio chunks from a stream URL as WAV files.
//
// Primary path (pure Go): uses go2rtc audio track for PCM-compatible codecs
// (PCMA, PCMU, PCM, PCML) via pcm.Transcode, and Opus via pion/opus decoder.
//
// Fallback path: for AAC and other unsupported codecs, uses ffmpeg.
type AudioExtractor struct {
	url            string
	interval       time.Duration
	connectTimeout time.Duration
	httpClient     *http.Client

	mu      sync.Mutex
	tmpDir  string
	audioCh chan AudioChunk
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
}

// NewAudioExtractor creates an audio extractor for the given stream URL.
func NewAudioExtractor(url string, opts AudioExtractorOptions) *AudioExtractor {
	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	connTimeout := opts.ConnectTimeout
	if connTimeout <= 0 {
		connTimeout = DefaultConnectTimeout
	}
	return &AudioExtractor{
		url:            url,
		interval:       interval,
		connectTimeout: connTimeout,
		httpClient:     opts.HTTPClient,
	}
}

// Start begins audio extraction in the background. Returns an error if
// the stream cannot be connected or no audio track is found.
func (a *AudioExtractor) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return fmt.Errorf("audio_extractor: already started")
	}

	tmpDir, err := os.MkdirTemp("", "saker-audio-*")
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("audio_extractor: create temp dir: %w", err)
	}
	a.tmpDir = tmpDir
	a.audioCh = make(chan AudioChunk, 8)
	a.done = make(chan struct{})

	extractCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.started = true
	a.mu.Unlock()

	// Try pure Go path first, fall back to ffmpeg.
	err = a.startGo2RTC(extractCtx)
	if errors.Is(err, ErrCodecNotSupported) {
		slog.Info("audio_extractor: codec not supported for pure Go, trying ffmpeg fallback",
			"url", a.url)
		err = a.startFFmpeg(extractCtx)
	}
	if err != nil {
		cancel()
		close(a.audioCh)
		close(a.done)
		os.RemoveAll(tmpDir)
		return err
	}

	return nil
}

// Next blocks until the next audio chunk is available or the context is cancelled.
// Returns io.EOF when the extractor is done.
func (a *AudioExtractor) Next(ctx context.Context) (AudioChunk, error) {
	select {
	case <-ctx.Done():
		return AudioChunk{}, ctx.Err()
	case chunk, ok := <-a.audioCh:
		if !ok {
			return AudioChunk{}, io.EOF
		}
		return chunk, nil
	}
}

// Close stops the audio extractor and cleans up temporary files.
func (a *AudioExtractor) Close() error {
	a.mu.Lock()
	cancel := a.cancel
	dir := a.tmpDir
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Wait for background goroutine to finish.
	if a.done != nil {
		<-a.done
	}

	if dir != "" {
		return os.RemoveAll(dir)
	}
	return nil
}
