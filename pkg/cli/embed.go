//go:build !nofrontend

package cli

import (
	"embed"
	"io/fs"
)

//go:embed all:frontend/dist
var embeddedFrontend embed.FS

func getEmbeddedFrontend() (fs.FS, error) {
	return fs.Sub(embeddedFrontend, "frontend/dist")
}

//go:embed all:editor/dist
var embeddedEditor embed.FS

func getEmbeddedEditor() (fs.FS, error) {
	return fs.Sub(embeddedEditor, "editor/dist")
}
