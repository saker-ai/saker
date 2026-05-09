package server

import (
	"fmt"
	"regexp"
	"strings"
)

// blockArtifactTags is the list of function-calling tag names whose full
// open/close blocks (with nested content) should be removed from
// user-facing assistant text. Backreferences aren't available in RE2,
// so we pre-compile one regex per tag in blockArtifactRes.
var blockArtifactTags = []string{
	"function_calls",
	"tool_calls",
	"tool_call",
	"tool_use",
	"function",
	"invoke",
	"parameter",
	"antml:function_calls",
	"antml:invoke",
	"antml:parameter",
}

// blockArtifactRes is one regex per tag in blockArtifactTags, each matching
// a full <tag …>…</tag> pair (case-insensitive, dot matches newline,
// non-greedy so adjacent blocks don't merge). Stripping the whole block is
// preferable to stripping just the outer tags so we don't leave orphan
// parameter values like "1" floating in the prose.
var blockArtifactRes = func() []*regexp.Regexp {
	res := make([]*regexp.Regexp, 0, len(blockArtifactTags))
	for _, t := range blockArtifactTags {
		q := regexp.QuoteMeta(t)
		res = append(res, regexp.MustCompile(
			fmt.Sprintf(`(?is)<\s*%s\b[^>]*>.*?<\s*/\s*%s\s*>`, q, q),
		))
	}
	return res
}()

// functionCallArtifactRe matches the syntactic vocabulary of common
// tool-calling formats so we can strip leaked tags from the user-facing
// assistant reply. This covers:
//   - Qwen / Hermes XML: <tool_call>, <function=…>, <parameter name="…">
//   - Claude tool_use XML: <invoke>, <function_calls>, <parameter>
//   - Generic openings/closings of those tags, with or without attributes
//
// Used as a SECOND pass after blockArtifactRe to clean up orphan fragments
// (e.g. trailing </tool_call> with no matching opener — the eddaff17 case).
var functionCallArtifactRe = regexp.MustCompile(
	`(?i)</?\s*(?:tool_call|tool_calls|tool_use|function_calls|function|parameter|parameters|invoke|antml:function_calls|antml:invoke|antml:parameter)\b[^>]*>`,
)

// fenceArtifactRe matches stray `<|...|>` sentinel tokens that some open
// model families emit (e.g. <|FunctionCallBegin|>, <|im_start|>,
// <|tool_call_start|>). They never carry user-facing meaning.
var fenceArtifactRe = regexp.MustCompile(`<\|[A-Za-z0-9_]+\|>`)

// blankLineRe collapses runs of >=3 newlines (with optional spaces) down
// to a single blank line, so the cleaned text doesn't end up with big
// vertical gaps where the stripped tags used to live.
var blankLineRe = regexp.MustCompile(`\n[ \t]*\n([ \t]*\n)+`)

// stripFunctionCallArtifacts removes function-calling syntax fragments
// that occasionally leak into the model's text channel — most commonly
// when an Anthropic-style tool_use response is parsed but the model
// (often a Qwen/OpenAI-bridge variant) appended trailing closing tags
// outside the structured tool_call block.
//
// The function is intentionally permissive: anything matching the
// known function-calling vocabulary is dropped, regardless of context,
// because that vocabulary should never appear in normal prose.
//
// Returns the cleaned text. Empty input returns empty output.
func stripFunctionCallArtifacts(s string) string {
	if s == "" {
		return s
	}
	// 1. Strip whole open/close-paired blocks first (with their content).
	// Iterate to fixed point so nested blocks like
	// <function_calls><invoke>…</invoke></function_calls> collapse cleanly
	// even if outer pair would otherwise leave inner residue.
	out := s
	for i := 0; i < 4; i++ { // bounded to avoid pathological loops
		next := out
		for _, re := range blockArtifactRes {
			next = re.ReplaceAllString(next, "")
		}
		if next == out {
			break
		}
		out = next
	}
	// 2. Strip orphan tags that have no matching pair (e.g. dangling </tool_call>).
	out = functionCallArtifactRe.ReplaceAllString(out, "")
	// 3. Strip stray <|sentinel|> tokens used by some open models.
	out = fenceArtifactRe.ReplaceAllString(out, "")
	// 4. Empty out lines that became pure punctuation residue.
	out = collapseStrayPunctuation(out)
	// 5. Collapse runs of >=3 newlines down to one blank line.
	out = blankLineRe.ReplaceAllString(out, "\n\n")
	// 6. Trim trailing whitespace — anything left after the last meaningful
	// character was either residue or original blank padding; the caller
	// will TrimSpace the final result anyway, but normalizing here keeps
	// stripFunctionCallArtifacts itself a self-contained, easily testable
	// transform.
	out = strings.TrimRight(out, " \t\n")
	return out
}

// collapseStrayPunctuation drops lines that consist solely of punctuation
// or whitespace — the residue when "</tool_call>." used to be the entire
// line. Lines with at least one letter / digit / CJK character pass through
// untouched.
func collapseStrayPunctuation(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if hasMeaningfulRune(ln) {
			continue
		}
		lines[i] = ""
	}
	return strings.Join(lines, "\n")
}

// cleanAssistantReply turns a raw assistant text buffer into the canonical
// form persisted as a thread item. Returns "" when nothing meaningful
// remains (e.g. the whole reply was leaked function-call syntax). Extracted
// so handler integration tests can drive it directly without spinning up a
// full agent loop.
func cleanAssistantReply(raw string) string {
	if raw == "" {
		return ""
	}
	out := strings.TrimLeft(raw, ".")
	out = stripFunctionCallArtifacts(out)
	out = strings.TrimSpace(out)
	return out
}

// hasMeaningfulRune reports whether s contains at least one character that
// is plausibly part of human-readable content — letters, digits, or any
// non-ASCII rune (catches CJK, emoji, etc.). Pure-punctuation lines are
// considered residue from a stripped tag.
func hasMeaningfulRune(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			return true
		case r >= 'A' && r <= 'Z':
			return true
		case r >= '0' && r <= '9':
			return true
		case r >= 0x80:
			// Any non-ASCII rune (e.g. CJK, emoji) counts as meaningful.
			return true
		}
	}
	return false
}
