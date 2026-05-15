package dockerenv

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sort"
	"strings"
	"time"

	sandboxenv "github.com/saker-ai/saker/pkg/sandbox/env"
)

// environment_lifecycle.go owns container lifecycle: image pull policy,
// container creation/teardown, image inspection, and PrepareSession /
// CloseSession. File and command I/O live in environment_io.go and helpers
// (path/quoting/argv) live in environment_helpers.go.

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
	network := strings.TrimSpace(e.cfg.NetworkMode)
	if network == "" {
		network = "none" // default to no network access (matches govm sandbox behavior)
	}
	argv = append(argv, "--network", network)
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
