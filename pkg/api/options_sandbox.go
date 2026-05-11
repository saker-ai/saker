package api

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	coreevents "github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/sandbox"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/security"
)

// SandboxOptions mirrors sandbox.Manager construction knobs exposed at the API
// layer so callers can customise filesystem/network/resource guards without
// touching lower-level packages.
type SandboxOptions struct {
	Type          string
	Root          string
	AllowedPaths  []string
	NetworkAllow  []string
	ResourceLimit sandbox.ResourceLimits
	GVisor        *sandboxenv.GVisorOptions
	Govm          *sandboxenv.GovmOptions
	Landlock      *sandboxenv.LandlockOptions
}

type GVisorOptions = sandboxenv.GVisorOptions
type GovmOptions = sandboxenv.GovmOptions
type LandlockOptions = sandboxenv.LandlockOptions
type MountSpec = sandboxenv.MountSpec

// PermissionRequest captures a permission prompt for sandbox "ask" matches.
type PermissionRequest struct {
	ToolName   string
	ToolParams map[string]any
	SessionID  string
	Rule       string
	Target     string
	Reason     string
	Approval   *security.ApprovalRecord
}

// PermissionRequestHandler lets hosts synchronously allow/deny PermissionAsk decisions.
type PermissionRequestHandler func(context.Context, PermissionRequest) (coreevents.PermissionDecisionType, error)

// SandboxReport documents the sandbox configuration attached to the runtime.
type SandboxReport struct {
	Roots          []string
	AllowedPaths   []string
	AllowedDomains []string
	ResourceLimits sandbox.ResourceLimits
}

func freezeSandboxOptions(in SandboxOptions) SandboxOptions {
	out := in
	if len(in.AllowedPaths) > 0 {
		out.AllowedPaths = append([]string(nil), in.AllowedPaths...)
	}
	if len(in.NetworkAllow) > 0 {
		out.NetworkAllow = append([]string(nil), in.NetworkAllow...)
	}
	if in.GVisor != nil {
		gv := *in.GVisor
		if len(in.GVisor.Mounts) > 0 {
			gv.Mounts = append([]sandboxenv.MountSpec(nil), in.GVisor.Mounts...)
		}
		out.GVisor = &gv
	}
	if in.Govm != nil {
		gv := *in.Govm
		if len(in.Govm.Mounts) > 0 {
			gv.Mounts = append([]sandboxenv.MountSpec(nil), in.Govm.Mounts...)
		}
		out.Govm = &gv
	}
	if in.Landlock != nil {
		ll := *in.Landlock
		if len(in.Landlock.AdditionalROPaths) > 0 {
			ll.AdditionalROPaths = append([]string(nil), in.Landlock.AdditionalROPaths...)
		}
		if len(in.Landlock.AdditionalRWPaths) > 0 {
			ll.AdditionalRWPaths = append([]string(nil), in.Landlock.AdditionalRWPaths...)
		}
		out.Landlock = &ll
	}
	return out
}

func normalizeSandboxOptions(projectRoot string, in SandboxOptions) SandboxOptions {
	out := in
	if out.Landlock != nil {
		ll := *out.Landlock
		if strings.TrimSpace(ll.HelperModeFlag) == "" {
			ll.HelperModeFlag = "--saker-landlock-helper"
		}
		if strings.TrimSpace(ll.DefaultGuestCwd) == "" {
			ll.DefaultGuestCwd = projectRoot
		}
		if ll.AutoCreateSessionWorkspace {
			if strings.TrimSpace(ll.SessionWorkspaceBase) == "" {
				ll.SessionWorkspaceBase = filepath.Join(projectRoot, "workspace")
			} else if !filepath.IsAbs(ll.SessionWorkspaceBase) {
				ll.SessionWorkspaceBase = filepath.Join(projectRoot, ll.SessionWorkspaceBase)
			}
			ll.SessionWorkspaceBase = filepath.Clean(ll.SessionWorkspaceBase)
		}
		out.Landlock = &ll
	}
	if out.GVisor != nil {
		gv := *out.GVisor
		if strings.TrimSpace(gv.HelperModeFlag) == "" {
			gv.HelperModeFlag = "--saker-gvisor-helper"
		}
		if strings.TrimSpace(gv.DefaultGuestCwd) == "" {
			gv.DefaultGuestCwd = "/workspace"
		}
		if gv.AutoCreateSessionWorkspace || len(gv.Mounts) == 0 {
			gv.AutoCreateSessionWorkspace = true
		}
		if strings.TrimSpace(gv.SessionWorkspaceBase) == "" {
			gv.SessionWorkspaceBase = filepath.Join(projectRoot, "workspace")
		} else if !filepath.IsAbs(gv.SessionWorkspaceBase) {
			gv.SessionWorkspaceBase = filepath.Join(projectRoot, gv.SessionWorkspaceBase)
		}
		gv.SessionWorkspaceBase = filepath.Clean(gv.SessionWorkspaceBase)
		if len(gv.Mounts) > 0 {
			mounts := make([]MountSpec, len(gv.Mounts))
			copy(mounts, gv.Mounts)
			gv.Mounts = mounts
		}
		out.GVisor = &gv
	}
	if out.Govm != nil {
		gv := *out.Govm
		if strings.TrimSpace(gv.DefaultGuestCwd) == "" {
			gv.DefaultGuestCwd = "/workspace"
		}
		if gv.AutoCreateSessionWorkspace || len(gv.Mounts) == 0 {
			gv.AutoCreateSessionWorkspace = true
		}
		if strings.TrimSpace(gv.SessionWorkspaceBase) == "" {
			gv.SessionWorkspaceBase = filepath.Join(projectRoot, "workspace")
		} else if !filepath.IsAbs(gv.SessionWorkspaceBase) {
			gv.SessionWorkspaceBase = filepath.Join(projectRoot, gv.SessionWorkspaceBase)
		}
		gv.SessionWorkspaceBase = filepath.Clean(gv.SessionWorkspaceBase)
		if strings.TrimSpace(gv.RuntimeHome) == "" {
			gv.RuntimeHome = filepath.Join(projectRoot, ".govm")
		} else if !filepath.IsAbs(gv.RuntimeHome) {
			gv.RuntimeHome = filepath.Join(projectRoot, gv.RuntimeHome)
		}
		gv.RuntimeHome = filepath.Clean(gv.RuntimeHome)
		if strings.TrimSpace(gv.OfflineImage) == "" && strings.TrimSpace(gv.Image) == "" {
			gv.OfflineImage = "py312-alpine"
		}
		if len(gv.Mounts) > 0 {
			mounts := make([]MountSpec, len(gv.Mounts))
			copy(mounts, gv.Mounts)
			gv.Mounts = mounts
		}
		out.Govm = &gv
	}
	return out
}

func validateSandboxOptions(projectRoot string, in SandboxOptions) error {
	cfg := normalizeSandboxOptions(projectRoot, in)
	validateMounts := func(name string, mounts []MountSpec) error {
		seen := make([]string, 0, len(mounts))
		for _, mount := range mounts {
			guest := strings.TrimSpace(mount.GuestPath)
			if guest == "" {
				return fmt.Errorf("api: %s mount guest path is required", name)
			}
			if !filepath.IsAbs(guest) {
				return fmt.Errorf("api: %s mount guest path must be absolute: %s", name, guest)
			}
			guest = filepath.Clean(guest)
			for _, existing := range seen {
				if guest == existing || strings.HasPrefix(guest+"/", existing+"/") || strings.HasPrefix(existing+"/", guest+"/") {
					return fmt.Errorf("api: overlapping %s guest mount paths: %s and %s", name, guest, existing)
				}
			}
			seen = append(seen, guest)
		}
		return nil
	}
	if cfg.GVisor != nil {
		if err := validateMounts("gvisor", cfg.GVisor.Mounts); err != nil {
			return err
		}
	}
	if cfg.Govm != nil {
		if err := validateMounts("govm", cfg.Govm.Mounts); err != nil {
			return err
		}
	}
	return nil
}

// defaultNetworkAllowList returns the default local-network allow list.
func defaultNetworkAllowList() []string {
	return []string{
		"localhost",
		"127.0.0.1",
		"::1",       // IPv6 localhost
		"0.0.0.0",   // 本机所有接口
		"*.local",   // 本地域名
		"192.168.*", // 私有网段
		"10.*",      // 私有网段
		"172.16.*",  // 私有网段
	}
}
