//go:build !windows

package toolbuiltin

import (
	"os"
	"syscall"
)

// newFileMode returns 0o666 with the current process umask applied,
// matching the permissions the OS would assign to a newly created file.
func newFileMode() os.FileMode {
	umask := syscall.Umask(0)
	syscall.Umask(umask)
	return os.FileMode(0o666) &^ os.FileMode(umask)
}
