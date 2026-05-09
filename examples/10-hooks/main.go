package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"runtime"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/core/hooks"
	modelpkg "github.com/cinience/saker/pkg/model"
)

func main() {
	// Get the directory of the current source file as the example root
	_, currentFile, _, _ := runtime.Caller(0)
	exampleDir := filepath.Dir(currentFile)
	scriptsDir := filepath.Join(exampleDir, "scripts")

	// Option 1: configure via TypedHooks in code (recommended for dynamic configuration)
	//
	// Features:
	//   - Async: run asynchronously, does not block the main flow
	//   - Once:  execute only once per session
	//   - Timeout: custom timeout (default 600s)
	//   - StatusMessage: status message displayed during execution
	typedHooks := []hooks.ShellHook{
		{
			Event:   events.PreToolUse,
			Command: filepath.Join(scriptsDir, "pre_tool.sh"),
		},
		{
			Event:   events.PostToolUse,
			Command: filepath.Join(scriptsDir, "post_tool.sh"),
			Async:   true, // run asynchronously, does not block tool calls
		},
	}

	// Create provider
	provider := &modelpkg.AnthropicProvider{
		ModelName: "claude-sonnet-4-5-20250514",
	}

	// Initialize runtime.
	// Hooks fire automatically when the agent executes a tool — no manual Publish needed.
	// Option 2: configure hooks via .saker/settings.json (see .saker/settings.json)
	rt, err := api.New(context.Background(), api.Options{
		ModelFactory: provider,
		ProjectRoot:  exampleDir,
		TypedHooks:   typedHooks,
	})
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	fmt.Println("=== Hooks Example ===")
	fmt.Println("Registered hooks:")
	fmt.Println("  - PreToolUse  (synchronous, validates tool calls)")
	fmt.Println("  - PostToolUse (asynchronous, logs execution results)")
	fmt.Println()
	fmt.Println("Exit code semantics (Claude Code spec):")
	fmt.Println("  exit 0  = success, parse stdout JSON")
	fmt.Println("  exit 2  = blocking error, stderr is the error message")
	fmt.Println("  other   = non-blocking, log stderr and continue")
	fmt.Println()

	// Run agent call — hooks fire automatically
	fmt.Println(">>> Running Agent call")
	fmt.Println("    Hooks will execute automatically when the agent calls a tool")
	fmt.Println()

	resp, err := rt.Run(context.Background(), api.Request{
		Prompt: "Use the pwd command to show the current directory",
	})
	if err != nil {
		log.Printf("run error: %v", err)
	} else if resp.Result != nil {
		fmt.Printf("\nOutput: %s\n", resp.Result.Output)
	}

	fmt.Println("\n=== Example Complete ===")
}
