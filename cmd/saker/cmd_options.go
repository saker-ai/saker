package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/saker-ai/saker/pkg/api"
	modelpkg "github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/provider"
)

// buildModelProvider delegates to the shared provider.Detect function.
func buildModelProvider(providerFlag, modelFlag, system string) (modelpkg.Provider, string) {
	return provider.Detect(providerFlag, modelFlag, system)
}

func buildSandboxOptions(projectRoot, backend, projectMountMode, offlineImage string) (api.SandboxOptions, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		projectRoot = "."
	}
	absProjectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return api.SandboxOptions{}, fmt.Errorf("resolve project root: %w", err)
	}
	projectRoot = absProjectRoot
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" {
		backend = "host"
	}
	projectMountMode = strings.ToLower(strings.TrimSpace(projectMountMode))
	if projectMountMode == "" {
		projectMountMode = "ro"
	}
	switch projectMountMode {
	case "ro", "rw", "off":
	default:
		return api.SandboxOptions{}, fmt.Errorf("invalid --sandbox-project-mount %q (expected ro|rw|off)", projectMountMode)
	}

	switch backend {
	case "host":
		return api.SandboxOptions{}, nil
	case "gvisor":
		opts := api.SandboxOptions{
			Type: "gvisor",
			GVisor: &api.GVisorOptions{
				Enabled:                    true,
				DefaultGuestCwd:            "/workspace",
				AutoCreateSessionWorkspace: true,
				SessionWorkspaceBase:       filepath.Join(projectRoot, "workspace"),
			},
		}
		if projectMountMode != "off" {
			opts.GVisor.Mounts = append(opts.GVisor.Mounts, api.MountSpec{
				HostPath:  projectRoot,
				GuestPath: "/project",
				ReadOnly:  projectMountMode != "rw",
			})
		}
		return opts, nil
	case "govm":
		if strings.TrimSpace(offlineImage) == "" {
			offlineImage = "py312-alpine"
		}
		opts := api.SandboxOptions{
			Type: "govm",
			Govm: &api.GovmOptions{
				Enabled:                    true,
				DefaultGuestCwd:            "/workspace",
				AutoCreateSessionWorkspace: true,
				SessionWorkspaceBase:       filepath.Join(projectRoot, "workspace"),
				RuntimeHome:                filepath.Join(projectRoot, ".govm"),
				OfflineImage:               offlineImage,
			},
		}
		if projectMountMode != "off" {
			opts.Govm.Mounts = append(opts.Govm.Mounts, api.MountSpec{
				HostPath:  projectRoot,
				GuestPath: "/project",
				ReadOnly:  projectMountMode != "rw",
			})
		}
		return opts, nil
	case "landlock":
		opts := api.SandboxOptions{
			Type: "landlock",
			Landlock: &api.LandlockOptions{
				Enabled:                    true,
				DefaultGuestCwd:            projectRoot,
				AutoCreateSessionWorkspace: true,
				SessionWorkspaceBase:       filepath.Join(projectRoot, "workspace"),
			},
		}
		return opts, nil
	default:
		return api.SandboxOptions{}, fmt.Errorf("invalid --sandbox-backend %q (expected host|gvisor|govm|landlock)", backend)
	}
}
