// Package middleware implements a chain-of-responsibility interception system
// with six stages: BeforeAgent, AfterAgent, BeforeModel, AfterModel,
// BeforeTool, and AfterTool.
//
// Middleware state is shared across stages via [State.Values], enabling
// cross-cutting concerns such as tracing, rate limiting, and safety checks.
package middleware
