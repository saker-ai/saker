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
	// Read the API key from environment variables
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	}
	if apiKey == "" {
		log.Fatal("please set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN environment variable")
	}

	// Create Anthropic provider
	provider := &model.AnthropicProvider{
		APIKey:    apiKey,
		ModelName: "claude-sonnet-4-5-20250929",
	}

	// Create Runtime, passing in the embedded filesystem
	runtime, err := api.New(context.Background(), api.Options{
		ProjectRoot:  ".",
		ModelFactory: provider,
		EmbedFS:      claudeFS, // key: pass the embedded .saker directory
	})
	if err != nil {
		log.Fatalf("failed to create runtime: %v", err)
	}
	defer runtime.Close()

	fmt.Println("=== Embedded Filesystem Example ===")
	fmt.Println("This example demonstrates how to embed the .saker directory into the binary")
	fmt.Println()
	fmt.Println("Embedded configuration and skills are loaded automatically at runtime")
	fmt.Println("You can still override embedded config by creating a local .saker/settings.local.json")
	fmt.Println()

	// Run a simple test
	result, err := runtime.Run(context.Background(), api.Request{
		Prompt:    "list the current directory",
		SessionID: "embed-demo",
	})
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	fmt.Println("Result:")
	if result.Result != nil {
		fmt.Println(result.Result.Output)
	}
}
