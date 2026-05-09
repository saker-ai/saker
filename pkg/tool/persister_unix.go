//go:build !windows

package tool

import "path/filepath"

func toolOutputBaseDir() string {
	return filepath.Join(string(filepath.Separator), "tmp", "saker", "tool-output")
}
