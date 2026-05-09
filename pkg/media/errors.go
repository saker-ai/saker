package media

import "errors"

var (
	// ErrNoFFmpeg indicates ffmpeg/ffprobe is not installed.
	ErrNoFFmpeg = errors.New("media: ffmpeg not found in PATH")

	// ErrChunkTooSmall indicates a video chunk is too small to process.
	ErrChunkTooSmall = errors.New("media: chunk too small to embed")

	// ErrIndexNotFound indicates no index exists at the expected path.
	ErrIndexNotFound = errors.New("media: index not found")

	// ErrNoResults indicates a search returned zero results.
	ErrNoResults = errors.New("media: no results found")

	// ErrUnsupportedFormat indicates an unsupported video format.
	ErrUnsupportedFormat = errors.New("media: unsupported video format")

	// ErrAPIKeyMissing indicates a required API key is not configured.
	ErrAPIKeyMissing = errors.New("media: API key not configured")

	// ErrQuotaExceeded indicates API rate limit was exceeded.
	ErrQuotaExceeded = errors.New("media: API quota exceeded")
)
