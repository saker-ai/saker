package dataset

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTaskDir scaffolds a single task directory with a task.json + tar files.
func writeTaskDir(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, "tasks", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile task.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests.tar"), []byte("FAKE"), 0o644); err != nil {
		t.Fatalf("WriteFile tests.tar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "environment.tar"), []byte("ENV"), 0o644); err != nil {
		t.Fatalf("WriteFile environment.tar: %v", err)
	}
	return dir
}

func TestLoad_ReadsAllTasksSorted(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTaskDir(t, root, "zeta", `{"docker_image":"ubuntu","tests_tar":"tests.tar","environment_tar":"environment.tar"}`)
	writeTaskDir(t, root, "alpha", `{"name":"alpha","category":"basic","instruction":"do","docker_image":"img","tests_tar":"tests.tar","agent_timeout_s":42}`)

	tasks, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(tasks))
	}
	if tasks[0].Name != "alpha" || tasks[1].Name != "zeta" {
		t.Fatalf("not sorted: %v", []string{tasks[0].Name, tasks[1].Name})
	}
	if tasks[0].AgentTimeout != 42*time.Second {
		t.Fatalf("AgentTimeout = %v, want 42s", tasks[0].AgentTimeout)
	}
	if tasks[1].EnvironmentTar == "" {
		t.Fatal("EnvironmentTar should be resolved")
	}
}

func TestLoad_RequiresDockerImageAndTestsTar(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTaskDir(t, root, "bad", `{"tests_tar":"tests.tar"}`)
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "docker_image is required") {
		t.Fatalf("want docker_image error, got %v", err)
	}

	root2 := t.TempDir()
	dir := filepath.Join(root2, "tasks", "bad2")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "task.json"), []byte(`{"docker_image":"img"}`), 0o644)
	_, err = Load(root2)
	if err == nil || !strings.Contains(err.Error(), "tests_tar is required") {
		t.Fatalf("want tests_tar error, got %v", err)
	}
}

func TestLoad_NameDefaultsToDirectoryName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTaskDir(t, root, "implicit-name", `{"docker_image":"img","tests_tar":"tests.tar"}`)
	tasks, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if tasks[0].Name != "implicit-name" {
		t.Fatalf("Name = %q, want implicit-name", tasks[0].Name)
	}
}

func TestLoad_DefaultsTestSh(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTaskDir(t, root, "x", `{"docker_image":"i","tests_tar":"tests.tar"}`)
	tasks, _ := Load(root)
	if tasks[0].TestSh != "bash test.sh" {
		t.Fatalf("TestSh = %q, want default 'bash test.sh'", tasks[0].TestSh)
	}
}

func TestFilter_IncludeAndExclude(t *testing.T) {
	t.Parallel()
	in := []Task{
		{Name: "qemu-startup"},
		{Name: "qemu-alpine"},
		{Name: "git-clone"},
		{Name: "crack-7z-hash"},
	}
	got, err := Filter(in, []string{"qemu-*"}, []string{"qemu-alpine"})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(got) != 1 || got[0].Name != "qemu-startup" {
		t.Fatalf("Filter result = %v", names(got))
	}

	got, _ = Filter(in, nil, []string{"crack-*"})
	if len(got) != 3 {
		t.Fatalf("exclude only: %v", names(got))
	}

	got, _ = Filter(in, []string{"git-clone"}, nil)
	if len(got) != 1 || got[0].Name != "git-clone" {
		t.Fatalf("exact include: %v", names(got))
	}
}

func TestOpenAttachments(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTaskDir(t, root, "x", `{"docker_image":"i","tests_tar":"tests.tar","environment_tar":"environment.tar"}`)
	tasks, _ := Load(root)
	task := tasks[0]

	f, err := task.OpenEnvironment()
	if err != nil {
		t.Fatalf("OpenEnvironment: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	if string(data) != "ENV" {
		t.Fatalf("env content = %q, want ENV", data)
	}

	g, err := task.OpenTests()
	if err != nil {
		t.Fatalf("OpenTests: %v", err)
	}
	defer g.Close()
	data, _ = io.ReadAll(g)
	if string(data) != "FAKE" {
		t.Fatalf("tests content = %q, want FAKE", data)
	}
}

func TestOpenEnvironment_NilWhenAbsent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTaskDir(t, root, "x", `{"docker_image":"i","tests_tar":"tests.tar"}`)
	tasks, _ := Load(root)
	task := tasks[0]
	if task.EnvironmentTar != "" {
		// We wrote the file but the JSON didn't reference it.
		// Resolved path stays empty intentionally.
		t.Fatalf("EnvironmentTar = %q, want empty", task.EnvironmentTar)
	}
	f, err := task.OpenEnvironment()
	if err != nil {
		t.Fatalf("OpenEnvironment: %v", err)
	}
	if f != nil {
		t.Fatal("expected nil reader")
	}
}

func names(ts []Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}
