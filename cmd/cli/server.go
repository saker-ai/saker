package main

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

// getEmbeddedEditor returns the OpenCut-derived browser editor static bundle.
// Mounted at /editor/ by the server when the binary is built with the editor
// assets present. When editor/dist is empty (only contains .gitkeep), the FS
// is still valid; the server simply serves a directory listing or 404 for
// missing files, which is fine for dev builds.
func getEmbeddedEditor() (fs.FS, error) {
	return fs.Sub(embeddedEditor, "editor/dist")
}
