package main

import (
	"context"
	"fmt"
	"log"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/middleware"
	modelpkg "github.com/cinience/saker/pkg/model"
)

func main() {
	// Create an Anthropic provider
	provider := &modelpkg.AnthropicProvider{ModelName: "claude-sonnet-4-5-20250929"}

	// Initialize the runtime with default configuration
	traceMW := middleware.NewTraceMiddleware(".trace")
	rt, err := api.New(context.Background(), api.Options{
		ModelFactory: provider,
		Middleware:   []middleware.Middleware{traceMW},
	})
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	// Make a synchronous call with a fixed prompt
	resp, err := rt.Run(context.Background(), api.Request{Prompt: "Hello"})
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	if resp.Result != nil {
		fmt.Println(resp.Result.Output)
	}
}
