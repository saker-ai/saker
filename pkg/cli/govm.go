//go:build govm && cgo

package cli

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/saker-ai/saker/pkg/api"
	govmclient "github.com/godeps/govm/pkg/client"
)

func validateGovmPlatformDefault() error {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64", "linux/arm64", "darwin/arm64":
		return nil
	default:
		return fmt.Errorf("govm requires linux/amd64, linux/arm64, or darwin/arm64; current platform is %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

func validateGovmRuntimeDefault(opts api.GovmOptions) error {
	rt, err := govmclient.NewRuntime(&govmclient.RuntimeOptions{HomeDir: opts.RuntimeHome})
	if err != nil {
		return err
	}
	rt.Close()
	return nil
}

func isGovmNativeUnavailableDefault(err error) bool {
	return errors.Is(err, govmclient.ErrNativeUnavailable) || strings.Contains(strings.ToLower(err.Error()), "govm native bridge unavailable")
}
