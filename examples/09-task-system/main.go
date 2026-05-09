package main

import (
	"context"
	"fmt"
	"log"

	"github.com/cinience/saker/pkg/api"
	modelpkg "github.com/cinience/saker/pkg/model"
)

func main() {
	provider := &modelpkg.AnthropicProvider{ModelName: "claude-sonnet-4-5-20250929"}

	rt, err := api.New(context.Background(), api.Options{
		ModelFactory: provider,
	})
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	// Test the Task system: create multiple tasks and set up dependencies
	prompt := `Please help me test the full functionality of the Task system:

1. Use TaskCreate to create 3 tasks:
   - Task A: "Read config file" (subject: "Read config", description: "Load settings.json", activeForm: "Reading config")
   - Task B: "Validate config" (subject: "Validate config", description: "Check config validity", activeForm: "Validating config")
   - Task C: "Start service" (subject: "Start service", description: "Initialize server", activeForm: "Starting service")

2. Use TaskUpdate to set dependencies:
   - Task B depends on Task A (B blockedBy A)
   - Task C depends on Task B (C blockedBy B)

3. Use TaskList to list all tasks and their dependencies

4. Use TaskUpdate to mark Task A as in_progress, then as completed

5. Use TaskList again to verify that Task B is automatically unblocked

6. Use TaskGet to fetch the details of Task B

Please execute these steps in order and describe the result after each step.`

	resp, err := rt.Run(context.Background(), api.Request{
		Prompt: prompt,
	})
	if err != nil {
		log.Fatalf("run: %v", err)
	}

	if resp.Result != nil {
		fmt.Println("\n=== Task System Test Result ===")
		fmt.Println(resp.Result.Output)
	}
}
