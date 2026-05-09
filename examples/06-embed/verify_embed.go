//go:build ignore
// +build ignore

package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
)

//go:embed .saker
var claudeFS embed.FS

func main() {
	fmt.Println("=== Verify Embedded Filesystem ===")
	fmt.Println()

	// Verify settings.json
	data, err := fs.ReadFile(claudeFS, ".saker/settings.json")
	if err != nil {
		log.Fatalf("failed to read settings.json: %v", err)
	}
	fmt.Printf("✓ settings.json embedded (%d bytes)\n", len(data))
	fmt.Printf("  preview: %s\n", string(data[:min(100, len(data))]))
	fmt.Println()

	// Verify skill
	data, err = fs.ReadFile(claudeFS, ".saker/skills/demo/SKILL.md")
	if err != nil {
		log.Fatalf("failed to read SKILL.md: %v", err)
	}
	fmt.Printf("✓ skills/demo/SKILL.md embedded (%d bytes)\n", len(data))
	fmt.Printf("  preview: %s\n", string(data[:min(100, len(data))]))
	fmt.Println()

	// List all embedded files
	fmt.Println("Embedded file list:")
	err = fs.WalkDir(claudeFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			fmt.Printf("  - %s\n", path)
		}
		return nil
	})
	if err != nil {
		log.Fatalf("failed to walk embedded files: %v", err)
	}

	fmt.Println()
	fmt.Println("✓ All embedded files verified successfully!")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
