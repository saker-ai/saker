//go:build windows

package version

import (
	"os"
	"os/exec"
)

func syscallExec(argv0 string, argv []string, envv []string) error {
	cmd := exec.Command(argv0, argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = envv
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
