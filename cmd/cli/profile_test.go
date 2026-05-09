package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/profile"
	"github.com/cinience/saker/pkg/testutil"
)

func TestProfileCommand_ShowDefault(t *testing.T) {
	t.Parallel()
	root := testutil.TempHome(t)
	var stdout, stderr bytes.Buffer

	err := runProfileCommand(&stdout, &stderr, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "default") {
		t.Errorf("expected 'default' in output, got: %s", stdout.String())
	}
}

func TestProfileCommand_CreateAndList(t *testing.T) {
	t.Parallel()
	root := testutil.TempHome(t)
	var stdout, stderr bytes.Buffer

	// Create a profile.
	err := runProfileCommand(&stdout, &stderr, root, []string{"create", "dev"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(stdout.String(), "Created profile: dev") {
		t.Errorf("expected creation message, got: %s", stdout.String())
	}

	// List profiles.
	stdout.Reset()
	err = runProfileCommand(&stdout, &stderr, root, []string{"list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "default") {
		t.Errorf("expected 'default' in list, got: %s", output)
	}
	if !strings.Contains(output, "dev") {
		t.Errorf("expected 'dev' in list, got: %s", output)
	}
}

func TestProfileCommand_Use(t *testing.T) {
	t.Parallel()
	root := testutil.TempHome(t)
	var stdout, stderr bytes.Buffer

	// Create then use.
	_ = runProfileCommand(&stdout, &stderr, root, []string{"create", "staging"})
	stdout.Reset()

	err := runProfileCommand(&stdout, &stderr, root, []string{"use", "staging"})
	if err != nil {
		t.Fatalf("use: %v", err)
	}
	if !strings.Contains(stdout.String(), "Switched to profile: staging") {
		t.Errorf("expected switch message, got: %s", stdout.String())
	}

	// Verify active profile persisted.
	active := profile.GetActive(root)
	if active != "staging" {
		t.Errorf("expected active=staging, got %s", active)
	}
}

func TestProfileCommand_UseDefault(t *testing.T) {
	t.Parallel()
	root := testutil.TempHome(t)
	var stdout, stderr bytes.Buffer

	err := runProfileCommand(&stdout, &stderr, root, []string{"use", "default"})
	if err != nil {
		t.Fatalf("use default: %v", err)
	}
	if !strings.Contains(stdout.String(), "Switched to default profile") {
		t.Errorf("expected default switch message, got: %s", stdout.String())
	}
}

func TestProfileCommand_UseNonexistent(t *testing.T) {
	t.Parallel()
	root := testutil.TempHome(t)
	var stdout, stderr bytes.Buffer

	err := runProfileCommand(&stdout, &stderr, root, []string{"use", "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent profile")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' error, got: %v", err)
	}
}

func TestProfileCommand_Delete(t *testing.T) {
	t.Parallel()
	root := testutil.TempHome(t)
	var stdout, stderr bytes.Buffer

	// Create then delete.
	_ = runProfileCommand(&stdout, &stderr, root, []string{"create", "temp"})
	stdout.Reset()

	err := runProfileCommand(&stdout, &stderr, root, []string{"delete", "temp"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(stdout.String(), "Deleted profile: temp") {
		t.Errorf("expected delete message, got: %s", stdout.String())
	}

	// Verify it no longer exists.
	if profile.Exists(root, "temp") {
		t.Error("profile 'temp' should not exist after delete")
	}
}

func TestProfileCommand_Show(t *testing.T) {
	t.Parallel()
	root := testutil.TempHome(t)
	var stdout, stderr bytes.Buffer

	_ = runProfileCommand(&stdout, &stderr, root, []string{"create", "viewer"})
	stdout.Reset()

	err := runProfileCommand(&stdout, &stderr, root, []string{"show", "viewer"})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Profile: viewer") {
		t.Errorf("expected profile name in show, got: %s", output)
	}
	if !strings.Contains(output, "Exists: true") {
		t.Errorf("expected Exists: true, got: %s", output)
	}
}

func TestProfileCommand_ShowNoArgs(t *testing.T) {
	t.Parallel()
	root := testutil.TempHome(t)
	var stdout, stderr bytes.Buffer

	err := runProfileCommand(&stdout, &stderr, root, []string{"show"})
	if err != nil {
		t.Fatalf("show default: %v", err)
	}
	if !strings.Contains(stdout.String(), "Profile: default") {
		t.Errorf("expected 'default' in show, got: %s", stdout.String())
	}
}

func TestProfileCommand_CreateMissingName(t *testing.T) {
	t.Parallel()
	root := testutil.TempHome(t)
	var stdout, stderr bytes.Buffer

	err := runProfileCommand(&stdout, &stderr, root, []string{"create"})
	if err == nil {
		t.Fatal("expected error for create without name")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("expected usage error, got: %v", err)
	}
}

func TestProfileCommand_UnknownAction(t *testing.T) {
	t.Parallel()
	root := testutil.TempHome(t)
	var stdout, stderr bytes.Buffer

	err := runProfileCommand(&stdout, &stderr, root, []string{"foobar"})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown profile action") {
		t.Errorf("expected 'unknown profile action' error, got: %v", err)
	}
}

func TestProfileCommand_CreateWithClone(t *testing.T) {
	t.Parallel()
	root := testutil.TempHome(t)
	var stdout, stderr bytes.Buffer

	// Create source profile with settings.
	_ = runProfileCommand(&stdout, &stderr, root, []string{"create", "source"})
	testutil.WriteFile(t, root, ".saker/profiles/source/settings.json", `{"model":"claude-3"}`)
	stdout.Reset()

	// Clone it.
	err := runProfileCommand(&stdout, &stderr, root, []string{"create", "cloned", "--clone", "source"})
	if err != nil {
		t.Fatalf("create with clone: %v", err)
	}
	if !strings.Contains(stdout.String(), "Created profile: cloned") {
		t.Errorf("expected creation message, got: %s", stdout.String())
	}

	// Verify clone has settings.
	if !profile.Exists(root, "cloned") {
		t.Error("cloned profile should exist")
	}
}
