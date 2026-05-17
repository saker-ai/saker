// app_update.go: Update() loop, message handlers, key dispatch, and submit logic.
package tui

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
)

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
			cmd := a.smartSpinner.Update(msg)
			a.smartSpinner.CheckStall()
			cmds = append(cmds, cmd)
		}

	case StreamTextMsg:
		a.chat.AppendStreamText(msg.Text)
		a.smartSpinner.AddTokens(len(msg.Text))

	case StreamToolStartMsg:
		a.chat.FinishStreaming()
		a.chat.AddToolCallWithParams(msg.Name, msg.Params, "pending")
		a.smartSpinner.SetVerb(toolVerb(msg.Name, msg.Params))

	case StreamToolOutputMsg:
		if msg.Output != "" {
			a.chat.UpdateLastToolOutput(msg.Output)
		}

	case StreamToolResultMsg:
		if msg.Output != "" {
			a.chat.UpdateLastToolOutput(msg.Output)
		}
		if msg.IsError {
			a.chat.UpdateLastToolStatus("error")
		} else {
			a.chat.UpdateLastToolStatus("success")
		}
		for _, p := range msg.ImagePaths {
			a.chat.AddImage(p)
		}
		a.chat.StartStreaming()
		a.smartSpinner.SetVerb("Thinking...")

	case StreamTokenUsageMsg:
		a.status.AddTokens(msg.Input, msg.Output)

	case StreamErrorTextMsg:
		a.chat.AddError(msg.Text)

	case SidePanelTextMsg:
		if a.sidePanel != nil {
			a.sidePanel.AppendText(msg.Text)
		}

	case SidePanelToolMsg:
		if a.sidePanel != nil {
			a.sidePanel.AppendText("\n[tool: " + msg.Name + "]\n")
		}

	case StreamDoneMsg:
		if a.runCancel != nil {
			a.runCancel()
			a.runCancel = nil
		}
		a.chat.FinishStreaming()
		a.smartSpinner.Stop()
		a.spinning = false
		a.input.SetEnabled(true)
		a.status.SetText("Ready")
		return a, a.flushChat()

	case StreamErrorMsg:
		if a.runCancel != nil {
			a.runCancel()
			a.runCancel = nil
		}
		a.chat.FinishStreaming()
		if msg.Err != nil {
			a.chat.AddError(msg.Err.Error())
		}
		a.smartSpinner.Stop()
		a.spinning = false
		a.input.SetEnabled(true)
		a.status.SetText("Ready")
		return a, a.flushChat()

	case BtwDoneMsg:
		if a.sidePanelCancel != nil {
			a.sidePanelCancel()
		}
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
		if a.sidePanelCancel != nil {
			a.sidePanelCancel()
		}
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
		if a.sidePanelCancel != nil {
			a.sidePanelCancel()
		}
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
		if a.sidePanelCancel != nil {
			a.sidePanelCancel()
		}
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

	case OpenQuestionPanelMsg:
		// Refuse a nested open: forward a Cancelled outcome immediately so the
		// caller's askFn unblocks and the LLM gets a clear error.
		if a.questionPanel != nil {
			select {
			case msg.Reply <- QuestionPanelOutcome{Cancelled: true}:
			default:
			}
			return a, nil
		}
		panel, outcome := NewQuestionPanel(a.styles, msg.Questions)
		panel.SetSize(a.width, a.height)
		a.questionPanel = panel
		a.questionOutcome = outcome
		a.questionDeliver = msg.Reply
		a.prevInputEnabled = a.input.enabled
		a.input.SetEnabled(false)
		a.status.SetText("Awaiting your input...")
		return a, a.waitForQuestionOutcome()

	case CloseQuestionPanelMsg:
		if a.questionPanel == nil {
			return a, nil
		}
		// Cancel triggers the panel to send a Cancelled outcome on the channel,
		// which arrives via QuestionPanelDoneMsg below.
		a.questionPanel.Cancel()
		return a, nil

	case OpenPermissionPanelMsg:
		if a.permPanel != nil {
			select {
			case msg.Reply <- PermissionPanelOutcome{Cancelled: true}:
			default:
			}
			return a, nil
		}
		panel, outcome := NewPermissionPanel(a.styles, msg.Request)
		panel.SetSize(a.width, a.height)
		a.permPanel = panel
		a.permOutcome = outcome
		a.permDeliver = msg.Reply
		a.prevInputEnabled = a.input.enabled
		a.input.SetEnabled(false)
		a.status.SetText("Permission required...")
		return a, a.waitForPermOutcome()

	case PermissionPanelDoneMsg:
		if a.permDeliver != nil {
			select {
			case a.permDeliver <- msg.Outcome:
			default:
			}
		}
		a.permPanel = nil
		a.permOutcome = nil
		a.permDeliver = nil
		a.input.SetEnabled(a.prevInputEnabled)
		if a.runCancel == nil && !a.spinning {
			a.status.SetText("Ready")
		} else if a.runCancel != nil {
			a.status.SetText("Thinking...")
		}
		return a, nil

	case QuestionPanelDoneMsg:
		// Forward the outcome to the tool-side caller (if still waiting).
		if a.questionDeliver != nil {
			select {
			case a.questionDeliver <- msg.Outcome:
			default:
			}
		}
		a.questionPanel = nil
		a.questionOutcome = nil
		a.questionDeliver = nil
		a.input.SetEnabled(a.prevInputEnabled)
		// Status: leave "Thinking..." if the model is still running, else Ready.
		if a.runCancel == nil && !a.spinning {
			a.status.SetText("Ready")
		} else if a.runCancel != nil {
			a.status.SetText("Thinking...")
		}
		return a, nil
	}

	// Forward to input (always — allows type-ahead while model runs).
	{
		var cmd tea.Cmd
		a.input, cmd = a.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	return a, tea.Batch(cmds...)
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
			followCtx, followCancel := context.WithCancel(a.ctx)
			a.sidePanelCancel = followCancel
			sessionID := a.sidePanel.SessionID()
			return a, tea.Batch(a.smartSpinner.Tick(), a.runIMFollowUp(followCtx, sessionID, text))
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
	if a.questionPanel != nil {
		a.questionPanel.SetSize(a.width, a.height)
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
	// Permission panel takes highest precedence.
	if a.permPanel != nil {
		return a.handlePermPanelKey(msg)
	}
	// Question panel takes precedence over everything else.
	if a.questionPanel != nil {
		return a.handleQuestionPanelKey(msg)
	}
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
			btwCtx, btwCancel := context.WithCancel(a.ctx)
			a.sidePanelCancel = btwCancel
			return a, tea.Batch(a.smartSpinner.Tick(), a.runBtw(btwCtx, question))
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
			imCtx, imCancel := context.WithCancel(a.ctx)
			a.sidePanelCancel = imCancel
			return a, tea.Batch(a.smartSpinner.Tick(), a.runIM(imCtx, imSessionID, instruction))
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

	runCtx, runCancel := context.WithCancel(a.ctx)
	a.runCancel = runCancel
	a.smartSpinner.Start()
	return a, tea.Batch(a.smartSpinner.Tick(), a.runStream(runCtx, text), flush)
}

// displaySandbox renders the sandbox backend name for /status, defaulting to "host".
func displaySandbox(s string) string {
	if strings.TrimSpace(s) == "" {
		return "host"
	}
	return s
}

// handleQuestionPanelKey routes a key into the active question panel and emits
// a QuestionPanelDoneMsg once the panel reports finished.
func (a *App) handleQuestionPanelKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Ctrl+D still quits the whole app, even mid-question.
	if msg.String() == "ctrl+d" {
		// Forward a Cancelled outcome so the askFn caller doesn't deadlock.
		if a.questionDeliver != nil {
			select {
			case a.questionDeliver <- QuestionPanelOutcome{Cancelled: true}:
			default:
			}
		}
		return a, tea.Quit
	}
	cmd := a.questionPanel.HandleKey(msg)
	return a, cmd
}

// waitForQuestionOutcome returns a tea.Cmd that blocks on the panel's outcome
// channel and produces a QuestionPanelDoneMsg when the user finishes. Running
// inside tea.Cmd guarantees we don't block the Update loop.
func (a *App) waitForQuestionOutcome() tea.Cmd {
	ch := a.questionOutcome
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		outcome, ok := <-ch
		if !ok {
			return QuestionPanelDoneMsg{Outcome: QuestionPanelOutcome{Cancelled: true}}
		}
		return QuestionPanelDoneMsg{Outcome: outcome}
	}
}

func (a *App) handlePermPanelKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+d" {
		if a.permDeliver != nil {
			select {
			case a.permDeliver <- PermissionPanelOutcome{Cancelled: true}:
			default:
			}
		}
		return a, tea.Quit
	}
	a.permPanel.HandleKey(msg)
	return a, nil
}

func (a *App) waitForPermOutcome() tea.Cmd {
	ch := a.permOutcome
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		outcome, ok := <-ch
		if !ok {
			return PermissionPanelDoneMsg{Outcome: PermissionPanelOutcome{Cancelled: true}}
		}
		return PermissionPanelDoneMsg{Outcome: outcome}
	}
}
