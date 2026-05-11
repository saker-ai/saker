package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/logging"
	"github.com/cinience/saker/pkg/project"
	"github.com/cinience/saker/pkg/sandbox/landlockenv"
	"github.com/cinience/saker/pkg/server"
)

// runServerMode starts the embedded HTTP server, wires the project store,
// auto-enables Landlock when available, and resolves web auth credentials.
func runServerMode(stdout, stderr io.Writer, opts api.Options, addr, dataDir, staticDir, logDir string, debug bool) error {
	opts.EntryPoint = api.EntryPointPlatform

	// Auto-enable Landlock sandbox when kernel supports it and user didn't
	// explicitly choose a sandbox backend.
	if opts.Sandbox.Type == "" && landlockenv.Available() {
		absRoot, _ := filepath.Abs(opts.ProjectRoot)
		if absRoot == "" {
			absRoot = opts.ProjectRoot
		}
		opts.Sandbox = api.SandboxOptions{
			Type: "landlock",
			Landlock: &api.LandlockOptions{
				Enabled:                    true,
				DefaultGuestCwd:            absRoot,
				AutoCreateSessionWorkspace: true,
				SessionWorkspaceBase:       filepath.Join(absRoot, "workspace"),
			},
		}
		fmt.Fprintln(stdout, "Landlock sandbox auto-enabled (kernel support detected)")
	}

	// Default data directory for session persistence.
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".saker", "server")
	}

	// Expose canvas storage to built-in tools (canvas_get_node).
	opts.CanvasDir = filepath.Join(dataDir, "canvas")

	// Initialize structured logger for server mode.
	if logDir == "" {
		logDir = filepath.Join(dataDir, "logs")
	}
	logger, logCleanup, logErr := logging.Setup(logDir)
	if logErr != nil {
		fmt.Fprintf(stderr, "Warning: failed to setup file logging: %v\n", logErr)
	}
	if logCleanup != nil {
		defer logCleanup()
	}
	if logger != nil {
		logger.Info("server log initialized", "log_dir", logDir)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if logger != nil {
		ctx = logging.WithLogger(ctx, logger)
	}

	rt, err := runtimeFactory(ctx, opts)
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}
	defer rt.Close()

	// Open the multi-tenant project store. DSN comes from SAKER_DB_DSN
	// (sqlite/postgres); empty falls back to <dataDir>/app.db (sqlite).
	projectStore, err := project.Open(project.Config{
		DSN:          os.Getenv("SAKER_DB_DSN"),
		FallbackPath: filepath.Join(dataDir, "app.db"),
	})
	if err != nil {
		return fmt.Errorf("open project store: %w", err)
	}
	defer projectStore.Close()

	srvOpts := server.Options{
		Addr:         addr,
		DataDir:      dataDir,
		Debug:        debug,
		Logger:       logger,
		ProjectStore: projectStore,
	}
	if staticDir != "" {
		srvOpts.StaticDir = staticDir
	} else {
		sub, subErr := getEmbeddedFrontend()
		if subErr != nil {
			fmt.Fprintf(stderr, "Warning: %v, serving API only\n", subErr)
		} else {
			srvOpts.StaticFS = sub
		}
	}
	// Mount the OpenCut-derived browser editor at /editor/. Non-fatal on
	// failure — the main app still works; only the editor sub-app is
	// unavailable. An empty placeholder dist still yields a valid FS.
	if editorSub, editorErr := getEmbeddedEditor(); editorErr != nil {
		fmt.Fprintf(stderr, "Warning: editor sub-app unavailable: %v\n", editorErr)
	} else {
		srvOpts.StaticEditorFS = editorSub
	}

	apiRuntime, ok := rt.(*api.Runtime)
	if !ok {
		return fmt.Errorf("server mode requires api.Runtime")
	}

	// Resolve web auth config: use existing settings or auto-generate credentials.
	if settings := apiRuntime.Settings(); settings != nil && settings.WebAuth != nil && settings.WebAuth.Password != "" {
		srvOpts.WebAuth = settings.WebAuth
		username := srvOpts.WebAuth.Username
		if username == "" {
			username = "admin"
		}
		fmt.Fprintf(stdout, "Web auth enabled: username=%s (remote access only)\n", username)
	} else {
		plain, hash, genErr := server.GeneratePassword()
		if genErr != nil {
			fmt.Fprintf(stderr, "Warning: failed to generate auth credentials: %v\n", genErr)
		} else {
			srvOpts.WebAuth = &config.WebAuthConfig{Username: "admin", Password: hash}
			// Write initial password to a file instead of stdout so it
			// doesn't persist in infrastructure logs.
			pwFile := filepath.Join(srvOpts.DataDir, "initial-password.txt")
			if writeErr := os.WriteFile(pwFile, []byte(plain), 0o600); writeErr != nil {
				fmt.Fprintf(stderr, "Warning: failed to write initial password file: %v\n", writeErr)
				// Fall back to stdout only if file write fails.
				fmt.Fprintf(stdout, "Web auth: username=admin password=%s (remote access only)\n", plain)
			} else {
				fmt.Fprintf(stdout, "Web auth: username=admin — initial password written to %s (remote access only)\n", pwFile)
			}
			// Persist to settings.local.json so the password survives restarts.
			if saveErr := config.SaveSettingsLocal(opts.ProjectRoot, &config.Settings{WebAuth: srvOpts.WebAuth}); saveErr != nil {
				fmt.Fprintf(stderr, "Warning: failed to save auth config: %v\n", saveErr)
			}
		}
	}

	srv, err := server.New(apiRuntime, srvOpts)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	fmt.Fprintf(stdout, "Saker server listening on %s\n", addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-sigCh:
		fmt.Fprintln(stdout, "\nShutting down...")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		return srv.Shutdown(shutCtx)
	}
}
