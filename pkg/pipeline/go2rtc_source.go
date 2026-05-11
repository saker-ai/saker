package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/hls"
	"github.com/AlexxIT/go2rtc/pkg/mjpeg"
	"github.com/AlexxIT/go2rtc/pkg/onvif"
	"github.com/AlexxIT/go2rtc/pkg/rtsp"
	"github.com/cinience/saker/pkg/artifact"
)

// DefaultConnectTimeout is the default timeout for establishing a stream connection.
const DefaultConnectTimeout = 15 * time.Second

// streamProducer is the common interface satisfied by rtsp.Conn and
// mpegts.Producer (returned by hls.OpenURL). Both embed core.Connection
// which provides GetMedias, GetTrack, and Stop.
type streamProducer interface {
	GetMedias() []*core.Media
	GetTrack(media *core.Media, codec *core.Codec) (*core.Receiver, error)
	Stop() error
}

// Go2RTCSourceOptions configures the Go2RTC stream source.
type Go2RTCSourceOptions struct {
	// SampleRate keeps every Nth frame (default: 1 = every frame).
	SampleRate int
	// BufferSize is the channel buffer for decoded frames (default: 32).
	BufferSize int
	// ConnectTimeout is the maximum time to establish the stream connection.
	// Default: DefaultConnectTimeout (15s).
	ConnectTimeout time.Duration
	// HTTPClient is used for HLS playlist fetches. If nil, http.DefaultClient is used.
	// Inject an SSRF-safe client to prevent requests to private networks.
	HTTPClient *http.Client
}

// Go2RTCStreamSource implements StreamSource by connecting to a streaming
// protocol URL (RTSP, RTMP, HLS, ONVIF) via go2rtc and extracting JPEG frames.
type Go2RTCStreamSource struct {
	uri            string
	sampleRate     int
	bufferSize     int
	connectTimeout time.Duration
	httpClient     *http.Client

	mu      sync.Mutex
	tmpDir  string
	frameCh chan frameEntry
	done    bool
	started bool
	cancel  context.CancelFunc
	connErr error
}

type frameEntry struct {
	path string
	idx  int
}

// NewGo2RTCStreamSource creates a source from a streaming URL.
// Supported schemes: rtsp://, rtmp://, onvif://, http(s)://*.m3u8 (HLS).
func NewGo2RTCStreamSource(uri string, opts Go2RTCSourceOptions) *Go2RTCStreamSource {
	sampleRate := opts.SampleRate
	if sampleRate <= 0 {
		sampleRate = 1
	}
	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = 32
	}
	connTimeout := opts.ConnectTimeout
	if connTimeout <= 0 {
		connTimeout = DefaultConnectTimeout
	}
	return &Go2RTCStreamSource{
		uri:            uri,
		sampleRate:     sampleRate,
		bufferSize:     bufSize,
		connectTimeout: connTimeout,
		httpClient:     opts.HTTPClient,
	}
}

func (g *Go2RTCStreamSource) Next(ctx context.Context) (artifact.ArtifactRef, error) {
	g.mu.Lock()
	if g.done {
		g.mu.Unlock()
		return artifact.ArtifactRef{}, io.EOF
	}
	if !g.started {
		if err := g.startLocked(ctx); err != nil {
			g.done = true
			g.mu.Unlock()
			return artifact.ArtifactRef{}, fmt.Errorf("go2rtc connect: %w", err)
		}
		g.started = true
	}
	g.mu.Unlock()

	select {
	case <-ctx.Done():
		return artifact.ArtifactRef{}, ctx.Err()
	case entry, ok := <-g.frameCh:
		if !ok {
			g.mu.Lock()
			g.done = true
			err := g.connErr
			g.mu.Unlock()
			if err != nil {
				return artifact.ArtifactRef{}, err
			}
			return artifact.ArtifactRef{}, io.EOF
		}
		return artifact.ArtifactRef{
			Source:     artifact.ArtifactSourceGenerated,
			Path:       entry.path,
			ArtifactID: fmt.Sprintf("rtc_frame_%05d", entry.idx),
			Kind:       artifact.ArtifactKindImage,
		}, nil
	}
}

func (g *Go2RTCStreamSource) Done() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.done
}

func (g *Go2RTCStreamSource) Close() error {
	g.mu.Lock()
	g.done = true
	cancel := g.cancel
	dir := g.tmpDir
	g.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if dir != "" {
		return os.RemoveAll(dir)
	}
	return nil
}

// startLocked initialises the stream connection and starts the background
// frame extraction goroutine. Must be called with g.mu held.
func (g *Go2RTCStreamSource) startLocked(parentCtx context.Context) error {
	tmpDir, err := os.MkdirTemp("", "saker-go2rtc-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	g.tmpDir = tmpDir

	ctx, cancel := context.WithCancel(parentCtx)
	g.cancel = cancel
	g.frameCh = make(chan frameEntry, g.bufferSize)

	// Connect to stream with a dedicated timeout so connection setup
	// doesn't consume the entire capture budget.
	connectCtx, connectCancel := context.WithTimeout(ctx, g.connectTimeout)
	defer connectCancel()

	prod, err := g.connectProducer(connectCtx)
	if err != nil {
		cancel()
		return err
	}

	// Find video media and get track
	var videoMedia *core.Media
	var videoCodec *core.Codec
	for _, media := range prod.GetMedias() {
		if media.Kind != core.KindVideo {
			continue
		}
		if media.Direction != core.DirectionRecvonly {
			continue
		}
		videoMedia = media
		if len(media.Codecs) > 0 {
			videoCodec = media.Codecs[0]
		}
		break
	}
	if videoMedia == nil || videoCodec == nil {
		_ = prod.Stop()
		cancel()
		return fmt.Errorf("no video track found in %s", g.uri)
	}

	track, err := prod.GetTrack(videoMedia, videoCodec)
	if err != nil {
		_ = prod.Stop()
		cancel()
		return fmt.Errorf("get track: %w", err)
	}

	// Guard against malformed MJPEG RTP packets that crash go2rtc's
	// RTPDepay with "slice bounds out of range" (missing bounds check
	// in go2rtc v1.9.14 pkg/mjpeg/rtp.go). We wrap the track's Input
	// handler to drop packets too small for RFC 2435 parsing BEFORE
	// they reach the consumer's internal goroutine (where we cannot
	// recover from panics).
	if videoCodec.IsRTP() {
		origInput := track.Input
		track.Input = func(packet *core.Packet) {
			if !isValidMJPEGRTP(packet.Payload) {
				return
			}
			origInput(packet)
		}
	}

	// Create MJPEG consumer to decode frames
	consumer := mjpeg.NewConsumer()
	consumerMedias := consumer.GetMedias()
	if len(consumerMedias) == 0 {
		_ = prod.Stop()
		cancel()
		return fmt.Errorf("mjpeg consumer has no medias")
	}

	if err := consumer.AddTrack(consumerMedias[0], videoCodec, track); err != nil {
		_ = prod.Stop()
		cancel()
		return fmt.Errorf("add track: %w", err)
	}

	// For RTSP connections, start playback explicitly
	if rtspClient, ok := prod.(*rtsp.Conn); ok {
		go func() {
			if err := rtspClient.Play(); err != nil {
				g.mu.Lock()
				g.connErr = fmt.Errorf("play: %w", err)
				g.mu.Unlock()
				_ = rtspClient.Stop()
				return
			}
			_ = rtspClient.Start()
		}()
	}

	// Start frame extraction goroutine
	go g.extractFrames(ctx, consumer, prod, tmpDir)

	return nil
}

// connectProducer creates a streamProducer for the configured URI based on
// the URL scheme. The context controls the connection timeout.
func (g *Go2RTCStreamSource) connectProducer(ctx context.Context) (streamProducer, error) {
	return ConnectStreamProducer(ctx, g.uri, g.httpClient)
}

// ConnectStreamProducer creates a streamProducer for the given URI based on
// the URL scheme. The context controls the connection timeout.
// httpClient is used for HLS fetches; nil defaults to http.DefaultClient.
// This is a package-level function so that AudioExtractor can reuse it.
func ConnectStreamProducer(ctx context.Context, uri string, httpClient *http.Client) (streamProducer, error) {
	lower := strings.ToLower(uri)

	switch {
	case strings.HasPrefix(lower, "onvif://"):
		return connectONVIF(ctx, uri)
	case isHLSURL(lower):
		return connectHLS(ctx, uri, httpClient)
	default:
		// Treat as rtsp/rtmp scheme.
		return connectRTSP(ctx, uri)
	}
}

// connectRTSP connects to an RTSP or RTMP stream.
// go2rtc's rtsp.NewClient has a built-in 5s per-operation timeout but does
// not accept a context, so we wrap the blocking calls with context selection.
func connectRTSP(ctx context.Context, uri string) (*rtsp.Conn, error) {
	type result struct {
		conn *rtsp.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		client := rtsp.NewClient(uri)
		if err := client.Dial(); err != nil {
			ch <- result{err: fmt.Errorf("dial %s: %w", uri, err)}
			return
		}
		if err := client.Describe(); err != nil {
			_ = client.Stop()
			ch <- result{err: fmt.Errorf("describe %s: %w", uri, err)}
			return
		}
		ch <- result{conn: client}
	}()

	select {
	case <-ctx.Done():
		// Drain the channel to clean up any connection that completed after timeout.
		go func() {
			if r := <-ch; r.conn != nil {
				_ = r.conn.Stop()
			}
		}()
		return nil, fmt.Errorf("connect %s: %w", uri, ctx.Err())
	case r := <-ch:
		return r.conn, r.err
	}
}

// connectHLS connects to an HLS live stream by fetching the m3u8 playlist.
// Uses context-aware HTTP request to respect the connection timeout.
// The httpClient allows callers to inject an SSRF-safe client.
//
// When the playlist is a master playlist (#EXT-X-STREAM-INF), this function
// resolves and fetches the variant media playlist itself before handing it to
// go2rtc. This is necessary because go2rtc's internal HLS reader creates its
// own http.Client for segment fetches. By pre-resolving, we ensure the reader
// talks directly to the CDN origin rather than through a proxy/WAF that may
// block or throttle connections.
func connectHLS(ctx context.Context, uri string, httpClient *http.Client) (streamProducer, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("parse HLS URL: %w", err)
	}
	client := httpClient
	if client == nil {
		client = http.DefaultClient
	}

	body, finalURL, err := fetchHLSPlaylist(ctx, client, u)
	if err != nil {
		return nil, err
	}

	prod, err := hls.OpenURL(finalURL, body)
	if err != nil {
		body.Close()
		return nil, fmt.Errorf("open HLS stream: %w", err)
	}
	return prod, nil
}

// reStreamINF matches HLS master playlist variant stream entries.
var reStreamINF = regexp.MustCompile(`#EXT-X-STREAM-INF[^\n]*\n(\S+)`)

// fetchHLSPlaylist fetches the playlist at u. If it is a master playlist,
// it resolves the first variant URL and fetches that media playlist instead.
// Returns an io.ReadCloser for the final media playlist and its resolved URL.
func fetchHLSPlaylist(ctx context.Context, client *http.Client, u *url.URL) (io.ReadCloser, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, nil, fmt.Errorf("create HLS request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch HLS playlist: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("HLS playlist %s: HTTP %d", u.String(), resp.StatusCode)
	}

	// Read the playlist body to check for master playlist.
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("read HLS playlist: %w", err)
	}

	// If this is a master playlist, resolve the variant and fetch it.
	if m := reStreamINF.FindSubmatch(data); m != nil {
		variantRef, err := url.Parse(string(m[1]))
		if err != nil {
			return nil, nil, fmt.Errorf("parse variant URL: %w", err)
		}
		variantURL := u.ResolveReference(variantRef)

		req2, err := http.NewRequestWithContext(ctx, http.MethodGet, variantURL.String(), http.NoBody)
		if err != nil {
			return nil, nil, fmt.Errorf("create variant request: %w", err)
		}
		resp2, err := client.Do(req2)
		if err != nil {
			return nil, nil, fmt.Errorf("fetch variant playlist: %w", err)
		}
		if resp2.StatusCode != http.StatusOK {
			resp2.Body.Close()
			return nil, nil, fmt.Errorf("variant playlist %s: HTTP %d", variantURL.String(), resp2.StatusCode)
		}
		// Return the variant body; go2rtc reader will refresh using variantURL.
		return resp2.Body, variantURL, nil
	}

	// Media playlist — wrap the already-read bytes back into a ReadCloser.
	return io.NopCloser(bytes.NewReader(data)), u, nil
}

// connectONVIF discovers the RTSP stream URL from an ONVIF device and
// connects via RTSP. The URI scheme is onvif://[user:pass@]host[:port][/path].
// go2rtc's onvif.NewClient does not accept a context, so we wrap with
// context selection to respect the connection timeout.
func connectONVIF(ctx context.Context, uri string) (*rtsp.Conn, error) {
	// Convert onvif:// to http:// for the ONVIF SOAP endpoint.
	// Safe to slice at fixed offset: only called when
	// the lowercased URI starts with "onvif://", so len("onvif://") == 8.
	httpURL := "http://" + uri[len("onvif://"):]

	type result struct {
		rtspURL string
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		client, err := onvif.NewClient(httpURL)
		if err != nil {
			ch <- result{err: fmt.Errorf("onvif connect %s: %w", uri, err)}
			return
		}
		rtspURL, err := client.GetURI()
		if err != nil {
			ch <- result{err: fmt.Errorf("onvif get stream URI: %w", err)}
			return
		}
		ch <- result{rtspURL: rtspURL}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("onvif connect %s: %w", uri, ctx.Err())
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return connectRTSP(ctx, r.rtspURL)
	}
}

// extractFrames reads JPEG frame data from the consumer and writes them to
// temp files, sending entries to g.frameCh.
func (g *Go2RTCStreamSource) extractFrames(ctx context.Context, consumer *mjpeg.Consumer, prod streamProducer, tmpDir string) {
	defer func() {
		_ = prod.Stop()
		close(g.frameCh)
	}()

	// frameWriter captures each Write as a separate JPEG frame
	fw := &frameWriter{
		ctx:        ctx,
		tmpDir:     tmpDir,
		frameCh:    g.frameCh,
		sampleRate: g.sampleRate,
	}

	// WriteTo blocks until the consumer is stopped or an error occurs
	_, _ = consumer.WriteTo(fw)
}

// frameWriter is an io.Writer that saves each Write call as a JPEG frame file.
type frameWriter struct {
	ctx        context.Context
	tmpDir     string
	frameCh    chan frameEntry
	sampleRate int
	frameCount int
	emitCount  int
}

func (fw *frameWriter) Write(p []byte) (int, error) {
	// Check context
	select {
	case <-fw.ctx.Done():
		return 0, fw.ctx.Err()
	default:
	}

	idx := fw.frameCount
	fw.frameCount++

	// Apply sample rate
	if idx%fw.sampleRate != 0 {
		return len(p), nil
	}

	// Write JPEG to temp file
	path := filepath.Join(fw.tmpDir, fmt.Sprintf("frame_%05d.jpg", fw.emitCount))
	if err := os.WriteFile(path, p, 0o600); err != nil {
		return 0, fmt.Errorf("write frame: %w", err)
	}

	entry := frameEntry{path: path, idx: fw.emitCount}
	fw.emitCount++

	select {
	case <-fw.ctx.Done():
		return 0, fw.ctx.Err()
	case fw.frameCh <- entry:
		return len(p), nil
	}
}

// IsStreamScheme reports whether the URI has a streaming protocol scheme
// that Go2RTCStreamSource can handle: rtsp://, rtmp://, onvif://, or HLS (.m3u8).
func IsStreamScheme(uri string) bool {
	lower := strings.ToLower(uri)
	return strings.HasPrefix(lower, "rtsp://") ||
		strings.HasPrefix(lower, "rtmp://") ||
		strings.HasPrefix(lower, "onvif://") ||
		isHLSURL(lower)
}

// isHLSURL reports whether the lowercased URI looks like an HLS playlist URL.
func isHLSURL(lower string) bool {
	return (strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")) &&
		strings.Contains(lower, ".m3u8")
}

// IsGo2RTCScheme reports whether the URI has a streaming protocol scheme.
//
// Deprecated: use IsStreamScheme instead.
func IsGo2RTCScheme(uri string) bool {
	return IsStreamScheme(uri)
}

// isValidMJPEGRTP checks that an RTP payload has sufficient length for
// go2rtc's MJPEG RTPDepay to process without panicking. go2rtc v1.9.14
// does not bounds-check before reading quantization tables from the
// payload (pkg/mjpeg/rtp.go:38), causing "slice bounds out of range"
// panics on malformed or truncated packets.
//
// See RFC 2435 §3.1 for the JPEG RTP header format.
func isValidMJPEGRTP(payload []byte) bool {
	// JPEG RTP header: 8 bytes minimum (type-specific, fragment offset,
	// type, Q, width, height).
	if len(payload) < 8 {
		return false
	}

	t := payload[4]
	headerLen := 8
	// Restart Marker header (types 64-127) adds 4 extra bytes.
	if 64 <= t && t <= 127 {
		headerLen = 12
	}
	if len(payload) < headerLen {
		return false
	}

	// When Q >= 128 the first packet of a frame carries inline
	// quantization tables: 4-byte QT header + 64-byte luma + 64-byte
	// chroma = 132 bytes right after the JPEG/restart header.
	// A packet this small with Q >= 128 is malformed and would panic.
	q := payload[5]
	if q >= 128 && len(payload) < headerLen+132 {
		return false
	}

	return true
}
