package tui

import (
	"encoding/base64"
	"fmt"
	"image"
	// Register decoders for dimension detection.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// ImageProtocol identifies which terminal image protocol to use.
type ImageProtocol int

const (
	ProtocolNone   ImageProtocol = iota // no inline image support
	ProtocolKitty                       // Kitty Graphics Protocol
	ProtocolITerm2                      // iTerm2 Inline Images Protocol
)

// kittyChunkSize is the max base64 bytes per Kitty Graphics chunk.
const kittyChunkSize = 4096

// defaultMaxCellWidth is the default max image width in terminal columns.
const defaultMaxCellWidth = 60

// DetectImageProtocol checks environment variables to determine which
// terminal image protocol the current terminal supports.
func DetectImageProtocol() ImageProtocol {
	term := os.Getenv("TERM_PROGRAM")
	termEnv := os.Getenv("TERM")

	// Kitty terminal
	if term == "kitty" || strings.Contains(termEnv, "kitty") {
		return ProtocolKitty
	}
	// Ghostty supports Kitty Graphics Protocol
	if term == "ghostty" {
		return ProtocolKitty
	}
	// WezTerm supports both protocols; prefer Kitty
	if term == "WezTerm" {
		return ProtocolKitty
	}
	// iTerm2
	if term == "iTerm.app" || os.Getenv("ITERM_SESSION_ID") != "" {
		return ProtocolITerm2
	}

	return ProtocolNone
}

// RenderImage renders an image file as a terminal inline image string.
// Returns "[Image: filename]" placeholder if the terminal does not support images.
// maxCellWidth limits the display width in terminal columns; 0 uses defaultMaxCellWidth.
func RenderImage(path string, maxCellWidth int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("[Image: %s (read error)]", filepath.Base(path))
	}
	if len(data) == 0 {
		return fmt.Sprintf("[Image: %s (empty)]", filepath.Base(path))
	}
	return renderImageData(data, filepath.Base(path), maxCellWidth)
}

// RenderImageData renders image bytes as a terminal inline image string.
func RenderImageData(data []byte, name string, maxCellWidth int) string {
	if len(data) == 0 {
		return fmt.Sprintf("[Image: %s (empty)]", name)
	}
	return renderImageData(data, name, maxCellWidth)
}

func renderImageData(data []byte, name string, maxCellWidth int) string {
	if maxCellWidth <= 0 {
		maxCellWidth = defaultMaxCellWidth
	}

	proto := DetectImageProtocol()
	switch proto {
	case ProtocolKitty:
		return renderKitty(data, maxCellWidth)
	case ProtocolITerm2:
		return renderITerm2(data, name, maxCellWidth)
	default:
		return fmt.Sprintf("[Image: %s]", name)
	}
}

// renderKitty encodes image data using the Kitty Graphics Protocol.
// Large payloads are split into chunks for reliable transmission.
func renderKitty(data []byte, maxCellWidth int) string {
	b64 := base64.StdEncoding.EncodeToString(data)

	// Detect image dimensions for aspect-ratio-aware column sizing.
	cols, rows := imageCellSize(data, maxCellWidth)

	var sb strings.Builder

	if len(b64) <= kittyChunkSize {
		// Single-chunk transfer.
		fmt.Fprintf(&sb, "\033_Ga=T,f=100,t=d,c=%d,r=%d;%s\033\\", cols, rows, b64)
	} else {
		// Multi-chunk: first chunk with m=1 (more), last with m=0.
		for i := 0; i < len(b64); i += kittyChunkSize {
			end := i + kittyChunkSize
			if end > len(b64) {
				end = len(b64)
			}
			chunk := b64[i:end]

			switch {
			case i == 0:
				// First chunk: include all parameters.
				fmt.Fprintf(&sb, "\033_Ga=T,f=100,t=d,c=%d,r=%d,m=1;%s\033\\", cols, rows, chunk)
			case end == len(b64):
				// Last chunk.
				fmt.Fprintf(&sb, "\033_Gm=0;%s\033\\", chunk)
			default:
				// Middle chunk.
				fmt.Fprintf(&sb, "\033_Gm=1;%s\033\\", chunk)
			}
		}
	}

	sb.WriteString("\n")
	return sb.String()
}

// renderITerm2 encodes image data using the iTerm2 Inline Images Protocol.
func renderITerm2(data []byte, name string, maxCellWidth int) string {
	b64 := base64.StdEncoding.EncodeToString(data)
	size := len(data)

	cols, rows := imageCellSize(data, maxCellWidth)

	var sb strings.Builder
	fmt.Fprintf(&sb, "\033]1337;File=inline=1;size=%d;width=%d;height=%d;preserveAspectRatio=1;name=%s:%s\a",
		size, cols, rows, base64.StdEncoding.EncodeToString([]byte(name)), b64)
	sb.WriteString("\n")
	return sb.String()
}

// imageCellSize returns (columns, rows) for displaying the image in the terminal.
// It decodes image headers to get the aspect ratio and scales to fit maxCellWidth.
// Falls back to (maxCellWidth, maxCellWidth/2) if dimensions cannot be determined.
func imageCellSize(data []byte, maxCellWidth int) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return maxCellWidth, maxCellWidth / 2
	}

	cols := maxCellWidth
	// Approximate: each cell is roughly 2x taller than wide (character aspect ratio).
	rows := int(float64(cols) * float64(cfg.Height) / float64(cfg.Width) / 2.0)
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}
