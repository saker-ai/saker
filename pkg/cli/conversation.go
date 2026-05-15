package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/saker-ai/saker/pkg/conversation"
)

// openConversationStoreForCLI opens the event-sourced conversation log for
// the CLI surface. Returns nil (and logs a warning to stderr) when the
// store cannot be opened — a broken SQLite file never wedges the CLI.
//
// The DB file lives at <configBase>/conversation.db. Returns nil when
// neither projectRoot nor configRoot resolves to a writable base path.
func openConversationStoreForCLI(projectRoot, configRoot string, stderr io.Writer) *conversation.Store {
	base := resolveCLIConfigBase(projectRoot, configRoot)
	if base == "" {
		return nil
	}
	dsn := filepath.Join(base, "conversation.db")
	st, err := conversation.Open(conversation.Config{DSN: dsn})
	if err != nil {
		fmt.Fprintf(stderr, "warn: open conversation store: %v\n", err)
		return nil
	}
	return st
}

// resolveCLIConfigBase mirrors pkg/api.resolveConfigBase: prefer
// configRoot when set, otherwise fall back to "<projectRoot>/.saker".
// Returns "" when both inputs are empty.
func resolveCLIConfigBase(projectRoot, configRoot string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	configRoot = strings.TrimSpace(configRoot)
	if projectRoot == "" && configRoot == "" {
		return ""
	}
	base := configRoot
	if base == "" {
		base = filepath.Join(projectRoot, ".saker")
	} else if !filepath.IsAbs(base) && projectRoot != "" {
		base = filepath.Join(projectRoot, base)
	}
	return base
}
