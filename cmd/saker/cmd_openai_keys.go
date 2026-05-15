package main

import (
	"io"

	"github.com/saker-ai/saker/pkg/project"
)

func runOpenAIKeyCommand(stdout, stderr io.Writer, args []string) error {
	return project.RunOpenAIKeyCommand(stdout, stderr, args)
}
