// Package tool provides the tool registry, executor, and JSON Schema validation
// for agent tool calls.
//
// Tools implement the [Tool] interface (Name, Description, Schema, Execute) and
// are registered in a thread-safe [Registry]. Built-in tools (bash, file_read,
// file_write, grep, glob) live in the builtin sub-package. External tools can
// be added via MCP or custom registration.
package tool
