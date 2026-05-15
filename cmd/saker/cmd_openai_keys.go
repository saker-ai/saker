package main

import (
	"io"

	"github.com/cinience/saker/pkg/project"
)

func runOpenAIKeyCommand(stdout, stderr io.Writer, args []string) error {
	return project.RunOpenAIKeyCommand(stdout, stderr, args)
}
