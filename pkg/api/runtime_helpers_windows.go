//go:build windows

package api

import (
	"os"
	"path/filepath"
)

func bashOutputBaseDir() string {
	return filepath.Join(os.TempDir(), "saker", "bash-output")
}

func toolOutputBaseDir() string {
	return filepath.Join(os.TempDir(), "saker", "tool-output")
}
