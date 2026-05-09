// Package dataset provides on-disk loaders for Terminal-Bench 2 task
// datasets. Two layouts are recognised:
//
// 1. Pre-converted "tarball" layout (originally produced by a fetch script):
//
//	<root>/manifest.json                # optional aggregate index
//	<root>/tasks/<name>/task.json       # per-task metadata
//	<root>/tasks/<name>/environment.tar # optional initial filesystem
//	<root>/tasks/<name>/tests.tar       # required test bundle
//	<root>/tasks/<name>/test.sh         # optional verifier (else inside tests.tar)
//
// 2. Upstream "harbor-framework" / "NousResearch" Terminal-Bench-2 layout —
// the format published by the public TB2 dataset repos:
//
//	<root>/<name>/task.toml             # per-task metadata in TOML
//	<root>/<name>/instruction.md        # agent prompt
//	<root>/<name>/environment/          # docker build context (image is prebuilt)
//	<root>/<name>/tests/                # test files (tar'd on-the-fly into /tests)
//	<root>/<name>/solution/             # oracle solution (ignored)
//
// Layout is auto-detected by Load: if <root>/tasks/ exists it uses (1),
// otherwise it falls back to (2). Both produce the same Task struct so
// downstream code is layout-agnostic.
//
// task.json schema (layout 1):
//
//	{
//	  "name": "qemu-startup",
//	  "category": "qemu",
//	  "instruction": "Boot the alpine VM and ...",
//	  "docker_image": "ubuntu:24.04",
//	  "environment_tar": "environment.tar",
//	  "tests_tar":       "tests.tar",
//	  "test_sh":         "test.sh",
//	  "agent_timeout_s":    1800,
//	  "terminal_timeout_s": 300,
//	  "skip_reason":     ""
//	}
package dataset

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Task is the runtime view of a TB2 evaluation task. EnvironmentTar / TestsTar
// are loaded lazily by the runner via OpenEnvironment / OpenTests so multi-GB
// datasets don't have to fit in memory all at once.
type Task struct {
	Name            string
	Category        string
	Instruction     string
	DockerImage     string
	EnvironmentTar  string // path; "" means no env tar (layout 1)
	TestsTar        string // path; required for layout 1
	TestSh          string // shell command to run, default "cd /tests && bash test.sh"
	AgentTimeout    time.Duration
	TerminalTimeout time.Duration
	SkipReason      string

	// envTarBuilder / testsTarBuilder are non-nil for tasks loaded from the
	// upstream layout, where tarballs are synthesised from on-disk
	// directories on demand instead of read from a pre-built file.
	envTarBuilder   func() (io.ReadCloser, error)
	testsTarBuilder func() (io.ReadCloser, error)
}

// taskJSON is the serialized form on disk (layout 1). Only this struct knows
// about the JSON shape — Task itself is the runner-facing type.
type taskJSON struct {
	Name             string `json:"name"`
	Category         string `json:"category"`
	Instruction      string `json:"instruction"`
	DockerImage      string `json:"docker_image"`
	EnvironmentTar   string `json:"environment_tar,omitempty"`
	TestsTar         string `json:"tests_tar"`
	TestSh           string `json:"test_sh,omitempty"`
	AgentTimeoutS    int    `json:"agent_timeout_s,omitempty"`
	TerminalTimeoutS int    `json:"terminal_timeout_s,omitempty"`
	SkipReason       string `json:"skip_reason,omitempty"`
}

// taskTOML is the upstream TB2 task.toml shape. Only the fields the runner
// actually consumes are decoded; unknown keys are ignored by BurntSushi/toml.
type taskTOML struct {
	Version  string `toml:"version"`
	Metadata struct {
		Category   string `toml:"category"`
		Difficulty string `toml:"difficulty"`
		SkipReason string `toml:"skip_reason"`
	} `toml:"metadata"`
	Verifier struct {
		TimeoutSec float64 `toml:"timeout_sec"`
	} `toml:"verifier"`
	Agent struct {
		TimeoutSec float64 `toml:"timeout_sec"`
	} `toml:"agent"`
	Environment struct {
		DockerImage string `toml:"docker_image"`
	} `toml:"environment"`
}

// Load reads every task under <root>. Layout is auto-detected: if
// <root>/tasks/<name>/task.json exists the legacy tarball layout is used;
// otherwise <root>/<name>/task.toml is treated as the upstream layout.
// Tasks are returned sorted by name.
func Load(root string) ([]Task, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("dataset: root is required")
	}
	tasksRoot := filepath.Join(root, "tasks")
	if info, err := os.Stat(tasksRoot); err == nil && info.IsDir() {
		return loadJSONLayout(tasksRoot)
	}
	return loadTOMLLayout(root)
}

// LoadFiltered loads then keeps tasks matching any include glob (or all if
// include is empty) AND not matching any exclude glob. Patterns use
// filepath.Match against task.Name. Pass exact names to filter precisely.
func LoadFiltered(root string, include, exclude []string) ([]Task, error) {
	all, err := Load(root)
	if err != nil {
		return nil, err
	}
	return Filter(all, include, exclude)
}

// Filter applies include/exclude globs without touching disk.
func Filter(tasks []Task, include, exclude []string) ([]Task, error) {
	out := tasks[:0:0]
	for _, t := range tasks {
		if !matchesAny(t.Name, include, true) {
			continue
		}
		if matchesAny(t.Name, exclude, false) {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// matchesAny returns true when name matches any pattern. emptyDefault is
// returned when patterns is empty.
func matchesAny(name string, patterns []string, emptyDefault bool) bool {
	if len(patterns) == 0 {
		return emptyDefault
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
		if p == name {
			return true
		}
	}
	return false
}

// loadJSONLayout walks <tasksRoot>/<name>/task.json directories.
func loadJSONLayout(tasksRoot string) ([]Task, error) {
	entries, err := os.ReadDir(tasksRoot)
	if err != nil {
		return nil, fmt.Errorf("dataset: read tasks dir: %w", err)
	}
	var tasks []Task
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		taskDir := filepath.Join(tasksRoot, e.Name())
		task, err := readJSONTask(taskDir)
		if err != nil {
			return nil, fmt.Errorf("dataset: load %s: %w", e.Name(), err)
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Name < tasks[j].Name })
	return tasks, nil
}

func readJSONTask(taskDir string) (Task, error) {
	metaPath := filepath.Join(taskDir, "task.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return Task{}, fmt.Errorf("read task.json: %w", err)
	}
	var raw taskJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return Task{}, fmt.Errorf("parse task.json: %w", err)
	}
	if strings.TrimSpace(raw.Name) == "" {
		raw.Name = filepath.Base(taskDir)
	}
	if strings.TrimSpace(raw.DockerImage) == "" {
		return Task{}, errors.New("task.json: docker_image is required")
	}
	if strings.TrimSpace(raw.TestsTar) == "" {
		return Task{}, errors.New("task.json: tests_tar is required")
	}
	t := Task{
		Name:            raw.Name,
		Category:        raw.Category,
		Instruction:     raw.Instruction,
		DockerImage:     raw.DockerImage,
		EnvironmentTar:  resolveAttachment(taskDir, raw.EnvironmentTar),
		TestsTar:        resolveAttachment(taskDir, raw.TestsTar),
		TestSh:          strings.TrimSpace(raw.TestSh),
		AgentTimeout:    time.Duration(raw.AgentTimeoutS) * time.Second,
		TerminalTimeout: time.Duration(raw.TerminalTimeoutS) * time.Second,
		SkipReason:      raw.SkipReason,
	}
	if t.TestSh == "" {
		t.TestSh = "bash test.sh"
	}
	return t, nil
}

// loadTOMLLayout walks <root>/<name>/task.toml directories. Non-task entries
// (README, LICENSE, .git, .gitignore, ...) are silently skipped.
func loadTOMLLayout(root string) ([]Task, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("dataset: read root: %w", err)
	}
	var tasks []Task
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		taskDir := filepath.Join(root, e.Name())
		tomlPath := filepath.Join(taskDir, "task.toml")
		if _, err := os.Stat(tomlPath); err != nil {
			// Directory without a task.toml is not a task — skip.
			continue
		}
		task, err := readTOMLTask(taskDir, e.Name())
		if err != nil {
			return nil, fmt.Errorf("dataset: load %s: %w", e.Name(), err)
		}
		tasks = append(tasks, task)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("dataset: no tasks found under %s (expected <root>/<name>/task.toml or <root>/tasks/)", root)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Name < tasks[j].Name })
	return tasks, nil
}

func readTOMLTask(taskDir, name string) (Task, error) {
	var meta taskTOML
	tomlPath := filepath.Join(taskDir, "task.toml")
	if _, err := toml.DecodeFile(tomlPath, &meta); err != nil {
		return Task{}, fmt.Errorf("parse task.toml: %w", err)
	}
	if strings.TrimSpace(meta.Environment.DockerImage) == "" {
		return Task{}, errors.New("task.toml: [environment].docker_image is required")
	}

	instruction, err := readInstruction(taskDir)
	if err != nil {
		return Task{}, err
	}

	testsDir := filepath.Join(taskDir, "tests")
	if info, err := os.Stat(testsDir); err != nil || !info.IsDir() {
		return Task{}, errors.New("task: tests/ directory is required")
	}

	t := Task{
		Name:            name,
		Category:        meta.Metadata.Category,
		Instruction:     instruction,
		DockerImage:     meta.Environment.DockerImage,
		TestsTar:        testsDir, // marker only — the real bytes come from testsTarBuilder
		TestSh:          "",       // empty → runner uses defaultVerifierCmd ("bash /tests/test.sh") and inherits container WORKDIR
		AgentTimeout:    secondsToDuration(meta.Agent.TimeoutSec),
		TerminalTimeout: secondsToDuration(meta.Verifier.TimeoutSec),
		SkipReason:      meta.Metadata.SkipReason,
		testsTarBuilder: func() (io.ReadCloser, error) { return tarDirContents(testsDir) },
	}
	return t, nil
}

// readInstruction reads instruction.md and strips the well-known
// Terminal-Bench canary string lines so they don't leak into the model
// prompt verbatim. Missing instruction.md is an error.
func readInstruction(taskDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(taskDir, "instruction.md"))
	if err != nil {
		return "", fmt.Errorf("read instruction.md: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	out := lines[:0]
	for _, line := range lines {
		if strings.Contains(line, "terminal-bench-canary") {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n")), nil
}

func secondsToDuration(sec float64) time.Duration {
	if sec <= 0 {
		return 0
	}
	return time.Duration(sec * float64(time.Second))
}

// tarDirContents builds a tar archive whose entries are paths relative to
// dir (not including dir itself). The runner extracts the result at /tests
// in the container, so foo/bar.sh inside dir lands at /tests/foo/bar.sh.
func tarDirContents(dir string) (io.ReadCloser, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		// tar uses forward slashes regardless of host OS.
		rel = filepath.ToSlash(rel)

		info, err := d.Info()
		if err != nil {
			return err
		}
		var link string
		if info.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return fmt.Errorf("readlink %s: %w", path, err)
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return fmt.Errorf("tar header %s: %w", path, err)
		}
		hdr.Name = rel
		if d.IsDir() {
			hdr.Name = rel + "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		f.Close()
		return copyErr
	})
	if walkErr != nil {
		return nil, fmt.Errorf("tar build %s: %w", dir, walkErr)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar close: %w", err)
	}
	return io.NopCloser(&buf), nil
}

// resolveAttachment turns a (possibly relative) attachment path into an
// absolute path under taskDir. Empty in → empty out.
func resolveAttachment(taskDir, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	return filepath.Clean(filepath.Join(taskDir, name))
}

// OpenEnvironment opens the environment archive. Returns (nil, nil) when
// the task has no environment payload (layout 2 always returns nil because
// the environment is baked into the prebuilt docker image).
func (t Task) OpenEnvironment() (io.ReadCloser, error) {
	if t.envTarBuilder != nil {
		return t.envTarBuilder()
	}
	if t.EnvironmentTar == "" {
		return nil, nil
	}
	f, err := os.Open(t.EnvironmentTar)
	if err != nil {
		return nil, fmt.Errorf("dataset: open environment_tar: %w", err)
	}
	return f, nil
}

// OpenTests opens the tests archive. For layout 1 this is a real file on
// disk; for layout 2 it is synthesised from <task>/tests/.
func (t Task) OpenTests() (io.ReadCloser, error) {
	if t.testsTarBuilder != nil {
		return t.testsTarBuilder()
	}
	if t.TestsTar == "" {
		return nil, errors.New("dataset: tests_tar is required")
	}
	f, err := os.Open(t.TestsTar)
	if err != nil {
		return nil, fmt.Errorf("dataset: open tests_tar: %w", err)
	}
	return f, nil
}
