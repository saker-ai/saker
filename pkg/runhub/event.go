// Package runhub implements an in-memory hub of long-running agent runs
// with multi-subscriber fan-out and bounded ring-buffer replay.
//
// Design constraints (from .docs/openai-inbound-gateway.md):
//   - Each saker Run.RunStream channel is single-consumer; the OpenAI
//     gateway needs N HTTP clients to subscribe to the same run (initial
//     POST + reconnect via Last-Event-ID), so we fan out here.
//   - Replay must be bounded: the ring buffer caps the per-run event log
//     so a long-running task can't blow memory.
//   - Slow subscribers must not stall the producer: if a subscriber's chan
//     is full, we drop events to that subscriber rather than block the
//     hub goroutine.
//
// The hub is intentionally process-local. Cross-process replay is a P1
// concern and lives behind a SQLite-backed implementation that mirrors
// this interface (see .docs §10.2).
package runhub

// Event is one item in a run's event log. Seq monotonically increases per
// run starting at 1; Type/Payload/Data are opaque to the hub — the gateway
// emits its own translation of saker StreamEvents into OpenAI chat.completion
// chunks before publishing.
type Event struct {
	// Seq is the per-run monotonic sequence number, starting at 1. Used
	// as the SSE Last-Event-ID for client-driven reconnect replay.
	Seq int

	// Type tags the event for routing (e.g. "chunk", "finish", "error").
	// The hub never inspects this — it's for subscribers to filter.
	Type string

	// Data is the serialized event payload (typically a JSON-encoded
	// chat.completion.chunk). The hub stores it as a byte slice so the
	// SSE writer can `data: %s\n\n` it without re-marshaling.
	Data []byte
}
