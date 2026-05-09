// Package model defines the provider-facing model interface and its implementations.
//
// It provides [Model] with Complete and CompleteStream methods that adapters
// (e.g., [AnthropicProvider]) implement for specific LLM providers. The package
// also includes model metadata lookup, context window detection, and cost
// estimation via an embedded LiteLLM pricing database.
package model
