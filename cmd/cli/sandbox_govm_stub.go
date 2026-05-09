//go:build !(govm && cgo)

package main

import (
	"fmt"

	"github.com/cinience/saker/pkg/api"
)

func init() {
	validateGovmPlatform = func() error {
		return fmt.Errorf("govm support not compiled in; rebuild with CGO_ENABLED=1 -tags govm")
	}
	validateGovmRuntime = func(_ api.GovmOptions) error {
		return fmt.Errorf("govm support not compiled in; rebuild with CGO_ENABLED=1 -tags govm")
	}
}

func isGovmNativeUnavailable(_ error) bool {
	return false
}
