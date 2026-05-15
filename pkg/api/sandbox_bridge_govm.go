//go:build govm && cgo

package api

import (
	sandboxenv "github.com/saker-ai/saker/pkg/sandbox/env"
	"github.com/saker-ai/saker/pkg/sandbox/govmenv"
)

func init() {
	govmEnvFactory = func(projectRoot string, opts *GovmOptions) sandboxenv.ExecutionEnvironment {
		return govmenv.New(projectRoot, opts)
	}
}
