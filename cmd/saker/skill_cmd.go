package main

import (
	"io"

	"github.com/cinience/saker/pkg/skillhub"
)

func runSkillCommand(stdout, stderr io.Writer, projectRoot string, args []string) error {
	return skillhub.RunCommand(stdout, stderr, projectRoot, args)
}
