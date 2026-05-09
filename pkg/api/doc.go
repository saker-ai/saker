// Package api provides the primary entry point for the saker Agent SDK.
//
// It exposes [Runtime] for creating and managing agent sessions, supporting
// both synchronous ([Runtime.Run]) and streaming ([Runtime.RunStream]) execution
// modes. The package bridges the agent core loop, model providers, tool registry,
// middleware chain, and optional subsystems (hooks, skills, subagents, sandbox)
// into a single cohesive interface.
//
// Basic usage:
//
//	rt, err := api.New(ctx, api.Options{
//	    ModelFactory: provider,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer rt.Close()
//
//	resp, err := rt.Run(ctx, api.Request{Prompt: "Hello"})
package api
