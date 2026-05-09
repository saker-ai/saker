//go:build windows

package toolbuiltin

import "os"

// newFileMode returns the default mode for new files on Windows (no umask concept).
func newFileMode() os.FileMode {
	return 0o666
}
