package landlockenv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
)

func prepareSession(_ context.Context, projectRoot string, opts *sandboxenv.LandlockOptions, session sandboxenv.SessionContext) (*sandboxenv.PreparedSession, error) {
	if opts == nil {
		return nil, fmt.Errorf("landlockenv: missing landlock config")
	}

	roPaths := []string{projectRoot}
	roPaths = append(roPaths, opts.AdditionalROPaths...)

	rwPaths := append([]string(nil), opts.AdditionalRWPaths...)
	rwPaths = append(rwPaths, "/tmp")

	if opts.AutoCreateSessionWorkspace {
		base := opts.SessionWorkspaceBase
		if base == "" {
			base = filepath.Join(projectRoot, "workspace")
		}
		hostPath := filepath.Join(base, session.SessionID)
		if err := os.MkdirAll(hostPath, 0o755); err != nil {
			return nil, fmt.Errorf("landlockenv: create session workspace: %w", err)
		}
		rwPaths = append(rwPaths, hostPath)
	}

	guestCwd := opts.DefaultGuestCwd
	if guestCwd == "" {
		guestCwd = projectRoot
	}

	prepared := &sandboxenv.PreparedSession{
		SessionID:   session.SessionID,
		GuestCwd:    guestCwd,
		SandboxType: "landlock",
		Meta: map[string]any{
			"project_root": projectRoot,
			"ro_paths":     roPaths,
			"rw_paths":     rwPaths,
		},
	}
	return prepared, nil
}
