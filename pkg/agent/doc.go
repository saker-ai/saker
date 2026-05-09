// Package agent implements the core agent loop that drives the LLM interaction cycle.
//
// The loop iterates between model generation and tool execution, respecting
// context cancellation and a configurable maximum iteration count. Middleware
// hooks are invoked at each stage (before/after agent, model, and tool) to
// allow interception and customization.
package agent
