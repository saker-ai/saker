package tui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const stallThreshold = 3 * time.Second

// SmartSpinner wraps bubbletea's spinner with status verb, stall detection,
// and character count tracking.
type SmartSpinner struct {
	spinner spinner.Model
	styles  Styles
	verb    string
	start   time.Time
	stalled bool
	active  bool

	lastTokenNano atomic.Int64
	charCount     atomic.Int64
}

func NewSmartSpinner(t Theme, s Styles) *SmartSpinner {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(t.Primary)
	return &SmartSpinner{
		spinner: sp,
		styles:  s,
		verb:    "Thinking...",
	}
}

func (s *SmartSpinner) Start() {
	s.start = time.Now()
	s.lastTokenNano.Store(time.Now().UnixNano())
	s.charCount.Store(0)
	s.stalled = false
	s.active = true
	s.verb = "Thinking..."
}

func (s *SmartSpinner) Stop() {
	s.active = false
	s.stalled = false
}

func (s *SmartSpinner) SetVerb(v string) {
	s.verb = v
	s.stalled = false
	s.lastTokenNano.Store(time.Now().UnixNano())
}

// AddTokens accumulates character count (safe to call from streaming goroutine).
func (s *SmartSpinner) AddTokens(n int) {
	s.charCount.Add(int64(n))
	s.lastTokenNano.Store(time.Now().UnixNano())
}

func (s *SmartSpinner) CheckStall() {
	if !s.active {
		return
	}
	lastNano := s.lastTokenNano.Load()
	if lastNano == 0 {
		return
	}
	s.stalled = time.Since(time.Unix(0, lastNano)) > stallThreshold
}

func (s *SmartSpinner) Tick() tea.Cmd {
	return s.spinner.Tick
}

func (s *SmartSpinner) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	s.spinner, cmd = s.spinner.Update(msg)
	return cmd
}

func (s *SmartSpinner) View() string {
	icon := s.spinner.View()
	if s.stalled {
		icon = lipgloss.NewStyle().Foreground(s.styles.Theme.Warning).Render("●")
	}

	verb := s.verb
	if s.stalled && !strings.Contains(s.verb, "Running") && !strings.Contains(s.verb, "Generating") {
		verb = "Waiting..."
	}

	elapsed := time.Since(s.start)
	chars := s.charCount.Load()

	var stats string
	if chars > 0 || elapsed > 2*time.Second {
		var parts []string
		if chars > 0 {
			parts = append(parts, formatCharCount(int(chars)))
		}
		if elapsed > 2*time.Second {
			parts = append(parts, formatDuration(elapsed))
		}
		if len(parts) > 0 {
			stats = " (" + strings.Join(parts, ", ") + ")"
		}
	}
	return icon + " " + verb + stats
}

func toolVerb(name, params string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "bash"):
		if params != "" {
			display := params
			if len(display) > 40 {
				display = display[:37] + "..."
			}
			return fmt.Sprintf("Running `%s`...", display)
		}
		return "Running command..."
	case strings.Contains(lower, "read"):
		if params != "" {
			return fmt.Sprintf("Reading %s...", params)
		}
		return "Reading file..."
	case strings.Contains(lower, "write"):
		if params != "" {
			return fmt.Sprintf("Writing %s...", params)
		}
		return "Writing file..."
	case strings.Contains(lower, "edit"):
		if params != "" {
			return fmt.Sprintf("Editing %s...", params)
		}
		return "Editing file..."
	case strings.Contains(lower, "grep"):
		if params != "" {
			return fmt.Sprintf("Searching `%s`...", params)
		}
		return "Searching..."
	case strings.Contains(lower, "glob"):
		return "Finding files..."
	case lower == "generate_image":
		return "Generating image..."
	case lower == "edit_image":
		return "Editing image..."
	case lower == "generate_video":
		return "Generating video..."
	case lower == "generate_3d":
		return "Generating 3D model..."
	case lower == "generate_music":
		return "Generating music..."
	case lower == "text_to_speech":
		return "Generating speech..."
	case lower == "transcribe_audio":
		return "Transcribing audio..."
	default:
		return fmt.Sprintf("Running %s...", name)
	}
}

func formatCharCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM chars", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk chars", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d chars", n)
	}
}

func formatDuration(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	rem := secs % 60
	if rem == 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dm%ds", mins, rem)
}
