package server

import (
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/api"
)

// TestTurnPersistenceStripsTrailingToolCallArtifact regresses the eddaff17
// case end-to-end through the same code paths handler.executeTurnWithBlocks
// uses for streaming + persistence:
//
//   - For each fake delta we drive streamArtifactFilter.Push (the SSE side)
//     and accumulate the raw text into buf (the persistence side).
//   - After the stream loop ends, cleanAssistantReply runs over buf, just
//     like the handler does at line 1371.
//
// Assertions:
//   - The emitted (filtered) stream never contains "tool_call" — the user
//     UI never sees the artifact.
//   - The persisted reply contains "the answer is 42" but not the artifact.
//   - The trailing whitespace residue from the stripped tag does not leave
//     a dangling blank line at the end.
func TestTurnPersistenceStripsTrailingToolCallArtifact(t *testing.T) {
	t.Parallel()

	// Synthetic deltas: a typical reply chunked into pieces, with the
	// model leaking "</tool_call>" at the tail (eddaff17 fingerprint).
	deltas := []string{
		"the ",
		"answer ",
		"is ",
		"42.",
		"</tool",
		"_call>",
	}

	var emitted strings.Builder
	var buf strings.Builder
	filter := &streamArtifactFilter{}
	for _, d := range deltas {
		buf.WriteString(d)
		emitted.WriteString(filter.Push(d))
	}
	emitted.WriteString(filter.Flush())

	streamSeen := emitted.String()
	if strings.Contains(streamSeen, "tool_call") {
		t.Fatalf("subscriber stream leaked artifact, got %q", streamSeen)
	}
	if !strings.Contains(streamSeen, "the answer is 42") {
		t.Fatalf("subscriber stream missing real reply, got %q", streamSeen)
	}

	persisted := cleanAssistantReply(buf.String())
	if strings.Contains(persisted, "tool_call") {
		t.Fatalf("persisted reply leaked artifact, got %q", persisted)
	}
	if persisted != "the answer is 42." {
		t.Fatalf("persisted reply wrong; got %q want %q", persisted, "the answer is 42.")
	}
}

// TestTurnPersistenceFullyArtifactReplyBecomesEmpty regresses the worst
// case: the entire model output was function-call syntax with no real
// prose. handler.executeTurnWithBlocks must skip the AppendItem call so
// the thread doesn't get an empty assistant bubble.
func TestTurnPersistenceFullyArtifactReplyBecomesEmpty(t *testing.T) {
	t.Parallel()

	deltas := []string{
		"<function_calls>",
		"<invoke name=\"foo\">",
		"<parameter name=\"x\">1</parameter>",
		"</invoke>",
		"</function_calls>",
	}
	var buf strings.Builder
	for _, d := range deltas {
		buf.WriteString(d)
	}
	if got := cleanAssistantReply(buf.String()); got != "" {
		t.Fatalf("artifact-only reply must clean to empty, got %q", got)
	}
}

// TestTurnPersistenceStrippedReplyKeepsTrailingPunctuation ensures the
// strip doesn't accidentally consume legitimate punctuation that happens
// to follow the artifact (e.g. "good answer.</tool_call>" → "good answer.")
// — the trailing period belongs to the prose, not the tag.
func TestTurnPersistenceStrippedReplyKeepsTrailingPunctuation(t *testing.T) {
	t.Parallel()
	got := cleanAssistantReply("good answer.</tool_call>")
	if got != "good answer." {
		t.Fatalf("expected %q, got %q", "good answer.", got)
	}
}

// TestTurnPersistenceStreamFilterMatchesPersistOnSplitArtifact verifies the
// invariant that subscribers see *exactly* what the persisted reply ends up
// containing, even when the artifact straddles a chunk boundary. Without
// the streaming filter, the user would see "</tool" flash on screen for a
// frame before the closer arrives — a UX regression we explicitly defend
// against here.
func TestTurnPersistenceStreamFilterMatchesPersistOnSplitArtifact(t *testing.T) {
	t.Parallel()
	deltas := []string{
		"hello ", "world ", "<tool_call>", "{\"x\":1}", "</tool_call>", " bye",
	}
	var emitted strings.Builder
	var buf strings.Builder
	filter := &streamArtifactFilter{}
	for _, d := range deltas {
		buf.WriteString(d)
		emitted.WriteString(filter.Push(d))
	}
	emitted.WriteString(filter.Flush())

	persisted := cleanAssistantReply(buf.String())
	streamCleaned := strings.TrimSpace(emitted.String())
	if streamCleaned != persisted {
		t.Fatalf("stream-cleaned vs persisted divergence:\n stream: %q\npersist: %q",
			streamCleaned, persisted)
	}
	if persisted != "hello world  bye" && persisted != "hello world bye" {
		// Either is acceptable: stripFunctionCallArtifacts collapses runs of
		// blank lines but does not normalize internal spaces.
		t.Fatalf("unexpected persisted form: %q", persisted)
	}
}

// TestTurnPersistenceStreamFilterNoOpForCleanReply confirms the filter is
// transparent when the model never emits any tag-like input — production
// streams should not be paying for buffering or regex passes for normal
// chats. (Functional check: the emitted stream equals the raw input.)
func TestTurnPersistenceStreamFilterNoOpForCleanReply(t *testing.T) {
	t.Parallel()
	deltas := []string{"hello ", "there, ", "how can I help today?"}
	var emitted strings.Builder
	filter := &streamArtifactFilter{}
	for _, d := range deltas {
		emitted.WriteString(filter.Push(d))
	}
	emitted.WriteString(filter.Flush())

	want := strings.Join(deltas, "")
	if got := emitted.String(); got != want {
		t.Fatalf("filter must be transparent for clean text\n got: %q\nwant: %q", got, want)
	}
}

// _ keeps the api import live: handler.executeTurnWithBlocks consumes
// api.StreamEvent values, and any future expansion of these tests will
// likely script those events. Importing here ensures we notice if the type
// is renamed or moved.
var _ = api.StreamEvent{}
