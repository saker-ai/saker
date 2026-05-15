//go:build nofrontend

package main

import (
	"fmt"
	"io/fs"
)

func getEmbeddedFrontend() (fs.FS, error) {
	return nil, fmt.Errorf("frontend not embedded (built with nofrontend tag)")
}

func getEmbeddedEditor() (fs.FS, error) {
	return nil, fmt.Errorf("editor not embedded (built with nofrontend tag)")
}
