package subagents

import (
	"fmt"
	"strings"

	"github.com/saker-ai/saker/pkg/message"
)

const (
	// ForkSubagentType is the synthetic agent type name for the fork path.
	ForkSubagentType = "fork"

	// ForkBoilerplateTag marks a message as belonging to a fork child,
	// used to detect recursive forking.
	ForkBoilerplateTag = "fork-boilerplate"

	// ForkDirectivePrefix precedes the user's directive in fork messages.
	ForkDirectivePrefix = "Your task:\n"

	// ForkPlaceholderResult is the identical placeholder used for all
	// tool_result blocks in the fork prefix. Must be the same across all
	// fork children to maximize prompt cache hits.
	ForkPlaceholderResult = "Fork started — processing in background"
)

// IsForkTarget returns true when the target indicates a fork path
// (empty string or explicit "fork" type).
func IsForkTarget(target string) bool {
	t := strings.TrimSpace(strings.ToLower(target))
	return t == "" || t == ForkSubagentType
}

// IsInForkChild detects whether the conversation already contains the
// fork boilerplate tag, preventing recursive forking.
func IsInForkChild(messages []message.Message) bool {
	tag := "<" + ForkBoilerplateTag + ">"
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		if strings.Contains(msg.Content, tag) {
			return true
		}
	}
	return false
}

// BuildChildDirective creates the fork boilerplate + directive text
// that instructs the fork child how to behave.
func BuildChildDirective(directive string) string {
	return fmt.Sprintf(`<%s>
STOP. READ THIS FIRST.

You are a forked worker process. You are NOT the main agent.

RULES (non-negotiable):
1. Do NOT spawn sub-agents; execute directly.
2. Do NOT converse, ask questions, or suggest next steps.
3. Do NOT editorialize or add meta-commentary.
4. USE your tools directly: Bash, Read, Write, etc.
5. Do NOT emit text between tool calls. Use tools silently, then report once at the end.
6. Stay strictly within your directive's scope.
7. Keep your report under 500 words unless the directive specifies otherwise.
8. Your response MUST begin with "Scope:". No preamble.
9. REPORT structured facts, then stop.

Output format (plain text labels, not markdown headers):
  Scope: <echo back your assigned scope in one sentence>
  Result: <the answer or key findings>
  Key files: <relevant file paths>
  Files changed: <list — include only if you modified files>
  Issues: <list — include only if there are issues to flag>
</%s>

%s%s`, ForkBoilerplateTag, ForkBoilerplateTag, ForkDirectivePrefix, directive)
}

// BuildForkedMessages constructs the messages to prepend to a fork child's
// conversation. For prompt cache sharing, all fork children must produce
// byte-identical API request prefixes. This function:
//  1. Keeps the full parent assistant message (all tool_use blocks, text)
//  2. Builds a single user message with placeholder tool_results for every
//     tool_use block, then appends a per-child directive text block
//
// Only the final directive differs per child, maximizing cache hits.
func BuildForkedMessages(directive string, lastAssistant message.Message) []message.Message {
	if len(lastAssistant.ToolCalls) == 0 {
		// No tool calls: just return the directive as a user message.
		return []message.Message{
			{Role: "user", Content: BuildChildDirective(directive)},
		}
	}

	// Clone the assistant message to avoid mutating the original.
	clonedAssistant := message.CloneMessage(lastAssistant)

	// Build tool_result content: identical placeholder for each tool_use.
	var parts []string
	for _, tc := range clonedAssistant.ToolCalls {
		parts = append(parts, fmt.Sprintf("[tool_result id=%s] %s", tc.ID, ForkPlaceholderResult))
	}
	// Append the per-child directive at the end.
	parts = append(parts, BuildChildDirective(directive))

	toolResultMsg := message.Message{
		Role:    "user",
		Content: strings.Join(parts, "\n\n"),
	}

	return []message.Message{clonedAssistant, toolResultMsg}
}
