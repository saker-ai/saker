package api

import (
	"path/filepath"
	"strings"

	"github.com/saker-ai/saker/pkg/config"
	"github.com/saker-ai/saker/pkg/sandbox"
	sandboxenv "github.com/saker-ai/saker/pkg/sandbox/env"
	"github.com/saker-ai/saker/pkg/sandbox/gvisorenv"
	"github.com/saker-ai/saker/pkg/sandbox/hostenv"
	"github.com/saker-ai/saker/pkg/sandbox/landlockenv"
)

// govmEnvFactory is set by sandbox_bridge_govm.go when built with the govm tag.
var govmEnvFactory func(projectRoot string, opts *GovmOptions) sandboxenv.ExecutionEnvironment

type noopFileSystemPolicy struct {
	root string
}

func (n *noopFileSystemPolicy) Allow(string) {
	_ = n
}

func (n *noopFileSystemPolicy) Validate(string) error { return nil }

func (n *noopFileSystemPolicy) Roots() []string {
	if n == nil || strings.TrimSpace(n.root) == "" {
		return nil
	}
	return []string{n.root}
}

// buildSandboxManager wires filesystem/network/resource policies using options
// and settings.json. Respects settings.Sandbox.Enabled to allow disabling
// sandbox validation entirely. Defaults to enabled for backwards compatibility.
func buildSandboxManager(opts Options, settings *config.Settings) (*sandbox.Manager, string) {
	// Check if sandbox is explicitly disabled in settings
	if settings != nil && settings.Sandbox != nil && settings.Sandbox.Enabled != nil && !*settings.Sandbox.Enabled {
		// Skip filesystem/network/resource validation, but keep tool permission rules
		// functional (permissions live under settings.Permissions, not settings.Sandbox).
		root := opts.Sandbox.Root
		if root == "" {
			root = opts.ProjectRoot
		}
		root = filepath.Clean(root)
		return sandbox.NewManager(&noopFileSystemPolicy{root: root}, nil, nil), root
	}

	root := opts.Sandbox.Root
	if root == "" {
		root = opts.ProjectRoot
	}
	root = filepath.Clean(root)
	resolvedRoot, err := filepath.EvalSymlinks(root)

	fs := sandbox.NewFileSystemAllowList(root)
	if err == nil && strings.TrimSpace(resolvedRoot) != "" {
		fs.Allow(resolvedRoot)
		root = resolvedRoot
	}

	for _, extra := range additionalSandboxPaths(settings) {
		fs.Allow(extra)
		if r, err := filepath.EvalSymlinks(extra); err == nil && strings.TrimSpace(r) != "" {
			fs.Allow(r)
		}
	}
	for _, extra := range opts.Sandbox.AllowedPaths {
		fs.Allow(extra)
		if r, err := filepath.EvalSymlinks(extra); err == nil && strings.TrimSpace(r) != "" {
			fs.Allow(r)
		}
	}

	netAllow := opts.Sandbox.NetworkAllow
	if len(netAllow) == 0 {
		netAllow = defaultNetworkAllowList()
	}

	nw := sandbox.NewDomainAllowList(netAllow...)
	return sandbox.NewManager(fs, nw, sandbox.NewResourceLimiter(opts.Sandbox.ResourceLimit)), root
}

func buildExecutionEnvironment(opts Options) sandboxenv.ExecutionEnvironment {
	if opts.Sandbox.Type == "govm" || (opts.Sandbox.Govm != nil && opts.Sandbox.Govm.Enabled) {
		if govmEnvFactory != nil {
			return govmEnvFactory(opts.ProjectRoot, opts.Sandbox.Govm)
		}
		// govm not compiled in — fall through to host.
	}
	if opts.Sandbox.Type == "gvisor" || (opts.Sandbox.GVisor != nil && opts.Sandbox.GVisor.Enabled) {
		return gvisorenv.New(opts.ProjectRoot, opts.Sandbox.GVisor)
	}
	if opts.Sandbox.Type == "landlock" || (opts.Sandbox.Landlock != nil && opts.Sandbox.Landlock.Enabled) {
		return landlockenv.New(opts.ProjectRoot, opts.Sandbox.Landlock)
	}
	return hostenv.New(opts.ProjectRoot)
}

func additionalSandboxPaths(settings *config.Settings) []string {
	if settings == nil || settings.Permissions == nil {
		return nil
	}
	var out []string
	seen := map[string]struct{}{}
	for _, path := range settings.Permissions.AdditionalDirectories {
		clean := strings.TrimSpace(path)
		if clean == "" {
			continue
		}
		abs, err := filepath.Abs(clean)
		if err == nil {
			clean = abs
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}
