package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cinience/saker/pkg/clikit"
	versionpkg "github.com/cinience/saker/pkg/version"
)

type multiValue []string

func (m *multiValue) String() string {
	return strings.Join(*m, ",")
}

func (m *multiValue) Set(value string) error {
	*m = append(*m, value)
	return nil
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
