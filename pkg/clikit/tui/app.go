package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/clikit"
	"github.com/google/uuid"
)

// AppConfig holds configuration for the TUI application.
type AppConfig struct {
	Engine           clikit.ReplEngine
	InitialSessionID string
	TimeoutMs        int
	Verbose          bool
	WaterfallMode    string
	// CustomCommands is invoked before built-in slash commands.
	// If it returns handled=true the command is consumed.
	CustomCommands func(input string, out io.Writer) (handled, quit bool)
	// UpdateNotice is an optional version update notification to display in the header.
	UpdateNotice string
}

// App is the top-level bubbletea Model.
type App struct {
	cfg    AppConfig
	ctx    context.Context
	cancel context.CancelFunc

	styles   Styles
	header   *Header
	chat     *Chat
	input    *Input
	status   *StatusBar
	spinner  spinner.Model
	spinning bool

	sessionID string
	width     int
	height    int

	// streaming state
	runCancel     context.CancelFunc
	lastInterrupt time.Time

	// side panel overlay (for /btw and /im)
	sidePanel       *SidePanel
	sidePanelCancel context.CancelFunc
}

// New creates a new TUI App.
func New(ctx context.Context, cfg AppConfig) *App {
	theme := DefaultTheme()
	styles := NewStyles(theme)

	sessionID := strings.TrimSpace(cfg.InitialSessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	appCtx, appCancel := context.WithCancel(ctx)

	a := &App{
		cfg:       cfg,
		ctx:       appCtx,
		cancel:    appCancel,
		styles:    styles,
		header:    NewHeader(styles),
		chat:      NewChat(styles),
		input:     NewInput(styles),
		status:    NewStatusBar(styles),
		spinner:   NewSpinner(theme),
		sessionID: sessionID,
	}

	// Populate header and status bar.
	a.header.SetModel(cfg.Engine.ModelName())
	a.header.SetSession(sessionID)
	a.header.SetSkillCount(len(cfg.Engine.Skills()))
	a.header.SetUpdateNotice(cfg.UpdateNotice)
	a.status.SetModel(cfg.Engine.ModelName())

	return a
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd {
	return tea.Batch(
		a.input.textarea.Focus(),
		tea.Println(a.header.View()),
	)
}

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.layout()
		return a, nil

	case tea.KeyPressMsg:
		return a.handleKey(msg)

	case spinner.TickMsg:
		if a.spinning {
			var cmd tea.Cmd
			a.spinner, cmd = a.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case StreamDoneMsg:
		a.chat.FinishStreaming()
		a.spinning = false
		a.input.SetEnabled(true)
		a.status.SetText("Ready")
		return a, a.flushChat()

	case StreamErrorMsg:
		a.chat.FinishStreaming()
		if msg.Err != nil {
			a.chat.AddError(msg.Err.Error())
		}
		a.spinning = false
		a.input.SetEnabled(true)
		a.status.SetText("Ready")
		return a, a.flushChat()

	case BtwDoneMsg:
		a.sidePanelCancel = nil
		if a.sidePanel != nil {
			a.sidePanel.SetDone()
			return a, nil
		}
		// Fallback: no panel active (shouldn't happen).
		if a.runCancel == nil {
			a.spinning = false
			a.input.SetEnabled(true)
			a.status.SetText("Ready")
		}
		return a, nil

	case BtwErrorMsg:
		a.sidePanelCancel = nil
		if a.sidePanel != nil {
			a.sidePanel.SetError(msg.Err)
			return a, nil
		}
		if msg.Err != nil {
			a.chat.AddError(msg.Err.Error())
		}
		if a.runCancel == nil {
			a.spinning = false
			a.input.SetEnabled(true)
			a.status.SetText("Ready")
		}
		return a, a.flushChat()

	case IMDoneMsg:
		a.sidePanelCancel = nil
		if a.sidePanel != nil {
			a.sidePanel.SetDone()
			if a.sidePanel.IsInteractive() {
				a.spinning = false
				a.input.SetEnabled(true)
				a.status.SetText("Ready")
			}
			return a, nil
		}
		if a.runCancel == nil {
			a.spinning = false
			a.input.SetEnabled(true)
			a.status.SetText("Ready")
		}
		return a, nil

	case IMErrorMsg:
		a.sidePanelCancel = nil
		if a.sidePanel != nil {
			a.sidePanel.SetError(msg.Err)
			if a.sidePanel.IsInteractive() {
				a.spinning = false
				a.input.SetEnabled(true)
				a.status.SetText("Ready")
			}
			return a, nil
		}
		if msg.Err != nil {
			a.chat.AddError(msg.Err.Error())
		}
		if a.runCancel == nil {
			a.spinning = false
			a.input.SetEnabled(true)
			a.status.SetText("Ready")
		}
		return a, a.flushChat()

	case CommandResultMsg:
		if msg.Quit {
			return a, tea.Quit
		}
		if msg.Text != "" {
			a.chat.AddError(msg.Text)
		}
		return a, a.flushChat()
	}

	// Forward to input (always — allows type-ahead while model runs).
	{
		var cmd tea.Cmd
		a.input, cmd = a.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	return a, tea.Batch(cmds...)
}

// View implements tea.Model.
func (a *App) View() tea.View {
	// Side panel overlay.
	if a.sidePanel != nil {
		if a.spinning {
			a.status.SetText(a.spinner.View() + " Thinking...")
		}
		panelView := a.sidePanel.View()
		statusView := a.status.View()
		if a.sidePanel.IsInteractive() {
			// Interactive panel (im): show panel + input + status.
			inputView := a.input.View()
			view := lipgloss.JoinVertical(lipgloss.Left, panelView, inputView, statusView)
			return tea.NewView(view)
		}
		// Non-interactive panel (btw): show panel + status only.
		view := lipgloss.JoinVertical(lipgloss.Left, panelView, statusView)
		return tea.NewView(view)
	}

	// Status text with spinner if active.
	if a.spinning {
		a.status.SetText(a.spinner.View() + " Thinking...")
	}
	statusView := a.status.View()
	inputView := a.input.View()
	chatView := a.chat.View()

	var parts []string
	if chatView != "" {
		parts = append(parts, chatView)
	}
	parts = append(parts, inputView, statusView)

	view := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return tea.NewView(view)
}

// handleSidePanelKey processes key events when the side panel overlay is active.
// Non-interactive panels (btw): scroll + any key to dismiss after done.
// Interactive panels (im): scroll + Enter to follow-up + Esc to close.
func (a *App) handleSidePanelKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "ctrl+d":
		return a, tea.Quit
	case "up", "k":
		if !a.sidePanel.IsInteractive() || !a.input.enabled {
			a.sidePanel.ScrollUp()
			return a, nil
		}
	case "down", "j":
		if !a.sidePanel.IsInteractive() || !a.input.enabled {
			a.sidePanel.ScrollDown()
			return a, nil
		}
	case "esc", "ctrl+c":
		if a.sidePanelCancel != nil {
			a.sidePanelCancel()
			a.sidePanelCancel = nil
		}
		return a, a.dismissSidePanel()
	case "enter":
		if a.sidePanel.IsInteractive() {
			text := a.input.Value()
			if text == "" {
				return a, nil
			}
			if !a.sidePanel.IsDone() {
				return a, nil
			}
			a.input.Reset()
			a.sidePanel.AddUserMessage(text)
			a.spinning = true
			a.input.SetEnabled(false)
			a.status.SetText("Thinking...")
			return a, tea.Batch(a.spinner.Tick, a.runIMFollowUp(text))
		}
	}

	// Interactive panel: forward other keys to input textarea.
	if a.sidePanel.IsInteractive() {
		var cmd tea.Cmd
		a.input, cmd = a.input.Update(msg)
		return a, cmd
	}

	// Non-interactive: any key dismisses after done.
	if a.sidePanel.IsDone() {
		return a, a.dismissSidePanel()
	}

	return a, nil
}

// runIMFollowUp sends a follow-up message in the /im panel's session.
func (a *App) runIMFollowUp(text string) tea.Cmd {
	return func() tea.Msg {
		if a.sidePanel == nil {
			return IMDoneMsg{}
		}

		followCtx, followCancel := context.WithCancel(a.ctx)
		a.sidePanelCancel = followCancel

		sessionID := a.sidePanel.SessionID()

		ch, err := a.cfg.Engine.RunStream(followCtx, sessionID, text)
		if err != nil {
			followCancel()
			return IMErrorMsg{Err: err}
		}

		for evt := range ch {
			switch evt.Type {
			case api.EventContentBlockDelta:
				if evt.Delta != nil && evt.Delta.Type == "text_delta" {
					if a.sidePanel != nil {
						a.sidePanel.AppendText(evt.Delta.Text)
					}
				}
			case api.EventToolExecutionStart:
				if a.sidePanel != nil {
					a.sidePanel.AppendText(fmt.Sprintf("\n[tool: %s]\n", evt.Name))
				}
			}
		}

		followCancel()
		return IMDoneMsg{}
	}
}

// dismissSidePanel closes the side panel and flushes its content to chat scrollback.
func (a *App) dismissSidePanel() tea.Cmd {
	if a.sidePanel == nil {
		return nil
	}
	// Add the side panel content to chat history for scrollback.
	content := strings.TrimSpace(a.sidePanel.content.String())
	if content != "" {
		if a.sidePanel.panelType == "btw" {
			a.chat.AddBtwQuestion(a.sidePanel.title)
		} else {
			a.chat.AddIMQuestion(a.sidePanel.title)
		}
		a.chat.AddSystem(content)
	}
	if a.sidePanel.err != nil {
		a.chat.AddError(a.sidePanel.err.Error())
	}
	a.sidePanel = nil
	a.sidePanelCancel = nil
	// Restore input state if no main stream is running.
	if a.runCancel == nil {
		a.spinning = false
		a.input.SetEnabled(true)
		a.status.SetText("Ready")
	}
	return a.flushChat()
}

// layout recalculates component sizes based on window dimensions.
func (a *App) layout() {
	a.chat.SetWidth(a.width)
	a.input.SetWidth(a.width)
	a.status.SetWidth(a.width)
	if a.sidePanel != nil {
		a.sidePanel.SetSize(a.width, a.height)
	}
}

// flushChat flushes completed chat messages to terminal scrollback via tea.Println.
func (a *App) flushChat() tea.Cmd {
	flushed, ok := a.chat.FlushMessages()
	if !ok {
		return nil
	}
	return tea.Println(flushed)
}

// handleKey processes key events.
func (a *App) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Side panel intercepts all keys when active.
	if a.sidePanel != nil {
		return a.handleSidePanelKey(msg)
	}

	switch msg.String() {
	case "ctrl+d":
		return a, tea.Quit

	case "ctrl+c":
		now := time.Now()
		// If model is running, cancel it.
		if a.runCancel != nil {
			a.runCancel()
			a.runCancel = nil
			a.chat.FinishStreaming()
			a.spinning = false
			a.input.SetEnabled(true)
			a.status.SetText("Interrupted (press Ctrl+C again to exit)")
			a.lastInterrupt = now
			return a, a.flushChat()
		}
		// Double Ctrl+C to exit.
		if now.Sub(a.lastInterrupt) < time.Second {
			return a, tea.Quit
		}
		a.lastInterrupt = now
		a.status.SetText("Press Ctrl+C again to exit")
		return a, nil

	case "enter":
		text := a.input.Value()
		if text == "" {
			return a, nil
		}
		// Allow /btw and /im while model is running (side questions).
		if !a.input.enabled {
			trimmed := strings.TrimSpace(text)
			if !strings.HasPrefix(trimmed, "/btw ") && !strings.HasPrefix(trimmed, "/im ") {
				return a, nil
			}
		}
		a.input.SaveHistory(text)
		a.input.Reset()
		return a.handleSubmit(text)
	}

	// Forward to input textarea.
	var cmd tea.Cmd
	a.input, cmd = a.input.Update(msg)
	return a, cmd
}

// handleSubmit processes submitted text (user prompt or slash command).
func (a *App) handleSubmit(text string) (tea.Model, tea.Cmd) {
	// Slash commands.
	if strings.HasPrefix(text, "/") {
		// Try custom commands first.
		if a.cfg.CustomCommands != nil {
			var buf bytes.Buffer
			if handled, quit := a.cfg.CustomCommands(text, &buf); handled {
				if quit {
					return a, tea.Quit
				}
				if msg := strings.TrimSpace(buf.String()); msg != "" {
					a.chat.AddError(msg)
				}
				return a, a.flushChat()
			}
		}

		cmd := strings.ToLower(strings.Fields(text)[0])
		switch cmd {
		case "/quit", "/exit", "/q":
			return a, tea.Quit
		case "/new":
			a.sessionID = uuid.NewString()
			a.header.SetSession(a.sessionID)
			a.status.ResetTokens()
			a.chat.Clear()
			a.chat.AddError("New conversation started")
			return a, a.flushChat()
		case "/model":
			parts := strings.Fields(text)
			if len(parts) == 1 {
				a.chat.AddError(fmt.Sprintf("Model: %s", a.cfg.Engine.ModelName()))
			} else {
				newModel := parts[1]
				if err := a.cfg.Engine.SetModel(a.ctx, newModel); err != nil {
					a.chat.AddError(fmt.Sprintf("Failed to switch model: %v", err))
				} else {
					a.header.SetModel(newModel)
					a.status.SetModel(newModel)
					a.chat.AddError(fmt.Sprintf("Model switched to: %s", newModel))
				}
			}
			return a, a.flushChat()
		case "/session":
			a.chat.AddError(fmt.Sprintf("Session: %s", a.sessionID))
			return a, a.flushChat()
		case "/skills":
			metas := a.cfg.Engine.Skills()
			sort.Slice(metas, func(i, j int) bool { return metas[i].Name < metas[j].Name })
			var lines []string
			for _, m := range metas {
				lines = append(lines, "- "+m.Name)
			}
			a.chat.AddError(strings.Join(lines, "\n"))
			return a, a.flushChat()
		case "/btw":
			question := strings.TrimSpace(text[4:])
			if question == "" {
				a.chat.AddError("Usage: /btw <question>")
				return a, a.flushChat()
			}
			a.sidePanel = NewSidePanel(a.styles, "btw", "/btw "+question)
			a.sidePanel.SetSize(a.width, a.height)
			a.spinning = true
			a.input.SetEnabled(false)
			a.status.SetText("Thinking...")
			return a, tea.Batch(a.spinner.Tick, a.runBtw(question))
		case "/im":
			instruction := strings.TrimSpace(text[3:])
			if instruction == "" {
				instruction = "帮我配置和连接一个 IM 平台"
			}
			imSessionID := "im-" + uuid.NewString()
			a.sidePanel = NewInteractiveSidePanel(a.styles, "im", "/im "+instruction, imSessionID)
			a.sidePanel.SetSize(a.width, a.height)
			a.spinning = true
			a.input.SetEnabled(false)
			a.status.SetText("IM bridge...")
			return a, tea.Batch(a.spinner.Tick, a.runIM(instruction))
		case "/status":
			status := fmt.Sprintf("Session: %s | Model: %s | Repo: %s | Sandbox: %s | Skills: %d",
				a.sessionID, a.cfg.Engine.ModelName(), a.cfg.Engine.RepoRoot(),
				displaySandbox(a.cfg.Engine.SandboxBackend()), len(a.cfg.Engine.Skills()))
			if a.status.inputTokens > 0 || a.status.outputTokens > 0 {
				total := a.status.inputTokens + a.status.outputTokens
				status += fmt.Sprintf(" | Tokens: %s (%s↑ %s↓)",
					formatTokenCount(total),
					formatTokenCount(a.status.inputTokens),
					formatTokenCount(a.status.outputTokens))
			}
			a.chat.AddError(status)
			return a, a.flushChat()
		case "/help":
			a.chat.AddError("/btw <question> /im /skills /status /new /session /model /help /quit")
			return a, a.flushChat()
		}
	}

	// Regular prompt.
	a.chat.AddUserMessage(text)
	flush := a.flushChat()
	a.chat.StartStreaming()
	a.spinning = true
	a.input.SetEnabled(false)
	a.status.SetText("Thinking...")

	return a, tea.Batch(a.spinner.Tick, a.runStream(text), flush)
}

// runStream starts streaming the model response in a goroutine and sends
// events back to the bubbletea program via tea.Cmd.
func (a *App) runStream(prompt string) tea.Cmd {
	return func() tea.Msg {
		runCtx, runCancel := context.WithCancel(a.ctx)
		a.runCancel = runCancel

		ctx := runCtx
		if a.cfg.TimeoutMs > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(runCtx, time.Duration(a.cfg.TimeoutMs)*time.Millisecond)
			defer cancel()
		}

		ch, err := a.cfg.Engine.RunStream(ctx, a.sessionID, prompt)
		if err != nil {
			runCancel()
			a.runCancel = nil
			return StreamErrorMsg{Err: err}
		}

		var pendingToolInput strings.Builder // accumulate tool input JSON
		for evt := range ch {
			switch evt.Type {
			case api.EventContentBlockDelta:
				if evt.Delta != nil {
					switch evt.Delta.Type {
					case "text_delta":
						a.chat.AppendStreamText(evt.Delta.Text)
					case "input_json_delta":
						// Accumulate tool input JSON for param extraction.
						var chunk string
						_ = json.Unmarshal(evt.Delta.PartialJSON, &chunk)
						pendingToolInput.WriteString(chunk)
					}
				}
			case api.EventToolExecutionStart:
				a.chat.FinishStreaming()
				params := extractToolParamsFromJSON(evt.Name, pendingToolInput.String())
				pendingToolInput.Reset()
				a.chat.AddToolCallWithParams(evt.Name, params, "pending")
				a.status.SetText(fmt.Sprintf("Tool: %s", evt.Name))
			case api.EventToolExecutionOutput:
				output := formatToolOutput(evt)
				if output != "" {
					a.chat.UpdateLastToolOutput(output)
				}
			case api.EventToolExecutionResult:
				result, isErr := formatToolResult(evt)
				if result != "" {
					a.chat.UpdateLastToolOutput(result)
				}
				if isErr {
					a.chat.UpdateLastToolStatus("error")
				} else {
					a.chat.UpdateLastToolStatus("success")
				}
				// Display inline images from tool result artifacts.
				for _, path := range extractImagePaths(evt) {
					a.chat.AddImage(path)
				}
				a.chat.StartStreaming()
				a.status.SetText("Thinking...")
			case api.EventMessageDelta:
				if evt.Usage != nil && (evt.Usage.InputTokens > 0 || evt.Usage.OutputTokens > 0) {
					a.status.AddTokens(evt.Usage.InputTokens, evt.Usage.OutputTokens)
				}
			case api.EventError:
				if evt.Output != nil {
					a.chat.AddError(fmt.Sprintf("%v", evt.Output))
				}
			}
		}

		runCancel()
		a.runCancel = nil
		return StreamDoneMsg{}
	}
}

// runBtw runs a /btw side question independently of the main stream.
// Text is written directly to the side panel overlay.
func (a *App) runBtw(question string) tea.Cmd {
	return func() tea.Msg {
		btwCtx, btwCancel := context.WithCancel(a.ctx)
		a.sidePanelCancel = btwCancel

		btwSessionID := "btw-" + uuid.NewString()
		wrappedPrompt := clikit.SideQuestionPromptWrapper + question

		// Fork from main session to inherit conversation context.
		ch, err := a.cfg.Engine.RunStreamForked(btwCtx, a.sessionID, btwSessionID, wrappedPrompt)
		if err != nil {
			btwCancel()
			return BtwErrorMsg{Err: err}
		}

		for evt := range ch {
			if evt.Type == api.EventContentBlockDelta && evt.Delta != nil && evt.Delta.Type == "text_delta" {
				if a.sidePanel != nil {
					a.sidePanel.AppendText(evt.Delta.Text)
				}
			}
		}

		btwCancel()
		return BtwDoneMsg{}
	}
}

// runIM runs an /im side question for IM bridge management.
// Text is written directly to the side panel overlay.
func (a *App) runIM(instruction string) tea.Cmd {
	return func() tea.Msg {
		imCtx, imCancel := context.WithCancel(a.ctx)
		a.sidePanelCancel = imCancel

		imSessionID := "im-" + uuid.NewString()
		wrappedPrompt := clikit.IMSidePromptWrapper + instruction

		// Fork from main session to inherit conversation context.
		ch, err := a.cfg.Engine.RunStreamForked(imCtx, a.sessionID, imSessionID, wrappedPrompt)
		if err != nil {
			imCancel()
			return IMErrorMsg{Err: err}
		}

		for evt := range ch {
			switch evt.Type {
			case api.EventContentBlockDelta:
				if evt.Delta != nil && evt.Delta.Type == "text_delta" {
					if a.sidePanel != nil {
						a.sidePanel.AppendText(evt.Delta.Text)
					}
				}
			case api.EventToolExecutionStart:
				if a.sidePanel != nil {
					a.sidePanel.AppendText(fmt.Sprintf("\n[tool: %s]\n", evt.Name))
				}
			}
		}

		imCancel()
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

	default:
		if n == 1 {
			return truncLine(output, 100)
		}
		return fmt.Sprintf("%d lines", n)
	}
}

func truncLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
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

func displaySandbox(s string) string {
	if strings.TrimSpace(s) == "" {
		return "host"
	}
	return s
}

// Run starts the bubbletea program.
func Run(ctx context.Context, cfg AppConfig) error {
	app := New(ctx, cfg)
	p := tea.NewProgram(app)
	_, err := p.Run()
	return err
}
