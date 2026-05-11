package pipeline

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/pcm"
	"github.com/AlexxIT/go2rtc/pkg/rtsp"
	"github.com/AlexxIT/go2rtc/pkg/wav"
	"github.com/pion/opus"
)

// audio_extractor_pcm.go owns the pure-Go decoding path: go2rtc connection,
// PCM/Opus chunking, and WAV serialisation. The ffmpeg fallback lives in
// audio_extractor_ffmpeg.go; the public extractor surface in
// audio_extractor.go.

// pcmChunker handles the shared buffering, chunk flushing, WAV writing, and
// channel sending logic used by both PCM and Opus collection paths.
type pcmChunker struct {
	extractor     *AudioExtractor
	wavHeader     []byte
	dstCodec      *core.Codec
	bytesPerChunk int
	buf           []byte
	bufMu         sync.Mutex
	chunkIdx      int
	startTime     time.Time
}

func newPCMChunker(a *AudioExtractor, wavHeader []byte, dstCodec *core.Codec) *pcmChunker {
	return &pcmChunker{
		extractor:     a,
		wavHeader:     wavHeader,
		dstCodec:      dstCodec,
		bytesPerChunk: pcm.BytesPerDuration(dstCodec, a.interval),
		buf:           make([]byte, 0, pcm.BytesPerDuration(dstCodec, a.interval)),
		startTime:     time.Now(),
	}
}

// handlePacket appends PCM data to the buffer and flushes full chunks to WAV files.
func (c *pcmChunker) handlePacket(ctx context.Context, pcmData []byte) {
	c.bufMu.Lock()
	c.buf = append(c.buf, pcmData...)

	for len(c.buf) >= c.bytesPerChunk {
		chunk := make([]byte, c.bytesPerChunk)
		copy(chunk, c.buf[:c.bytesPerChunk])
		c.buf = c.buf[c.bytesPerChunk:]
		c.bufMu.Unlock()

		path := filepath.Join(c.extractor.tmpDir, fmt.Sprintf("chunk_%03d.wav", c.chunkIdx))
		if err := writeWAV(path, c.wavHeader, chunk); err != nil {
			slog.Error("audio_extractor: write WAV chunk failed", "error", err)
			c.bufMu.Lock()
			continue
		}

		ts := time.Duration(c.chunkIdx) * c.extractor.interval
		select {
		case c.extractor.audioCh <- AudioChunk{Path: path, Index: c.chunkIdx, Timestamp: ts}:
		case <-ctx.Done():
			return
		}
		c.chunkIdx++
		c.bufMu.Lock()
	}
	c.bufMu.Unlock()
}

// flushRemaining writes any remaining buffer as a final partial chunk if substantial (>0.5s).
func (c *pcmChunker) flushRemaining() {
	c.bufMu.Lock()
	remainingBuf := make([]byte, len(c.buf))
	copy(remainingBuf, c.buf)
	c.bufMu.Unlock()

	minBytes := pcm.BytesPerDuration(c.dstCodec, 500*time.Millisecond)
	if len(remainingBuf) >= minBytes {
		path := filepath.Join(c.extractor.tmpDir, fmt.Sprintf("chunk_%03d.wav", c.chunkIdx))
		if err := writeWAV(path, c.wavHeader, remainingBuf); err == nil {
			ts := time.Duration(c.chunkIdx) * c.extractor.interval
			select {
			case c.extractor.audioCh <- AudioChunk{Path: path, Index: c.chunkIdx, Timestamp: ts}:
			default:
			}
		}
	}
}

// collectAndFlush runs the collection loop: attach sink, wait for context done,
// detach sink, flush remaining, and log duration.
func (c *pcmChunker) collectAndFlush(ctx context.Context, track *core.Receiver, handler func(packet *core.Packet), prod streamProducer, label string) {
	defer func() {
		_ = prod.Stop()
		close(c.extractor.audioCh)
		close(c.extractor.done)
	}()

	sink := &core.Node{}
	sink.Input = handler
	track.AppendChild(sink)

	<-ctx.Done()
	track.RemoveChild(sink)

	c.flushRemaining()

	duration := time.Since(c.startTime).Round(time.Second)
	slog.Info("audio_extractor: "+label+" stopped", "url", c.extractor.url, "chunks", c.chunkIdx, "duration", duration)
}

// isPCMCompatible reports whether the codec can be transcoded to PCM in pure Go.
func isPCMCompatible(codecName string) bool {
	switch codecName {
	case core.CodecPCMA, core.CodecPCMU, core.CodecPCM, core.CodecPCML:
		return true
	}
	return false
}

// isOpusCodec reports whether the codec is Opus (decodable via pion/opus).
func isOpusCodec(codecName string) bool {
	return codecName == core.CodecOpus
}

// isPureGoDecodable reports whether the codec can be decoded without ffmpeg.
func isPureGoDecodable(codecName string) bool {
	return isPCMCompatible(codecName) || isOpusCodec(codecName)
}

// startGo2RTC connects to the stream via go2rtc, finds an audio track with a
// PCM-compatible codec, and starts background extraction.
func (a *AudioExtractor) startGo2RTC(ctx context.Context) error {
	connectCtx, connectCancel := context.WithTimeout(ctx, a.connectTimeout)
	defer connectCancel()

	prod, err := ConnectStreamProducer(connectCtx, a.url, a.httpClient)
	if err != nil {
		return fmt.Errorf("audio_extractor: connect: %w", err)
	}

	// Find audio track. Priority: PCM-compatible > Opus > unsupported.
	var audioMedia *core.Media
	var audioCodec *core.Codec
	var opusMedia *core.Media
	var opusCodec *core.Codec
	var unsupportedMedia *core.Media
	var unsupportedCodec *core.Codec
	for _, media := range prod.GetMedias() {
		if media.Kind != core.KindAudio || media.Direction != core.DirectionRecvonly {
			continue
		}
		for _, codec := range media.Codecs {
			if isPCMCompatible(codec.Name) {
				// Best: PCM-compatible (zero-overhead transcode).
				audioMedia = media
				audioCodec = codec
				break
			}
			if isOpusCodec(codec.Name) && opusMedia == nil {
				opusMedia = media
				opusCodec = codec
			}
		}
		if audioMedia != nil {
			break
		}
		// Remember first unsupported audio codec for error reporting.
		if unsupportedMedia == nil && opusMedia == nil && len(media.Codecs) > 0 {
			unsupportedMedia = media
			unsupportedCodec = media.Codecs[0]
		}
	}

	// Fallback priority: Opus (pure Go) > unsupported (ffmpeg).
	if audioMedia == nil {
		if opusMedia != nil {
			audioMedia = opusMedia
			audioCodec = opusCodec
		} else if unsupportedMedia != nil {
			audioMedia = unsupportedMedia
			audioCodec = unsupportedCodec
		}
	}

	if audioMedia == nil {
		_ = prod.Stop()
		return fmt.Errorf("audio_extractor: no audio track found in %s", a.url)
	}

	if !isPureGoDecodable(audioCodec.Name) {
		_ = prod.Stop()
		return fmt.Errorf("%w: %s", ErrCodecNotSupported, audioCodec.Name)
	}

	track, err := prod.GetTrack(audioMedia, audioCodec)
	if err != nil {
		_ = prod.Stop()
		return fmt.Errorf("audio_extractor: get audio track: %w", err)
	}

	// Start RTSP playback if needed.
	if rtspClient, ok := prod.(*rtsp.Conn); ok {
		go func() {
			if err := rtspClient.Play(); err != nil {
				slog.Error("audio_extractor: rtsp play failed", "error", err)
				_ = rtspClient.Stop()
				return
			}
			_ = rtspClient.Start()
		}()
	}

	// Target codec: PCM16LE mono 16kHz (optimal for Whisper).
	dstCodec := &core.Codec{Name: core.CodecPCML, ClockRate: 16000, Channels: 1}
	wavHeader := wav.Header(dstCodec)

	if isOpusCodec(audioCodec.Name) {
		slog.Info("audio_extractor: started pure Go Opus decoding",
			"url", a.url, "codec", audioCodec.Name, "clock_rate", audioCodec.ClockRate)
		go a.collectOpus(ctx, track, wavHeader, dstCodec, prod)
	} else {
		transcode := pcm.Transcode(dstCodec, audioCodec)
		slog.Info("audio_extractor: started pure Go PCM extraction",
			"url", a.url, "codec", audioCodec.Name, "clock_rate", audioCodec.ClockRate)
		go a.collectPCM(ctx, track, transcode, wavHeader, dstCodec, prod)
	}
	return nil
}

// collectPCM collects audio RTP packets, transcodes to PCM16LE, and writes
// WAV chunks at the configured interval.
func (a *AudioExtractor) collectPCM(ctx context.Context, track *core.Receiver, transcode func([]byte) []byte, wavHeader []byte, dstCodec *core.Codec, prod streamProducer) {
	chunker := newPCMChunker(a, wavHeader, dstCodec)
	handler := func(packet *core.Packet) {
		pcmData := transcode(packet.Payload)
		if len(pcmData) > 0 {
			chunker.handlePacket(ctx, pcmData)
		}
	}
	chunker.collectAndFlush(ctx, track, handler, prod, "PCM")
}

// collectOpus decodes Opus RTP packets to PCM16LE using pion/opus (pure Go)
// and writes WAV chunks at the configured interval.
func (a *AudioExtractor) collectOpus(ctx context.Context, track *core.Receiver, wavHeader []byte, dstCodec *core.Codec, prod streamProducer) {
	decoder, err := opus.NewDecoderWithOutput(int(dstCodec.ClockRate), int(dstCodec.Channels))
	if err != nil {
		slog.Error("audio_extractor: create opus decoder failed", "error", err)
		_ = prod.Stop()
		close(a.audioCh)
		close(a.done)
		return
	}

	chunker := newPCMChunker(a, wavHeader, dstCodec)
	int16Buf := make([]int16, 1920)

	handler := func(packet *core.Packet) {
		if len(packet.Payload) == 0 {
			return
		}
		samplesPerCh, decErr := decoder.DecodeToInt16(packet.Payload, int16Buf)
		if decErr != nil {
			return
		}
		totalSamples := samplesPerCh * int(dstCodec.Channels)
		pcmData := make([]byte, totalSamples*2)
		for i := 0; i < totalSamples; i++ {
			sample := int16Buf[i]
			pcmData[i*2] = byte(sample)
			pcmData[i*2+1] = byte(sample >> 8)
		}
		chunker.handlePacket(ctx, pcmData)
	}
	chunker.collectAndFlush(ctx, track, handler, prod, "Opus")
}

// writeWAV writes a WAV file with the given header and PCM data.
func writeWAV(path string, header, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write WAV header.
	if _, err := f.Write(header); err != nil {
		return err
	}

	// Patch RIFF size and data size in the header.
	// RIFF size at offset 4 = file size - 8.
	// data size at offset len(header)-4 = len(data).
	totalSize := uint32(len(header) - 8 + len(data))
	dataSize := uint32(len(data))

	// Seek back and patch sizes.
	if _, err := f.Seek(4, io.SeekStart); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, totalSize); err != nil {
		return err
	}
	if _, err := f.Seek(int64(len(header)-4), io.SeekStart); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, dataSize); err != nil {
		return err
	}

	// Seek to end and write PCM data.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}
