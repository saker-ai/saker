//go:build !linux

package landlockenv

// Available reports whether the running kernel supports Landlock.
// On non-Linux platforms, Landlock is never available.
func Available() bool {
	return false
}
