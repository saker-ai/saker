//go:build integration_docker

// Package terminalbench: end-to-end smoke test that exercises the dockerenv
// backend rather than the in-memory stubEnv. Builds and runs only with the
// `integration_docker` tag so the regular `go test ./...` doesn't depend on
// a working Docker daemon.
//
//	go test -tags=integration_docker -count=1 ./pkg/eval/terminalbench/...
//
// What it covers:
//
//   - dockerenv.Environment.PrepareSession actually starts a container
//   - CopyArchiveTo unpacks a real tar
//   - bash test.sh runs inside the container and writes /logs/verifier/reward.txt
//   - Runner picks up the reward and emits a passing TaskResult
//
// Skipped automatically when:
//
//   - Docker CLI is not on PATH
//   - The smoke image (busybox:latest) cannot be pulled
package terminalbench

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/eval/dataset"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/sandbox/dockerenv"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
)

func TestIntegration_TerminalBench_BusyboxSmoke(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	root := t.TempDir()
	taskDir := filepath.Join(root, "tasks", "smoke")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	envTar := buildTarForTest(t, map[string]string{
		"hello.txt": "hi from env\n",
	})
	if err := os.WriteFile(filepath.Join(taskDir, "environment.tar"), envTar, 0o644); err != nil {
		t.Fatalf("write env.tar: %v", err)
	}

	testsTar := buildTarForTest(t, map[string]string{
		"test.sh": "#!/bin/sh\nset -e\nmkdir -p /logs/verifier\necho 1.0 > /logs/verifier/reward.txt\necho PASS\n",
	})
	if err := os.WriteFile(filepath.Join(taskDir, "tests.tar"), testsTar, 0o644); err != nil {
		t.Fatalf("write tests.tar: %v", err)
	}

	taskJSON := `{
		"docker_image": "busybox:latest",
		"environment_tar": "environment.tar",
		"tests_tar": "tests.tar",
		"agent_timeout_s": 30,
		"terminal_timeout_s": 30,
		"test_sh": "cd /tests && sh test.sh",
		"instruction": "Just succeed."
	}`
	if err := os.WriteFile(filepath.Join(taskDir, "task.json"), []byte(taskJSON), 0o644); err != nil {
		t.Fatalf("write task.json: %v", err)
	}

	cfg := Config{
		DatasetRoot:     root,
		OutputDir:       filepath.Join(root, "out"),
		Concurrency:     1,
		MaxIterations:   1,
		TaskTimeout:     2 * time.Minute,
		TerminalTimeout: 30 * time.Second,
		PullPolicy:      dockerenv.PullIfMissing,
		ContainerTTL:    3 * time.Minute,
		ProviderName:    "stub",
		ModelName:       "stub-model",
		ModelFactory:    func(_ context.Context) (model.Model, error) { return &stubModel{reply: "done"}, nil },
		EnvFactory: func(task dataset.Task) (sandboxenv.ExecutionEnvironment, error) {
			return dockerenv.New(dockerenv.Config{
				Image:          task.DockerImage,
				NamePrefix:     "saker-tb2-it",
				DefaultWorkdir: "/app",
				PullPolicy:     dockerenv.PullIfMissing,
				ContainerTTL:   3 * time.Minute,
				DefaultTimeout: 30 * time.Second,
			}), nil
		},
	}

	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	report, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if report.Aggregate.Passed != 1 {
		t.Fatalf("expected 1 pass, aggregate=%+v results=%+v", report.Aggregate, report.Results)
	}
	if report.Results[0].Score != 1.0 {
		t.Fatalf("Score=%v, want 1.0", report.Results[0].Score)
	}
}

// buildTarForTest emits an in-memory tar archive containing the given
// file-name → contents map. Files are recorded with mode 0755 so test.sh is
// executable when extracted into the container.
func buildTarForTest(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		hdr := &tar.Header{
			Name:    name,
			Mode:    0o755,
			Size:    int64(len(body)),
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}
