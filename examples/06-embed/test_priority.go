//go:build ignore
// +build ignore

package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/model"
)

//go:embed .saker
var claudeFS embed.FS

func main() {
	fmt.Println("=== Test Filesystem Priority ===")
	fmt.Println()

	// Create a stub provider (will not actually call the API)
	provider := &model.AnthropicProvider{
		APIKey:    "sk-test-key",
		ModelName: "claude-sonnet-4-5-20250929",
	}

	// Create Runtime, passing in the embedded filesystem
	runtime, err := api.New(context.Background(), api.Options{
		ProjectRoot:  ".",
		ModelFactory: provider,
		EmbedFS:      claudeFS,
	})
	if err != nil {
		log.Fatalf("failed to create runtime: %v", err)
	}
	defer runtime.Close()

	fmt.Println("✓ Runtime created successfully")
	fmt.Println("✓ Embedded .saker directory loaded")
	fmt.Println()
	fmt.Println("Notes:")
	fmt.Println("- If .saker/settings.local.json exists locally, it overrides the embedded config")
	fmt.Println("- If it does not exist locally, the embedded .saker/settings.json is used")
	fmt.Println()

	// Check whether a local override file exists
	if _, err := os.Stat(".saker/settings.local.json"); err == nil {
		fmt.Println("✓ Local .saker/settings.local.json detected — local config takes priority")
	} else {
		fmt.Println("✓ No local override file detected, using embedded config")
	}
}
