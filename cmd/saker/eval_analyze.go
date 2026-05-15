package main

import (
	"io"

	"github.com/cinience/saker/pkg/eval/terminalbench"
)

func runEvalAnalyze(stdout, stderr io.Writer, args []string) error {
	return terminalbench.RunAnalyze(stdout, stderr, args)
}
