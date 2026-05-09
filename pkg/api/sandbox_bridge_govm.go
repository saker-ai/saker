//go:build govm && cgo

package api

import (
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/sandbox/govmenv"
)

func init() {
	govmEnvFactory = func(projectRoot string, opts *GovmOptions) sandboxenv.ExecutionEnvironment {
		return govmenv.New(projectRoot, opts)
	}
}
