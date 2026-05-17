// app_commands.go: tea.Cmd factories for streaming runs and tool-output formatting helpers.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/clikit"
	"github.com/google/uuid"
)

// runStream starts streaming the model response in a goroutine and sends
// events back to the bubbletea program via program.Send(). All shared state
// mutations happen in Update() via the corresponding message handlers.
func (a *App) runStream(ctx context.Context, prompt string) tea.Cmd {
	return func() tea.Msg {
		streamCtx := ctx
		if a.cfg.TimeoutMs > 0 {
			var cancel context.CancelFunc
			streamCtx, cancel = context.WithTimeout(ctx, time.Duration(a.cfg.TimeoutMs)*time.Millisecond)
			defer cancel()
		}

		ch, err := a.cfg.Engine.RunStream(streamCtx, a.sessionID, prompt)
		if err != nil {
			return StreamErrorMsg{Err: err}
		}

		var pendingToolInput strings.Builder
		for evt := range ch {
			switch evt.Type {
			case api.EventContentBlockDelta:
				if evt.Delta != nil {
					switch evt.Delta.Type {
					case "text_delta":
						a.program.Send(StreamTextMsg{Text: evt.Delta.Text})
					case "input_json_delta":
						var chunk string
						_ = json.Unmarshal(evt.Delta.PartialJSON, &chunk)
						pendingToolInput.WriteString(chunk)
					}
				}
			case api.EventToolExecutionStart:
				params := extractToolParamsFromJSON(evt.Name, pendingToolInput.String())
				pendingToolInput.Reset()
				a.program.Send(StreamToolStartMsg{Name: evt.Name, Params: params})
			case api.EventToolExecutionOutput:
				output := formatToolOutput(evt)
				if output != "" {
					a.program.Send(StreamToolOutputMsg{Output: output})
				}
			case api.EventToolExecutionResult:
				result, isErr := formatToolResult(evt)
				a.program.Send(StreamToolResultMsg{
					Output:     result,
					IsError:    isErr,
					ImagePaths: extractImagePaths(evt),
				})
			case api.EventMessageDelta:
				if evt.Usage != nil && (evt.Usage.InputTokens > 0 || evt.Usage.OutputTokens > 0) {
					a.program.Send(StreamTokenUsageMsg{Input: evt.Usage.InputTokens, Output: evt.Usage.OutputTokens})
				}
			case api.EventError:
				if evt.Output != nil {
					a.program.Send(StreamErrorTextMsg{Text: fmt.Sprintf("%v", evt.Output)})
				}
			}
		}

		return StreamDoneMsg{}
	}
}

// runBtw runs a /btw side question independently of the main stream.
// Text events are forwarded to the Update loop via SidePanelTextMsg.
func (a *App) runBtw(ctx context.Context, question string) tea.Cmd {
	return func() tea.Msg {
		btwSessionID := "btw-" + uuid.NewString()
		wrappedPrompt := clikit.SideQuestionPromptWrapper + question

		ch, err := a.cfg.Engine.RunStreamForked(ctx, a.sessionID, btwSessionID, wrappedPrompt)
		if err != nil {
			return BtwErrorMsg{Err: err}
		}

		for evt := range ch {
			if evt.Type == api.EventContentBlockDelta && evt.Delta != nil && evt.Delta.Type == "text_delta" {
				a.program.Send(SidePanelTextMsg{Text: evt.Delta.Text})
			}
		}

		return BtwDoneMsg{}
	}
}

// runIM runs an /im side question for IM bridge management.
// Text events are forwarded to the Update loop via SidePanelTextMsg.
func (a *App) runIM(ctx context.Context, imSessionID, instruction string) tea.Cmd {
	return func() tea.Msg {
		wrappedPrompt := clikit.IMSidePromptWrapper + instruction

		ch, err := a.cfg.Engine.RunStreamForked(ctx, a.sessionID, imSessionID, wrappedPrompt)
		if err != nil {
			return IMErrorMsg{Err: err}
		}

		for evt := range ch {
			switch evt.Type {
			case api.EventContentBlockDelta:
				if evt.Delta != nil && evt.Delta.Type == "text_delta" {
					a.program.Send(SidePanelTextMsg{Text: evt.Delta.Text})
				}
			case api.EventToolExecutionStart:
				a.program.Send(SidePanelToolMsg{Name: evt.Name})
			}
		}

		return IMDoneMsg{}
	}
}

// runIMFollowUp sends a follow-up message in the /im panel's session.
func (a *App) runIMFollowUp(ctx context.Context, sessionID, text string) tea.Cmd {
	return func() tea.Msg {
		ch, err := a.cfg.Engine.RunStream(ctx, sessionID, text)
		if err != nil {
			return IMErrorMsg{Err: err}
		}

		for evt := range ch {
			switch evt.Type {
			case api.EventContentBlockDelta:
				if evt.Delta != nil && evt.Delta.Type == "text_delta" {
					a.program.Send(SidePanelTextMsg{Text: evt.Delta.Text})
				}
			case api.EventToolExecutionStart:
				a.program.Send(SidePanelToolMsg{Name: evt.Name})
			}
		}

		return IMDoneMsg{}
	}
}

// extractToolParamsFromJSON extracts a brief parameter summary from accumulated
// tool input JSON, following Claude Code's display pattern:
//   - Read/Write: show file path
//   - Bash: show command (truncated)
//   - Grep/Glob: show pattern
//   - Other: empty
func extractToolParamsFromJSON(toolName, inputJSON string) string {
	if inputJSON == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &m); err != nil {
		return ""
	}

	switch {
	case strings.Contains(strings.ToLower(toolName), "read"),
		strings.Contains(strings.ToLower(toolName), "write"),
		strings.Contains(strings.ToLower(toolName), "edit"):
		if fp, ok := m["file_path"].(string); ok {
			return filepath.Base(fp)
		}
	case strings.Contains(strings.ToLower(toolName), "bash"):
		if cmd, ok := m["command"].(string); ok {
			// Truncate long commands like Claude Code (max 2 lines, 160 chars).
			lines := strings.SplitN(cmd, "\n", 3)
			display := strings.Join(lines[:min(len(lines), 2)], " ")
			if len(display) > 80 {
				return display[:77] + "…"
			}
			return display
		}
	case strings.Contains(strings.ToLower(toolName), "grep"):
		if pat, ok := m["pattern"].(string); ok {
			if len(pat) > 60 {
				return pat[:57] + "…"
			}
			return pat
		}
	case strings.Contains(strings.ToLower(toolName), "glob"):
		if pat, ok := m["pattern"].(string); ok {
			return pat
		}
	}
	return ""
}

// formatToolOutput creates a brief output summary from a tool_execution_output event.
// This is the raw tool output before the final result event.
func formatToolOutput(evt api.StreamEvent) string {
	if evt.Output == nil {
		return ""
	}
	s := fmt.Sprintf("%v", evt.Output)
	return summarizeOutput(evt.Name, s)
}

// formatToolResult creates a result summary from a tool_execution_result event.
// Returns (summary, isError).
func formatToolResult(evt api.StreamEvent) (string, bool) {
	if evt.Output == nil {
		return "", false
	}
	m, ok := evt.Output.(map[string]any)
	if !ok {
		return "", false
	}

	// Check for error in metadata.
	isErr := false
	if meta, ok := m["metadata"].(map[string]any); ok {
		if errFlag, ok := meta["is_error"].(bool); ok && errFlag {
			isErr = true
		}
	}

	out, _ := m["output"].(string)
	if out == "" {
		return "", isErr
	}

	return summarizeToolResult(evt.Name, out), isErr
}

// summarizeOutput returns a short display string for raw tool output.
func summarizeOutput(toolName, output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	n := len(lines)

	name := strings.ToLower(toolName)
	switch {
	case strings.Contains(name, "bash"):
		// Show last 2 lines of bash output as preview.
		if n <= 2 {
			return truncLine(output, 100)
		}
		last := strings.TrimSpace(lines[n-1])
		if last == "" && n >= 2 {
			last = strings.TrimSpace(lines[n-2])
		}
		return fmt.Sprintf("… %s (%d lines)", truncLine(last, 60), n)
	default:
		if n <= 2 {
			return truncLine(output, 100)
		}
		return fmt.Sprintf("%d lines", n)
	}
}

// summarizeToolResult generates a tool-specific result summary.
func summarizeToolResult(toolName, output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	n := len(lines)
	name := strings.ToLower(toolName)

	switch {
	case strings.Contains(name, "read"):
		return fmt.Sprintf("Read %d %s", n, pluralize(n, "line", "lines"))

	case strings.Contains(name, "write"):
		return fmt.Sprintf("Wrote %d %s", n, pluralize(n, "line", "lines"))

	case strings.Contains(name, "edit"):
		return "Applied changes"

	case strings.Contains(name, "grep"):
		// Count file matches from output lines.
		count := 0
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				count++
			}
		}
		return fmt.Sprintf("%d %s", count, pluralize(count, "match", "matches"))

	case strings.Contains(name, "glob"):
		count := 0
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				count++
			}
		}
		return fmt.Sprintf("%d %s", count, pluralize(count, "file", "files"))

	case strings.Contains(name, "bash"):
		if n <= 2 {
			return truncLine(output, 100)
		}
		// Show last meaningful line + line count.
		last := lastNonEmpty(lines)
		return fmt.Sprintf("… %s (%d lines)", truncLine(last, 60), n)

	case isAigoTool(name):
		return summarizeAigoResult(name, output)

	default:
		if n == 1 {
			return truncLine(output, 100)
		}
		return fmt.Sprintf("%d lines", n)
	}
}

var aigoToolLabels = map[string]string{
	"generate_image":   "Image generated",
	"edit_image":       "Image edited",
	"generate_video":   "Video generated",
	"edit_video":       "Video edited",
	"generate_3d":      "3D model generated",
	"generate_music":   "Music generated",
	"text_to_speech":   "Audio generated",
	"design_voice":     "Voice designed",
	"transcribe_audio": "Transcription complete",
}

func isAigoTool(name string) bool {
	_, ok := aigoToolLabels[name]
	return ok
}

func summarizeAigoResult(name, output string) string {
	label := aigoToolLabels[name]
	if label == "" {
		label = "Done"
	}
	output = strings.TrimSpace(output)
	if strings.HasPrefix(output, "http://") || strings.HasPrefix(output, "https://") {
		return label
	}
	if name == "transcribe_audio" && output != "" {
		r := []rune(output)
		if len(r) > 40 {
			return fmt.Sprintf("%s — %s…", label, string(r[:40]))
		}
		return fmt.Sprintf("%s — %s", label, output)
	}
	if output != "" {
		return fmt.Sprintf("%s — %s", label, truncLine(output, 60))
	}
	return label
}

func truncLine(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func lastNonEmpty(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s != "" {
			return s
		}
	}
	return ""
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// extractImagePaths pulls local image file paths from a tool_execution_result event.
// Artifacts are stored in metadata["artifacts"] as []artifact.ArtifactRef or []any.
func extractImagePaths(evt api.StreamEvent) []string {
	m, ok := evt.Output.(map[string]any)
	if !ok {
		return nil
	}
	meta, _ := m["metadata"].(map[string]any)
	if meta == nil {
		return nil
	}
	raw, ok := meta["artifacts"]
	if !ok {
		return nil
	}

	var paths []string
	imageExts := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}

	switch arts := raw.(type) {
	case []artifact.ArtifactRef:
		for _, ref := range arts {
			if ref.Path != "" && (ref.Kind == artifact.ArtifactKindImage || imageExts[strings.ToLower(filepath.Ext(ref.Path))]) {
				paths = append(paths, ref.Path)
			}
		}
	case []any:
		for _, item := range arts {
			if ref, ok := item.(map[string]any); ok {
				path, _ := ref["path"].(string)
				kind, _ := ref["kind"].(string)
				if path != "" && (kind == "image" || imageExts[strings.ToLower(filepath.Ext(path))]) {
					paths = append(paths, path)
				}
			}
		}
	}
	return paths
}
