//go:build govm && cgo

package api

import (
	"testing"

	"github.com/cinience/saker/pkg/sandbox/govmenv"
)

func TestBuildExecutionEnvironmentSelectsGovm(t *testing.T) {
	root := t.TempDir()
	env := buildExecutionEnvironment(Options{
		ProjectRoot: root,
		Sandbox: SandboxOptions{
			Type: "govm",
			Govm: &GovmOptions{Enabled: true},
		},
	})
	if _, ok := env.(*govmenv.Environment); !ok {
		t.Fatalf("expected govm environment, got %T", env)
	}
}
