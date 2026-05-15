// openaikeys.go: CLI entry point for `saker openai-key <action>` subcommands.
//
// This is the operator-side counterpart to the OpenAI gateway: it manipulates
// the Bearer keys that authenticate /v1/* requests.
//
// Actions:
//
//	create  — generate a new key (plaintext shown ONCE)
//	list    — show all keys owned by a user (prefix only, no plaintext)
//	revoke  — mark a key revoked by id (idempotent)
//
// All actions reuse the same SQLite/Postgres store as `saker --server`,
// resolved from SAKER_DB_DSN with the same data-dir fallback the server uses.
package project

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

// RunOpenAIKeyCommand handles `saker openai-key <action>` subcommands.
func RunOpenAIKeyCommand(stdout, stderr io.Writer, args []string) error {
	if len(args) == 0 {
		printOpenAIKeyUsage(stderr)
		return fmt.Errorf("openai-key: action required (create|list|revoke)")
	}

	action := args[0]
	rest := args[1:]

	switch action {
	case "create":
		return runOpenAIKeyCreate(stdout, stderr, rest)
	case "list":
		return runOpenAIKeyList(stdout, stderr, rest)
	case "revoke":
		return runOpenAIKeyRevoke(stdout, stderr, rest)
	case "-h", "--help", "help":
		printOpenAIKeyUsage(stdout)
		return nil
	default:
		printOpenAIKeyUsage(stderr)
		return fmt.Errorf("openai-key: unknown action %q (use create, list, or revoke)", action)
	}
}

func printOpenAIKeyUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: saker openai-key <action> [options]

Manage Bearer API keys for the OpenAI-compatible /v1/* gateway. The key
plaintext is shown ONCE at create time and never persisted in plaintext.

Actions:
  create   --user <name> [--project <id|new>] [--name <label>] [--data-dir <path>]
           Generate a new key. --user defaults to the OS user. --project=new
           provisions a personal project for the user when one isn't supplied.

  list     --user <name> [--data-dir <path>]
           Show keys for a user (prefix, name, last_used_at, revoked).

  revoke   --id <api_key_id> [--data-dir <path>]
           Mark a key revoked. Future requests with that token return 401.`)
}

// ResolveStore opens the same project store the server uses. dataDir overrides
// the default ~/.saker/server. SAKER_DB_DSN takes precedence over the
// SQLite fallback if set, matching cmd_server.go behavior.
func ResolveStore(dataDir string) (*Store, error) {
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".saker", "server")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("openai-key: ensure data dir: %w", err)
	}
	store, err := Open(Config{
		DSN:          os.Getenv("SAKER_DB_DSN"),
		FallbackPath: filepath.Join(dataDir, "app.db"),
	})
	if err != nil {
		return nil, fmt.Errorf("openai-key: open store: %w", err)
	}
	return store, nil
}

// ResolveUser finds (or provisions) the user row for username. The CLI is
// the only authenticated path that runs outside the HTTP server, so we use
// the same EnsureLocalhostUser path the localhost-bypass auth uses — the
// resulting user row is the one the gateway will see when the key is used.
func ResolveUser(ctx context.Context, store *Store, username string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		// Fall back to OS user. Mirrors how the server's localhost dev
		// bypass derives identity, so the same key works in both paths.
		osUser, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("openai-key: derive default user: %w", err)
		}
		username = osUser.Username
	}
	// Look up an existing localhost user first; only create one if missing.
	row, err := store.LookupUserByUsername(ctx, username, UserSourceLocalhost)
	if err == nil && row != nil {
		return row, nil
	}
	// EnsureLocalhostUser uses osUID as the external id; for non-default
	// usernames we pass the username itself so the row stays unique.
	osUID := username
	if curr, currErr := user.Current(); currErr == nil && curr.Username == username {
		osUID = curr.Uid
	}
	return store.EnsureLocalhostUser(ctx, osUID)
}

func runOpenAIKeyCreate(stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("openai-key create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	username := fs.String("user", "", "Owner username (default: OS user)")
	projectID := fs.String("project", "", "Project id to scope the key to (use 'new' to provision a personal project)")
	keyName := fs.String("name", "", "Human-readable label for the key (e.g. 'ci-pipeline')")
	dataDir := fs.String("data-dir", "", "Server data directory (default: ~/.saker/server)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	store, err := ResolveStore(*dataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	usr, err := ResolveUser(ctx, store, *username)
	if err != nil {
		return err
	}

	// Resolve project: empty means "any project the user has access to";
	// "new" provisions a personal project so the key has a real scope.
	pid := strings.TrimSpace(*projectID)
	if strings.EqualFold(pid, "new") {
		p, perr := store.EnsurePersonalProject(ctx, usr.ID)
		if perr != nil {
			return fmt.Errorf("openai-key create: ensure personal project: %w", perr)
		}
		pid = p.ID
	}

	res, err := store.CreateAPIKey(ctx, usr.ID, pid, *keyName)
	if err != nil {
		return fmt.Errorf("openai-key create: %w", err)
	}

	fmt.Fprintln(stdout, "API key created. The plaintext below is shown ONCE — copy it now:")
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "  %s\n", res.Plaintext)
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "  id          : %s\n", res.APIKey.ID)
	fmt.Fprintf(stdout, "  user        : %s (%s)\n", usr.DisplayName, usr.ID)
	if pid != "" {
		fmt.Fprintf(stdout, "  project     : %s\n", pid)
	} else {
		fmt.Fprintln(stdout, "  project     : (any — admin-style key)")
	}
	if res.APIKey.Name != "" {
		fmt.Fprintf(stdout, "  label       : %s\n", res.APIKey.Name)
	}
	fmt.Fprintf(stdout, "  prefix      : ak_%s…\n", res.APIKey.Prefix)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Use with: curl -H 'Authorization: Bearer "+res.Plaintext+"' http://127.0.0.1:10112/v1/models")
	return nil
}

func runOpenAIKeyList(stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("openai-key list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	username := fs.String("user", "", "Owner username (default: OS user)")
	dataDir := fs.String("data-dir", "", "Server data directory (default: ~/.saker/server)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	store, err := ResolveStore(*dataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	usr, err := ResolveUser(ctx, store, *username)
	if err != nil {
		return err
	}

	rows, err := store.ListAPIKeys(ctx, usr.ID)
	if err != nil {
		return fmt.Errorf("openai-key list: %w", err)
	}

	if len(rows) == 0 {
		fmt.Fprintf(stdout, "No keys for user %s\n", usr.DisplayName)
		return nil
	}

	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPREFIX\tNAME\tPROJECT\tCREATED\tLAST_USED\tSTATUS")
	for _, r := range rows {
		status := "active"
		if r.RevokedAt != nil {
			status = "revoked"
		}
		last := "-"
		if r.LastUsedAt != nil {
			last = r.LastUsedAt.Format(time.RFC3339)
		}
		proj := r.ProjectID
		if proj == "" {
			proj = "(any)"
		}
		name := r.Name
		if name == "" {
			name = "-"
		}
		fmt.Fprintf(tw, "%s\tak_%s…\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, r.Prefix, name, proj,
			r.CreatedAt.Format(time.RFC3339), last, status)
	}
	return tw.Flush()
}

func runOpenAIKeyRevoke(stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("openai-key revoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keyID := fs.String("id", "", "API key id to revoke (see 'openai-key list')")
	dataDir := fs.String("data-dir", "", "Server data directory (default: ~/.saker/server)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*keyID) == "" {
		return fmt.Errorf("openai-key revoke: --id is required")
	}

	store, err := ResolveStore(*dataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.RevokeAPIKey(context.Background(), *keyID); err != nil {
		return fmt.Errorf("openai-key revoke: %w", err)
	}
	fmt.Fprintf(stdout, "Revoked API key %s\n", *keyID)
	return nil
}
