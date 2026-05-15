package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/saker-ai/saker/pkg/clikit"
	versionpkg "github.com/saker-ai/saker/pkg/version"
	"github.com/mattn/go-isatty"
)

// resolveTUIMode interprets the --tui flag value (auto|on|off, also accepting
// true/false/1/0/yes/no for backwards compatibility). When mode is "auto", it
// returns true iff both stdin and stdout are TTYs — this avoids launching the
// bubbletea TUI in pipe / CI / non-tty environments where its alt-screen and
// ANSI escapes would corrupt the output stream.
func resolveTUIMode(setting string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case "on", "true", "1", "yes":
		return true, nil
	case "off", "false", "0", "no":
		return false, nil
	case "auto", "":
		return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd()), nil
	default:
		return false, fmt.Errorf("invalid --tui value %q (want auto|on|off)", setting)
	}
}

type multiValue []string

func (m *multiValue) String() string {
	return strings.Join(*m, ",")
}

func (m *multiValue) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func containsTool(list []string, name string) bool {
	target := strings.ToLower(strings.TrimSpace(name))
	repl := strings.NewReplacer("-", "_", " ", "_")
	for _, v := range list {
		key := strings.ToLower(strings.TrimSpace(v))
		key = repl.Replace(key)
		if key == target {
			return true
		}
	}
	return false
}

func splitMultiValue(m multiValue) []string {
	if len(m) == 0 {
		return nil
	}
	var result []string
	for _, v := range m {
		for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
			if s := strings.TrimSpace(part); s != "" {
				result = append(result, s)
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// promptUpgrade asks the user whether to upgrade and performs the upgrade if accepted.
// Returns true if the upgrade was performed (caller should exit), false otherwise.
func promptUpgrade(stdout, stderr io.Writer, info *versionpkg.UpdateInfo) bool {
	fmt.Fprintf(stdout, "\nUpdate available: v%s -> v%s\n", info.Current, info.Latest)
	if info.Message != "" {
		fmt.Fprintf(stdout, "  %s\n", info.Message)
	}
	fmt.Fprintf(stdout, "\nUpgrade now? [Y/n/r(release notes)] ")

	reader := bufio.NewReader(os.Stdin)
	for {
		line, _ := reader.ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))

		switch answer {
		case "", "y", "yes":
			fmt.Fprintln(stdout)
			if err := versionpkg.SelfUpgrade(info.Latest, func(msg string) {
				fmt.Fprintf(stdout, "  %s\n", msg)
			}); err != nil {
				fmt.Fprintf(stderr, "Upgrade failed: %v\n", err)
				fmt.Fprintln(stdout, "Continuing with current version...")
				return false
			}
			fmt.Fprintf(stdout, "\n  Successfully upgraded to v%s. Restarting...\n\n", info.Latest)
			if err := versionpkg.Restart(); err != nil {
				fmt.Fprintf(stderr, "Restart failed: %v\n", err)
				fmt.Fprintln(stdout, "Please restart saker manually.")
			}
			return true
		case "n", "no":
			fmt.Fprintln(stdout)
			return false
		case "r", "release":
			if info.ReleaseURL != "" {
				fmt.Fprintf(stdout, "  Release notes: %s/tag/v%s\n", info.ReleaseURL, info.Latest)
			} else {
				fmt.Fprintln(stdout, "  No release URL available.")
			}
			fmt.Fprintf(stdout, "\nUpgrade now? [Y/n] ")
		default:
			fmt.Fprintf(stdout, "  Please enter Y, n, or r: ")
		}
	}
}

func parseTags(values multiValue) map[string]string {
	if len(values) == 0 {
		return nil
	}
	tags := map[string]string{}
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		val := "true"
		if len(parts) == 2 {
			val = strings.TrimSpace(parts[1])
		}
		tags[key] = val
	}
	return tags
}

func clikitTurnRecorder() *clikit.TurnRecorder {
	return clikit.NewTurnRecorder()
}

func envOr(def string, names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return def
}

func envOrInt(def int, names ...string) int {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			if i, err := strconv.Atoi(v); err == nil {
				return i
			}
		}
	}
	return def
}

func envOrBool(def bool, names ...string) bool {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			switch strings.ToLower(v) {
			case "1", "true", "yes", "on":
				return true
			case "0", "false", "no", "off":
				return false
			}
		}
	}
	return def
}
