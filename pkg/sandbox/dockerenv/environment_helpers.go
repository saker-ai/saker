package dockerenv

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

// environment_helpers.go holds the pure utility functions used across
// lifecycle and IO files: argv/quoting/path helpers and randomness.

func buildExecArgv(containerID, workdir string, env map[string]string, argv ...string) []string {
	out := []string{"exec"}
	if strings.TrimSpace(workdir) != "" {
		out = append(out, "-w", workdir)
	}
	for k, v := range env {
		out = append(out, "--env", fmt.Sprintf("%s=%s", k, v))
	}
	out = append(out, containerID)
	out = append(out, argv...)
	return out
}

// injectInteractive inserts `-i` after the leading `exec` so docker keeps
// stdin open. We need this for tar -xf - and cat > <path>.
func injectInteractive(argv []string) []string {
	if len(argv) == 0 || argv[0] != "exec" {
		return argv
	}
	out := make([]string, 0, len(argv)+1)
	out = append(out, "exec", "-i")
	out = append(out, argv[1:]...)
	return out
}

func randomSuffix() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// normalizeGuestPath joins `path` against workdir if not absolute, and
// returns a Clean'd POSIX path.
func normalizeGuestPath(path, workdir string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return workdir
	}
	if !strings.HasPrefix(path, "/") {
		path = filepath.Join(workdir, path)
	}
	return filepath.Clean(path)
}

// guestRootForPattern returns the longest non-glob prefix of `pattern`,
// e.g. "/app/src/**/*.go" -> "/app/src". Used so `find` doesn't traverse
// the entire filesystem when only a small subtree matters.
func guestRootForPattern(pattern string) string {
	parts := strings.Split(pattern, "/")
	out := []string{}
	for _, p := range parts {
		if strings.ContainsAny(p, "*?[") {
			break
		}
		out = append(out, p)
	}
	root := strings.Join(out, "/")
	if root == "" {
		return "/"
	}
	return root
}

// shellQuote single-quotes `s` for /bin/sh inside the container.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
