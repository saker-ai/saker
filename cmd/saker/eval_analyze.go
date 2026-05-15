package main

import (
	"io"

	"github.com/saker-ai/saker/pkg/eval/terminalbench"
)

func runEvalAnalyze(stdout, stderr io.Writer, args []string) error {
	return terminalbench.RunAnalyze(stdout, stderr, args)
}
