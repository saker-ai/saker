package tui

import (
	"regexp"
	"strings"
	"sync"

	"charm.land/glamour/v2"
)

var mdSyntaxRE = regexp.MustCompile(`(?m)(^#{1,6}\s|\*\*|__|~~|` + "```" + `|^>\s|^[-*+]\s|^\d+\.\s|\[.+\]\(.+\))`)

var (
	cachedRenderer      *glamour.TermRenderer
	cachedRendererWidth int
	cachedRendererMu    sync.Mutex
)

func getOrCreateRenderer(width int) *glamour.TermRenderer {
	cachedRendererMu.Lock()
	defer cachedRendererMu.Unlock()

	if cachedRenderer != nil && cachedRendererWidth == width {
		return cachedRenderer
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	cachedRenderer = r
	cachedRendererWidth = width
	return r
}

func renderMarkdown(content string, width int) string {
	if width < 20 {
		width = 20
	}
	if !mdSyntaxRE.MatchString(content) {
		return content
	}
	r := getOrCreateRenderer(width)
	if r == nil {
		return content
	}
	result, err := r.Render(content)
	if err != nil {
		return content
	}
	return strings.TrimRight(result, "\n")
}

// StreamingRenderer caches rendered stable blocks to avoid re-rendering
// the entire content on each streaming update.
type StreamingRenderer struct {
	renderedPrefix string
	stableText     string
	width          int
}

func NewStreamingRenderer() *StreamingRenderer {
	return &StreamingRenderer{}
}

func (sr *StreamingRenderer) Render(fullText string, width int) string {
	if width != sr.width {
		sr.Reset()
		sr.width = width
	}
	idx := strings.LastIndex(fullText, "\n\n")
	if idx < 0 {
		return renderMarkdown(fullText, width)
	}
	stableText := fullText[:idx+2]
	tailText := fullText[idx+2:]

	if stableText == sr.stableText && sr.renderedPrefix != "" {
		if tailText == "" {
			return sr.renderedPrefix
		}
		return sr.renderedPrefix + "\n" + renderMarkdown(tailText, width)
	}
	sr.stableText = stableText
	sr.renderedPrefix = renderMarkdown(stableText, width)
	if tailText == "" {
		return sr.renderedPrefix
	}
	return sr.renderedPrefix + "\n" + renderMarkdown(tailText, width)
}

func (sr *StreamingRenderer) Reset() {
	sr.renderedPrefix = ""
	sr.stableText = ""
}
