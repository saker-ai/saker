//go:build !(govm && cgo)

package cli

import (
	"fmt"

	"github.com/saker-ai/saker/pkg/api"
)

func validateGovmPlatformDefault() error {
	return fmt.Errorf("govm support not compiled in; rebuild with CGO_ENABLED=1 -tags govm")
}

func validateGovmRuntimeDefault(_ api.GovmOptions) error {
	return fmt.Errorf("govm support not compiled in; rebuild with CGO_ENABLED=1 -tags govm")
}

func isGovmNativeUnavailableDefault(_ error) bool {
	return false
}
