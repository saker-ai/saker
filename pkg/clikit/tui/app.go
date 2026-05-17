// app.go: App struct, AppConfig, constructor, and Run entry point.
package tui

import (
	"context"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/saker-ai/saker/pkg/clikit"
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

	// question panel overlay (for AskUserQuestion tool)
	questionPanel    *QuestionPanel
	questionOutcome  <-chan QuestionPanelOutcome
	questionDeliver  chan<- QuestionPanelOutcome // bridge channel back to askFn caller
	prevInputEnabled bool                        // saved input state to restore after panel closes

	// program is set by Run() so that cross-thread tool callers can use program.Send().
	program *tea.Program
}

// New creates a new TUI App.
func New(ctx context.Context, cfg AppConfig) *App {
	theme := DetectTheme()
	styles := NewStyles(theme)

	sessionID := strings.TrimSpace(cfg.InitialSessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	appCtx, appCancel := context.WithCancel(ctx) //nolint:govet // Cancel is retained on App.cancel and invoked from App.Stop.

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

// Run starts the bubbletea program.
func Run(ctx context.Context, cfg AppConfig) error {
	app := New(ctx, cfg)
	p := tea.NewProgram(app)
	app.program = p
	// Wire the interactive AskUserQuestion handler so that tool calls invoked
	// from agent goroutines can prompt the user via the bubbletea event loop.
	if r, ok := cfg.Engine.(clikit.AskQuestionRegistrar); ok {
		r.SetAskQuestionFunc(app.askQuestionFromTUI)
		defer r.SetAskQuestionFunc(nil)
	}
	_, err := p.Run()
	return err
}
