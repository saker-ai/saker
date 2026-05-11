package clikit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/google/uuid"
)

// CommandHandler is called before built-in command dispatch. If it returns
// handled=true the built-in handler is skipped. Return quit=true to exit.
type CommandHandler func(input string, out io.Writer) (handled, quit bool)

type InteractiveShellConfig struct {
	Engine            ReplEngine
	InitialSessionID  string
	TimeoutMs         int
	Verbose           bool
	WaterfallMode     string
	ShowStatusPerTurn bool
	// CustomCommands is invoked before built-in slash commands.
	CustomCommands CommandHandler
	// BannerExtra is printed after the standard banner (e.g. background task info).
	BannerExtra string
}

type InteractiveShell struct {
	cfg InteractiveShellConfig
}

func NewInteractiveShell(cfg InteractiveShellConfig) *InteractiveShell {
	return &InteractiveShell{cfg: cfg}
}

func PrintBanner(out io.Writer, modelName string, metas []SkillMeta) {
	if out == nil {
		return
	}
	fmt.Fprintf(out, "\nAgentkit CLI\n")
	fmt.Fprintf(out, "Model: %s\n", modelName)
	fmt.Fprintf(out, "Skills: %d loaded\n", len(metas))
	fmt.Fprintf(out, "Commands: /btw /skills /new /session /model /help /quit\n")
}

func RunREPL(ctx context.Context, in io.ReadCloser, out, errOut io.Writer, eng ReplEngine, timeoutMs int, verbose bool, waterfallMode string, initialSessionID string) {
	_ = RunInteractiveShell(ctx, in, out, errOut, eng, timeoutMs, verbose, waterfallMode, initialSessionID)
}

// RunInteractiveShellOpts is the options variant of RunInteractiveShell that
// accepts an InteractiveShellConfig directly, allowing callers to set custom
// command handlers and banner text.
func RunInteractiveShellOpts(ctx context.Context, in io.ReadCloser, out, errOut io.Writer, cfg InteractiveShellConfig) error {
	shell := NewInteractiveShell(cfg)
	if err := shell.Run(ctx, in, out, errOut); err != nil && errOut != nil {
		fmt.Fprintf(errOut, "interactive shell failed: %v\n", err)
	}
	return nil
}

func RunInteractiveShell(ctx context.Context, in io.ReadCloser, out, errOut io.Writer, eng ReplEngine, timeoutMs int, verbose bool, waterfallMode string, initialSessionID string) error {
	return RunInteractiveShellOpts(ctx, in, out, errOut, InteractiveShellConfig{
		Engine:            eng,
		InitialSessionID:  initialSessionID,
		TimeoutMs:         timeoutMs,
		Verbose:           verbose,
		WaterfallMode:     waterfallMode,
		ShowStatusPerTurn: true,
	})
}

func (s *InteractiveShell) Run(ctx context.Context, in io.ReadCloser, out, errOut io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}
	if in == nil {
		in = io.NopCloser(strings.NewReader(""))
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "> ",
		Stdin:           in,
		Stdout:          nopWriteCloser{Writer: out},
		Stderr:          nopWriteCloser{Writer: errOut},
		HistoryLimit:    1000,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		fmt.Fprintf(errOut, "init repl failed: %v\n", err)
		return err
	}
	defer func() { _ = rl.Close() }()

	sessionID := strings.TrimSpace(s.cfg.InitialSessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	if s.cfg.BannerExtra != "" {
		fmt.Fprintln(out, s.cfg.BannerExtra)
	}

	var lastInterrupt time.Time
	const doubleInterruptWindow = 1 * time.Second

	for {
		if s.cfg.ShowStatusPerTurn {
			printShellStatus(out, s.cfg.Engine, sessionID)
		}
		line, err := rl.Readline()
		if isReadTermination(err) {
			break
		}
		if errors.Is(err, readline.ErrInterrupt) {
			now := time.Now()
			if now.Sub(lastInterrupt) < doubleInterruptWindow {
				// Double Ctrl+C: exit
				break
			}
			lastInterrupt = now
			fmt.Fprintln(out, "(press Ctrl+C again to exit)")
			continue
		}
		if err != nil {
			return fmt.Errorf("read failed: %w", err)
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		// Try custom commands first, then built-in commands.
		if s.cfg.CustomCommands != nil {
			if handled, quit := s.cfg.CustomCommands(input, out); handled {
				if quit {
					return nil
				}
				continue
			}
		}

		// Keep the legacy REPL stable; new interactive features belong in
		// pkg/clikit/tui/app.go.

		// Handle /btw side question (needs ctx and errOut, so handled here
		// rather than in handleCommand).
		if strings.HasPrefix(strings.ToLower(input), "/btw") {
			question := strings.TrimSpace(input[4:])
			if question == "" {
				fmt.Fprintln(out, "usage: /btw <question>")
			} else if err := RunSideQuestion(ctx, out, errOut, s.cfg.Engine, question); err != nil {
				fmt.Fprintf(errOut, "btw failed: %v\n", err)
			}
			continue
		}

		if handled, quit := handleCommand(input, s.cfg.Engine, &sessionID, out); handled {
			if quit {
				return nil
			}
			continue
		}

		// Use a cancellable context so Ctrl+C (SIGINT) can interrupt the
		// current model execution without killing the entire REPL.
		runCtx, runCancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			done <- RunStream(runCtx, out, errOut, s.cfg.Engine, sessionID, input, s.cfg.TimeoutMs, s.cfg.Verbose, s.cfg.WaterfallMode)
		}()

		// Wait for either completion or interrupt.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		select {
		case err := <-done:
			signal.Stop(sigCh)
			if err != nil {
				fmt.Fprintf(errOut, "run failed: %v\n", err)
			}
		case <-sigCh:
			signal.Stop(sigCh)
			runCancel()
			<-done // wait for goroutine to finish
			lastInterrupt = time.Now()
			fmt.Fprintln(out, "\ninterrupted (press Ctrl+C again to exit)")
			continue
		}
		runCancel() // ensure cleanup
	}
	fmt.Fprintln(out, "bye")
	return nil
}

type nopWriteCloser struct {
	io.Writer
}

func (n nopWriteCloser) Close() error { return nil }

func isReadTermination(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, io.EOF)
}

func handleCommand(input string, eng ReplEngine, sessionID *string, out io.Writer) (handled, quit bool) {
	if out == nil {
		out = io.Discard
	}
	cmd := strings.ToLower(strings.Fields(input)[0])
	switch cmd {
	case "/quit", "/exit", "/q":
		fmt.Fprintln(out, "bye")
		return true, true
	case "/new":
		*sessionID = uuid.NewString()
		fmt.Fprintln(out, "new conversation")
		return true, false
	case "/model":
		fmt.Fprintf(out, "model: %s\n", eng.ModelName())
		return true, false
	case "/session":
		fmt.Fprintf(out, "session: %s\n", *sessionID)
		return true, false
	case "/status":
		printShellStatus(out, eng, *sessionID)
		return true, false
	case "/help":
		fmt.Fprintln(out, "/btw <question> /skills /status /new /session /model /help /quit")
		return true, false
	case "/skills":
		metas := eng.Skills()
		sort.Slice(metas, func(i, j int) bool { return metas[i].Name < metas[j].Name })
		for _, m := range metas {
			fmt.Fprintf(out, "- %s\n", m.Name)
		}
		return true, false
	}
	return false, false
}

func printShellStatus(out io.Writer, eng ReplEngine, sessionID string) {
	if out == nil || eng == nil {
		return
	}
	fmt.Fprintf(out, "Session: %s | Model: %s | Repo: %s | Sandbox: %s | Skills: %d\n",
		sessionID, eng.ModelName(), eng.RepoRoot(), displayValue(eng.SandboxBackend(), "host"), len(eng.Skills()))
}

func displayValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
