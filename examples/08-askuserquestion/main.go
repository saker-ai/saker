//go:build !demo_simple && !demo_llm

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/model"
)

func main() {
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

	ctx := context.Background()

	// Initialize runtime (AskUserQuestion tool is included automatically)
	runtime, err := api.New(ctx, api.Options{
		ProjectRoot:  ".",
		ModelFactory: provider,
		EntryPoint:   api.EntryPointCLI,
	})
	if err != nil {
		log.Fatalf("failed to initialize runtime: %v", err)
	}
	defer runtime.Close()

	fmt.Println("=== AskUserQuestion Tool Demo ===")
	fmt.Println()

	// Scenario 1: single question, single-select
	fmt.Println("[Scenario 1] Single technology selection question")
	fmt.Println("----------------------------------------")
	result1, err := runtime.Run(ctx, api.Request{
		Prompt: `You are helping me design the technical architecture for a new project.

Use the AskUserQuestion tool to ask me:
- Question: "Which database should we use?"
- Header: "Database"
- Three options:
  1. PostgreSQL - powerful relational database
  2. MongoDB - flexible document database
  3. Redis - high-performance in-memory database
- Single-select mode`,
		SessionID: "demo-1",
	})
	if err != nil {
		log.Printf("scenario 1 error: %v\n", err)
	} else if result1.Result != nil {
		fmt.Printf("Output:\n%s\n", result1.Result.Output)
	}
	fmt.Println()

	// Scenario 2: multiple questions
	fmt.Println("[Scenario 2] Configure deployment environment (multiple questions)")
	fmt.Println("----------------------------------------")
	result2, err := runtime.Run(ctx, api.Request{
		Prompt: `You are helping me configure a deployment environment.

Use the AskUserQuestion tool to ask me three questions at once:

1. "Choose a deployment environment?"
   Header: "Environment"
   Options: Staging, Production
   Single-select

2. "Which features should be enabled?"
   Header: "Features"
   Options: Caching, Monitoring, Logging, Backup
   Multi-select

3. "Choose a deployment region?"
   Header: "Region"
   Options: US-East, EU-West
   Single-select`,
		SessionID: "demo-2",
	})
	if err != nil {
		log.Printf("scenario 2 error: %v\n", err)
	} else if result2.Result != nil {
		fmt.Printf("Output:\n%s\n", result2.Result.Output)
	}
	fmt.Println()

	// Scenario 3: multi-select question
	fmt.Println("[Scenario 3] Feature selection (multi-select)")
	fmt.Println("----------------------------------------")
	result3, err := runtime.Run(ctx, api.Request{
		Prompt: `You are helping me configure project features.

Use the AskUserQuestion tool to ask me:
- Question: "Which third-party services should be integrated?"
- Header: "Integrations"
- Four options:
  1. Stripe - payment processing
  2. SendGrid - email delivery
  3. Twilio - SMS service
  4. AWS S3 - file storage
- Multi-select mode (allow choosing multiple)`,
		SessionID: "demo-3",
	})
	if err != nil {
		log.Printf("scenario 3 error: %v\n", err)
	} else if result3.Result != nil {
		fmt.Printf("Output:\n%s\n", result3.Result.Output)
	}
	fmt.Println()

	// Scenario 4: real-world decision scenario
	fmt.Println("[Scenario 4] Real development decision")
	fmt.Println("----------------------------------------")
	result4, err := runtime.Run(ctx, api.Request{
		Prompt: `I am building a user authentication feature and am unsure which authentication method to use.

Please first analyse the pros and cons of each approach, then use the AskUserQuestion tool to ask for my preference.
The question should include 2-3 common authentication options.`,
		SessionID: "demo-4",
	})
	if err != nil {
		log.Printf("scenario 4 error: %v\n", err)
	} else if result4.Result != nil {
		fmt.Printf("Output:\n%s\n", result4.Result.Output)
	}
	fmt.Println()

	fmt.Println("=== Demo Complete ===")
	fmt.Println()
	fmt.Println("Notes:")
	fmt.Println("- The tool returns the question structure but does not wait for actual user input")
	fmt.Println("- In a real application, you need to implement a UI to collect user selections")
	fmt.Println("- User selections can be passed back via the 'answers' parameter")
}
