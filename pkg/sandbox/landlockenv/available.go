//go:build linux

package landlockenv

import (
	"sync"
	"syscall"
)

// SYS_LANDLOCK_CREATE_RULESET is the syscall number for landlock_create_ruleset.
// Available since Linux 5.13.
const sysLandlockCreateRuleset = 444

var (
	availableOnce   sync.Once
	availableResult bool
)

// Available reports whether the running kernel supports Landlock.
// The result is cached after the first call.
func Available() bool {
	availableOnce.Do(func() {
		availableResult = probeLandlock()
	})
	return availableResult
}

// probeLandlock attempts a landlock_create_ruleset syscall with a nil attr
// and size 0 to check kernel support. A supported kernel returns EINVAL or
// EFAULT (bad args but syscall exists); an unsupported kernel returns ENOSYS.
func probeLandlock() bool {
	_, _, errno := syscall.RawSyscall(sysLandlockCreateRuleset, 0, 0, 0)
	// ENOSYS means the syscall doesn't exist (kernel too old or Landlock disabled).
	// Any other errno (EINVAL, EFAULT, etc.) means the syscall exists.
	return errno != syscall.ENOSYS
}
