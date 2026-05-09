//go:build !windows

package toolbuiltin

import "path/filepath"

func bashOutputBaseDir() string {
	return filepath.Join(string(filepath.Separator), "tmp", "saker", "bash-output")
}
