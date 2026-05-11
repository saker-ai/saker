package clikit

import (
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

// AskQuestionFunc mirrors toolbuiltin.AskQuestionFunc and is re-exported so
// front-end consumers (TUI) can register a handler without importing the
// builtin tool package directly.
type AskQuestionFunc = toolbuiltin.AskQuestionFunc

// AskQuestionRegistrar is implemented by runtime adapters that can accept an
// interactive AskUserQuestion handler. The TUI registers its handler at
// startup so that subsequent agent runs can prompt the user inside the
// terminal UI instead of returning the "not available" guard.
type AskQuestionRegistrar interface {
	SetAskQuestionFunc(fn AskQuestionFunc)
}
