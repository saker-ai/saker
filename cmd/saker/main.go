package main

import (
	"fmt"
	"os"

	"github.com/saker-ai/saker/pkg/cli"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	app := cli.New()
	app.Version = Version
	if err := app.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
