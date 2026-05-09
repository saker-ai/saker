// Package dockerenv implements sandboxenv.ExecutionEnvironment by shelling
// out to the `docker` CLI. Each session gets its own dedicated container,
// kept alive by `sleep <ttl>`. RunCommand uses `docker exec`, file IO uses
// `docker cp` (via stdin/stdout streams), and CloseSession does `docker rm -f`.
//
// Designed for the Terminal-Bench 2 evaluation runner: per-task isolation,
// no host filesystem leakage, no Docker SDK dependency.
package dockerenv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
		"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
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

// PrepareSession ensures a container is running for the given session ID.
// Returns the cached PreparedSession if one already exists.
func (e *Environment) PrepareSession(ctx context.Context, session sandboxenv.SessionContext) (*sandboxenv.PreparedSession, error) {
	if strings.TrimSpace(e.cfg.Image) == "" {
		return nil, errors.New("dockerenv: image is required")
	}
	if strings.TrimSpace(session.SessionID) == "" {
		return nil, errors.New("dockerenv: session id is required")
	}

	e.mu.Lock()
	if existing, ok := e.sessions[session.SessionID]; ok {
		prepared := existing.prepared
		e.mu.Unlock()
		return prepared, nil
	}
	e.mu.Unlock()

	if err := e.ensureImage(ctx, e.cfg.Image); err != nil {
		return nil, err
	}

	containerName := e.buildContainerName(session.SessionID)
	workdir := e.resolveWorkdir(ctx, e.cfg.Image)
	// Capture the immutable image digest so reports can reproduce the exact
	// bits that ran. Best-effort: a stale local image with no RepoDigests
	// (e.g. built locally, never pushed) returns empty and Meta omits the key.
	imageDigest, _ := e.inspectImageDigest(ctx, e.cfg.Image)
	containerID, err := e.startContainer(ctx, containerName, workdir, e.cfg.Image)
	if err != nil {
		return nil, err
	}
	// Make sure the workdir exists inside the container — the image may not
	// ship one, and `docker run -w` creates the dir but only if --workdir is
	// honoured by the runtime; some bare images ignore it.
	if _, mkErr := e.execInContainer(ctx, containerID, "/", e.cfg.DefaultTimeout, nil, "mkdir", "-p", workdir); mkErr != nil {
		_ = e.removeContainer(context.Background(), containerID)
		return nil, fmt.Errorf("dockerenv: ensure workdir: %w", mkErr)
	}

	meta := map[string]any{
		"container_id":   containerID,
		"container_name": containerName,
		"image":          e.cfg.Image,
	}
	if imageDigest != "" {
		meta["image_digest"] = imageDigest
	}
	prepared := &sandboxenv.PreparedSession{
		SessionID:   session.SessionID,
		GuestCwd:    workdir,
		SandboxType: SandboxType,
		Meta:        meta,
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if existing, ok := e.sessions[session.SessionID]; ok {
		// Lost the race; tear down our throw-away container.
		go func(id string) { _ = e.removeContainer(context.Background(), id) }(containerID)
		return existing.prepared, nil
	}
	e.sessions[session.SessionID] = &sessionState{
		prepared:      prepared,
		containerID:   containerID,
		containerName: containerName,
		image:         e.cfg.Image,
		workdir:       workdir,
	}
	return prepared, nil
}

// RunCommand executes /bin/sh -lc <cmd> inside the session's container.
func (e *Environment) RunCommand(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.CommandRequest) (*sandboxenv.CommandResult, error) {
	state, err := e.session(ps)
	if err != nil {
		return nil, err
	}
	workdir := strings.TrimSpace(req.Workdir)
	if workdir == "" {
		workdir = state.workdir
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = e.cfg.DefaultTimeout
	}
	res, err := e.execInContainer(ctx, state.containerID, workdir, timeout, req.Env, "/bin/sh", "-lc", req.Command)
	if err != nil {
		return nil, err
	}
	return &sandboxenv.CommandResult{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
		Duration: res.Duration,
	}, nil
}

// RunCommandStream is like RunCommand but pipes stdout/stderr deltas.
func (e *Environment) RunCommandStream(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.CommandRequest, cb sandboxenv.CommandStreamCallbacks) (*sandboxenv.CommandResult, error) {
	state, err := e.session(ps)
	if err != nil {
		return nil, err
	}
	workdir := strings.TrimSpace(req.Workdir)
	if workdir == "" {
		workdir = state.workdir
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = e.cfg.DefaultTimeout
	}
	argv := buildExecArgv(state.containerID, workdir, req.Env, "/bin/sh", "-lc", req.Command)
	res, err := e.cmd.Stream(ctx, argv, nil, timeout, cb.OnStdout, cb.OnStderr)
	if err != nil {
		return nil, err
	}
	return &sandboxenv.CommandResult{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
		Duration: res.Duration,
	}, nil
}

// ReadFile cats the file via `docker exec`. Stdout is the file body; we
// detect binary content by scanning for NUL like other env impls.
func (e *Environment) ReadFile(ctx context.Context, ps *sandboxenv.PreparedSession, path string) ([]byte, error) {
	state, err := e.session(ps)
	if err != nil {
		return nil, err
	}
	guestPath := normalizeGuestPath(path, state.workdir)
	res, err := e.execInContainer(ctx, state.containerID, "/", e.cfg.DefaultTimeout, nil, "cat", "--", guestPath)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("dockerenv: read file %s: exit %d: %s", guestPath, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	data := []byte(res.Stdout)
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("binary file %s is not supported", path)
	}
	return data, nil
}

// WriteFile pipes `data` into `tee <guestPath>` running inside the container.
// Creates parent dirs first.
func (e *Environment) WriteFile(ctx context.Context, ps *sandboxenv.PreparedSession, path string, data []byte) error {
	state, err := e.session(ps)
	if err != nil {
		return err
	}
	guestPath := normalizeGuestPath(path, state.workdir)
	parent := filepath.Dir(guestPath)
	if parent != "" && parent != "." {
		if res, mkErr := e.execInContainer(ctx, state.containerID, "/", e.cfg.DefaultTimeout, nil, "mkdir", "-p", parent); mkErr != nil {
			return fmt.Errorf("dockerenv: ensure parent dir: %w", mkErr)
		} else if res.ExitCode != 0 {
			return fmt.Errorf("dockerenv: ensure parent dir %s: exit %d: %s", parent, res.ExitCode, strings.TrimSpace(res.Stderr))
		}
	}
	argv := buildExecArgv(state.containerID, "/", nil, "sh", "-c", "cat > "+shellQuote(guestPath))
	// Prefix with `-i` for stdin attach.
	argv = injectInteractive(argv)
	res, err := e.cmd.Run(ctx, argv, bytes.NewReader(data), e.cfg.DefaultTimeout)
	if err != nil {
		return fmt.Errorf("dockerenv: write file: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("dockerenv: write file %s: exit %d: %s", guestPath, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// EditFile reads, replaces, writes — same pattern as other env impls.
func (e *Environment) EditFile(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.EditRequest) error {
	data, err := e.ReadFile(ctx, ps, req.Path)
	if err != nil {
		return err
	}
	content := string(data)
	if req.ReplaceAll {
		content = strings.ReplaceAll(content, req.OldText, req.NewText)
	} else {
		content = strings.Replace(content, req.OldText, req.NewText, 1)
	}
	return e.WriteFile(ctx, ps, req.Path, []byte(content))
}

// Glob delegates to `find -path` inside the container; returns guest paths.
func (e *Environment) Glob(ctx context.Context, ps *sandboxenv.PreparedSession, pattern string) ([]string, error) {
	state, err := e.session(ps)
	if err != nil {
		return nil, err
	}
	guestPattern := pattern
	if !strings.HasPrefix(guestPattern, "/") {
		guestPattern = filepath.Join(state.workdir, guestPattern)
	}
	guestPattern = filepath.Clean(guestPattern)
	root := guestRootForPattern(guestPattern)
	cmd := fmt.Sprintf("find %s -path %s -print 2>/dev/null", shellQuote(root), shellQuote(guestPattern))
	res, err := e.execInContainer(ctx, state.containerID, "/", e.cfg.DefaultTimeout, nil, "/bin/sh", "-c", cmd)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 && strings.TrimSpace(res.Stderr) != "" {
		return nil, fmt.Errorf("dockerenv: glob: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	var matches []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		matches = append(matches, line)
	}
	return matches, nil
}

// Grep uses `grep -nrE` inside the container and parses path:line:preview.
func (e *Environment) Grep(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.GrepRequest) ([]sandboxenv.GrepMatch, error) {
	state, err := e.session(ps)
	if err != nil {
		return nil, err
	}
	root := strings.TrimSpace(req.Path)
	if root == "" {
		root = state.workdir
	}
	flags := "-nrI"
	if !req.CaseSensitive {
		flags += "i"
	}
	if !req.Literal {
		flags += "E"
	} else {
		flags += "F"
	}
	cmd := fmt.Sprintf("grep %s -- %s %s 2>/dev/null || true",
		flags,
		shellQuote(req.Pattern),
		shellQuote(root))
	res, err := e.execInContainer(ctx, state.containerID, "/", e.cfg.DefaultTimeout, nil, "/bin/sh", "-c", cmd)
	if err != nil {
		return nil, err
	}
	var matches []sandboxenv.GrepMatch
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		// path:line:preview
		first := strings.IndexByte(line, ':')
		if first < 0 {
			continue
		}
		rest := line[first+1:]
		second := strings.IndexByte(rest, ':')
		if second < 0 {
			continue
		}
		path := line[:first]
		lineNum, convErr := strconv.Atoi(rest[:second])
		if convErr != nil {
			continue
		}
		preview := rest[second+1:]
		matches = append(matches, sandboxenv.GrepMatch{
			Path:    path,
			Line:    lineNum,
			Column:  1,
			Preview: preview,
		})
	}
	return matches, nil
}

// CloseSession removes the container and forgets the session.
func (e *Environment) CloseSession(ctx context.Context, ps *sandboxenv.PreparedSession) error {
	if ps == nil {
		return nil
	}
	e.mu.Lock()
	state := e.sessions[ps.SessionID]
	delete(e.sessions, ps.SessionID)
	e.mu.Unlock()
	if state == nil {
		return nil
	}
	return e.removeContainer(ctx, state.containerID)
}

// CopyArchiveTo streams a tar archive into `<destDir>` of the container.
// Used by the TB2 runner to upload environment.tar / tests.tar without having
// to land them on the host first. destDir must already exist.
func (e *Environment) CopyArchiveTo(ctx context.Context, ps *sandboxenv.PreparedSession, destDir string, archive io.Reader) error {
	state, err := e.session(ps)
	if err != nil {
		return err
	}
	if strings.TrimSpace(destDir) == "" {
		return errors.New("dockerenv: destDir is required")
	}
	// Ensure destDir exists; tar -xf - relies on it.
	if res, mkErr := e.execInContainer(ctx, state.containerID, "/", e.cfg.DefaultTimeout, nil, "mkdir", "-p", destDir); mkErr != nil {
		return fmt.Errorf("dockerenv: ensure dest dir: %w", mkErr)
	} else if res.ExitCode != 0 {
		return fmt.Errorf("dockerenv: ensure dest dir %s: exit %d: %s", destDir, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	argv := buildExecArgv(state.containerID, "/", nil, "tar", "-xf", "-", "-C", destDir)
	argv = injectInteractive(argv)
	res, err := e.cmd.Run(ctx, argv, archive, e.cfg.DefaultTimeout)
	if err != nil {
		return fmt.Errorf("dockerenv: extract archive: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("dockerenv: extract archive: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (e *Environment) session(ps *sandboxenv.PreparedSession) (*sessionState, error) {
	if ps == nil {
		return nil, errors.New("dockerenv: missing prepared session")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.sessions[ps.SessionID]
	if state == nil {
		return nil, errors.New("dockerenv: session is not initialised")
	}
	return state, nil
}

func (e *Environment) ensureImage(ctx context.Context, image string) error {
	switch e.cfg.PullPolicy {
	case PullNever:
		return nil
	case PullAlways:
		return e.pullImage(ctx, image)
	case PullIfMissing, "":
		if _, ok := e.pulledImg.Load(image); ok {
			return nil
		}
		// `docker image inspect` is the cheapest local check.
		res, err := e.cmd.Run(ctx, []string{"image", "inspect", image}, nil, e.cfg.DefaultTimeout)
		if err == nil && res.ExitCode == 0 {
			e.pulledImg.Store(image, struct{}{})
			return nil
		}
		return e.pullImage(ctx, image)
	default:
		return fmt.Errorf("dockerenv: unknown pull policy %q", e.cfg.PullPolicy)
	}
}

func (e *Environment) pullImage(ctx context.Context, image string) error {
	res, err := e.cmd.Run(ctx, []string{"pull", image}, nil, 30*time.Minute)
	if err != nil {
		return fmt.Errorf("dockerenv: pull %s: %w", image, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("dockerenv: pull %s: exit %d: %s", image, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	e.pulledImg.Store(image, struct{}{})
	return nil
}

// resolveWorkdir picks the container workdir using the precedence laid out on
// Config.DefaultWorkdir. Caller-supplied non-empty values win; otherwise the
// image's WORKDIR is queried; the final fallback is fallbackWorkdir.
func (e *Environment) resolveWorkdir(ctx context.Context, image string) string {
	if explicit := strings.TrimSpace(e.cfg.DefaultWorkdir); explicit != "" {
		return explicit
	}
	if detected, err := e.inspectImageWorkdir(ctx, image); err == nil {
		if trimmed := strings.TrimSpace(detected); trimmed != "" {
			return trimmed
		}
	}
	return fallbackWorkdir
}

// inspectImageDigest returns the first RepoDigest of the image
// (`name@sha256:...`). Empty when the image was built locally and never
// pushed/pulled — there is no digest to capture in that case. Errors are
// returned but callers typically swallow them: the digest is informational.
func (e *Environment) inspectImageDigest(ctx context.Context, image string) (string, error) {
	res, err := e.cmd.Run(ctx,
		[]string{"image", "inspect", image, "--format", "{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}"},
		nil, e.cfg.DefaultTimeout)
	if err != nil {
		return "", fmt.Errorf("dockerenv: inspect digest %s: %w", image, err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("dockerenv: inspect digest %s: exit %d: %s",
			image, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return strings.TrimSpace(res.Stdout), nil
}

// inspectImageWorkdir returns the WORKDIR baked into the image (the
// `Config.WorkingDir` field of `docker image inspect`). An empty string means
// the image declared no WORKDIR — caller decides the fallback.
func (e *Environment) inspectImageWorkdir(ctx context.Context, image string) (string, error) {
	res, err := e.cmd.Run(ctx,
		[]string{"image", "inspect", image, "--format", "{{.Config.WorkingDir}}"},
		nil, e.cfg.DefaultTimeout)
	if err != nil {
		return "", fmt.Errorf("dockerenv: inspect %s: %w", image, err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("dockerenv: inspect %s: exit %d: %s", image, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	out := strings.TrimSpace(res.Stdout)
	// Some Docker versions print "<no value>" for an unset WorkingDir.
	if out == "<no value>" {
		return "", nil
	}
	return out, nil
}

func (e *Environment) startContainer(ctx context.Context, name, workdir, image string) (string, error) {
	argv := []string{"run", "-d", "--rm", "--name", name, "-w", workdir}
	if strings.TrimSpace(e.cfg.NetworkMode) != "" {
		argv = append(argv, "--network", e.cfg.NetworkMode)
	}
	// Sort env keys so identical ExtraEnv maps produce identical argv —
	// helps test golden output and keeps `docker inspect` diffs stable.
	if len(e.cfg.ExtraEnv) > 0 {
		keys := make([]string, 0, len(e.cfg.ExtraEnv))
		for k := range e.cfg.ExtraEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			argv = append(argv, "-e", fmt.Sprintf("%s=%s", k, e.cfg.ExtraEnv[k]))
		}
	}
	argv = append(argv, e.cfg.ExtraRunArgs...)
	argv = append(argv, image, "sleep", strconv.FormatInt(int64(e.cfg.ContainerTTL/time.Second), 10))
	res, err := e.cmd.Run(ctx, argv, nil, e.cfg.DefaultTimeout)
	if err != nil {
		return "", fmt.Errorf("dockerenv: docker run: %w", err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("dockerenv: docker run: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	id := strings.TrimSpace(res.Stdout)
	if id == "" {
		return "", errors.New("dockerenv: docker run returned empty container id")
	}
	return id, nil
}

func (e *Environment) execInContainer(ctx context.Context, containerID, workdir string, timeout time.Duration, env map[string]string, argv ...string) (cmdResult, error) {
	full := buildExecArgv(containerID, workdir, env, argv...)
	return e.cmd.Run(ctx, full, nil, timeout)
}

func (e *Environment) removeContainer(ctx context.Context, containerID string) error {
	if containerID == "" {
		return nil
	}
	res, err := e.cmd.Run(ctx, []string{"rm", "-f", containerID}, nil, e.cfg.DefaultTimeout)
	if err != nil {
		return fmt.Errorf("dockerenv: docker rm: %w", err)
	}
	if res.ExitCode != 0 {
		// Don't fail loud — container may have been auto-removed already.
		return nil
	}
	return nil
}

func (e *Environment) buildContainerName(sessionID string) string {
	return fmt.Sprintf("%s-%s-%s", e.cfg.NamePrefix, sandboxenv.SanitizeName(sessionID), randomSuffix())
}

func buildExecArgv(containerID, workdir string, env map[string]string, argv ...string) []string {
	out := []string{"exec"}
	if strings.TrimSpace(workdir) != "" {
		out = append(out, "-w", workdir)
	}
	for k, v := range env {
		out = append(out, "--env", fmt.Sprintf("%s=%s", k, v))
	}
	out = append(out, containerID)
	out = append(out, argv...)
	return out
}

// injectInteractive inserts `-i` after the leading `exec` so docker keeps
// stdin open. We need this for tar -xf - and cat > <path>.
func injectInteractive(argv []string) []string {
	if len(argv) == 0 || argv[0] != "exec" {
		return argv
	}
	out := make([]string, 0, len(argv)+1)
	out = append(out, "exec", "-i")
	out = append(out, argv[1:]...)
	return out
}

func randomSuffix() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// normalizeGuestPath joins `path` against workdir if not absolute, and
// returns a Clean'd POSIX path.
func normalizeGuestPath(path, workdir string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return workdir
	}
	if !strings.HasPrefix(path, "/") {
		path = filepath.Join(workdir, path)
	}
	return filepath.Clean(path)
}

// guestRootForPattern returns the longest non-glob prefix of `pattern`,
// e.g. "/app/src/**/*.go" -> "/app/src". Used so `find` doesn't traverse
// the entire filesystem when only a small subtree matters.
func guestRootForPattern(pattern string) string {
	parts := strings.Split(pattern, "/")
	out := []string{}
	for _, p := range parts {
		if strings.ContainsAny(p, "*?[") {
			break
		}
		out = append(out, p)
	}
	root := strings.Join(out, "/")
	if root == "" {
		return "/"
	}
	return root
}

// shellQuote single-quotes `s` for /bin/sh inside the container.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
