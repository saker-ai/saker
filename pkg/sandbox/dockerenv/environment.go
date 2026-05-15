// Package dockerenv implements sandboxenv.ExecutionEnvironment by shelling
// out to the `docker` CLI. Each session gets its own dedicated container,
// kept alive by `sleep <ttl>`. RunCommand uses `docker exec`, file IO uses
// `docker cp` (via stdin/stdout streams), and CloseSession does `docker rm -f`.
//
// Designed for the Terminal-Bench 2 evaluation runner: per-task isolation,
// no host filesystem leakage, no Docker SDK dependency.
//
// The implementation is split across sibling files to stay below the
// repository-wide 600 LOC cap:
//   - environment.go (this file): public types (Config, Environment, PullPolicy)
//     and constructor.
//   - environment_lifecycle.go: PrepareSession, CloseSession, image pull and
//     inspect helpers, container start/stop, session lookup.
//   - environment_io.go: RunCommand, file/grep/glob/archive operations.
//   - environment_helpers.go: pure helpers (path normalisation, shell quoting,
//     argv builders, suffix generator).
package dockerenv

import (
	"strings"
	"sync"
	"time"

	sandboxenv "github.com/saker-ai/saker/pkg/sandbox/env"
)

// SandboxType is the value placed in PreparedSession.SandboxType so that
// pkg/tool/builtin's isVirtualizedSandboxSession can route through us.
const SandboxType = "docker"

// PullPolicy controls how Environment ensures the image exists before
// starting the first container in a session.
type PullPolicy string

const (
	PullAlways    PullPolicy = "always"
	PullIfMissing PullPolicy = "if-missing"
	PullNever     PullPolicy = "never"
)

// Config bundles per-Environment settings. Image is required; everything else
// has a sensible default.
//
// DefaultWorkdir semantics:
//   - non-empty   → used verbatim, image inspect is bypassed (caller wants
//     this exact dir, e.g. legacy "/app" tarball-layout tasks)
//   - empty (zero) → PrepareSession runs `docker image inspect --format
//     '{{.Config.WorkingDir}}'`; the result becomes the
//     container workdir. Falls back to "/app" only when the
//     image declares no WORKDIR and inspection fails.
//
// The empty-default behaviour is what TB2 needs because each task's
// Dockerfile sets its own WORKDIR (/workspace, /build, /root, etc.) and
// hard-coding /app leaves the agent in an empty directory.
type Config struct {
	Image          string
	DockerBinary   string
	NamePrefix     string
	DefaultWorkdir string
	NetworkMode    string
	PullPolicy     PullPolicy
	ContainerTTL   time.Duration
	ExtraRunArgs   []string
	DefaultTimeout time.Duration

	// ExtraEnv is injected at `docker run` time via `-e KEY=VAL`. Values
	// persist for the container lifetime, so every subsequent `docker exec`
	// inherits them without per-call plumbing. Used by the TB2 runner to
	// push China-friendly mirror endpoints (UV_PYTHON_INSTALL_MIRROR,
	// PIP_INDEX_URL, …) into the container so verifier scripts and agent
	// tools both bypass GitHub/PyPI direct-fetch timeouts.
	ExtraEnv map[string]string
}

// fallbackWorkdir is the last-resort guest cwd used when DefaultWorkdir is
// empty and the image's own WORKDIR cannot be determined.
const fallbackWorkdir = "/app"

// Environment is a sandboxenv.ExecutionEnvironment that routes commands and
// file operations into a per-session Docker container.
type Environment struct {
	cfg       Config
	cmd       commander
	pulledImg sync.Map // image -> struct{} (PullPolicy=PullAlways always re-pulls)

	mu       sync.Mutex
	sessions map[string]*sessionState
}

type sessionState struct {
	prepared      *sandboxenv.PreparedSession
	containerID   string
	containerName string
	image         string
	workdir       string
}

// New constructs an Environment with the given config. The docker binary is
// not invoked until the first PrepareSession call. Note: DefaultWorkdir is
// intentionally NOT defaulted here — leaving it empty enables the
// image-WORKDIR auto-detect path inside PrepareSession.
func New(cfg Config) *Environment {
	if strings.TrimSpace(cfg.DockerBinary) == "" {
		cfg.DockerBinary = "docker"
	}
	if strings.TrimSpace(cfg.NamePrefix) == "" {
		cfg.NamePrefix = "saker-tb2"
	}
	if cfg.PullPolicy == "" {
		cfg.PullPolicy = PullIfMissing
	}
	if cfg.ContainerTTL <= 0 {
		cfg.ContainerTTL = 2 * time.Hour
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 5 * time.Minute
	}
	return &Environment{
		cfg:      cfg,
		cmd:      &execCommander{binary: cfg.DockerBinary},
		sessions: make(map[string]*sessionState),
	}
}

// withCommander overrides the docker command runner. Test-only.
func (e *Environment) withCommander(c commander) {
	e.cmd = c
}
