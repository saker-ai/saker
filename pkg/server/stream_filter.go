package server

import (
	"fmt"
	"regexp"
	"strings"
)

// streamArtifactFilter strips function-call artifacts (e.g. <tool_call>,
// <|FunctionCallBegin|>) from text deltas as they stream out, before they
// reach the SSE subscribers.
//
// Why: stripFunctionCallArtifacts already runs once on the *persisted*
// assistant reply, so the saved history is clean. But raw deltas were being
// forwarded unfiltered — meaning if a confused model bled "</tool_call>"
// into its text channel, the user momentarily saw the tag flash on screen
// before the post-stream rewrite removed it from history. The filter here
// closes that gap so subscribers never see the garbage in the first place.
//
// How: a chunk that's clearly free of any tag fragment is forwarded as-is.
// Otherwise the filter holds back two kinds of tail data:
//
//  1. A *partial* tag at the very end (we've seen "<" or "<|" with no
//     matching closer yet) — held until the next chunk completes or
//     refutes the tag.
//  2. An *opened-but-unclosed* artifact block (e.g. "<tool_call>{json}"
//     with no matching "</tool_call>" yet) — held so subscribers don't
//     briefly see the inner garbage between opener and closer.
//
// Flush() releases whatever is left at end-of-stream, run through the
// strip pass so half-emitted tags don't reach subscribers in raw form.
//
// The held buffer is bounded by streamFilterMaxBuffer to defend against a
// pathological never-closing stream — past the cap we give up, flush, and
// start fresh.
type streamArtifactFilter struct {
	pending []byte
}

// streamFilterMaxBuffer caps the held-back bytes. Function-call inner JSON
// payloads can be a few hundred bytes; 16 KiB is roomy enough for any sane
// case yet still bounded.
const streamFilterMaxBuffer = 16 * 1024

// Push consumes a new delta chunk and returns whatever is safe to emit
// downstream after stripping function-call artifacts. Bytes that might
// still be the prefix of (or inside) a tag block are held until Push or
// Flush is called again.
//
// Returning "" is normal — it means the chunk is entirely held back.
// Subscribers that see empty deltas should ignore them.
func (f *streamArtifactFilter) Push(chunk string) string {
	if chunk == "" {
		return ""
	}
	if len(f.pending) == 0 && !strings.ContainsAny(chunk, "<") {
		return chunk
	}
	if len(f.pending)+len(chunk) > streamFilterMaxBuffer {
		// Pathological input: flush everything through the strip pass and
		// start over rather than grow without bound.
		combined := string(f.pending) + chunk
		f.pending = f.pending[:0]
		return stripStreamArtifacts(combined)
	}
	f.pending = append(f.pending, chunk...)
	combined := string(f.pending)
	safe, hold := splitForArtifacts(combined)
	f.pending = append(f.pending[:0], hold...)
	if safe == "" {
		return ""
	}
	return stripStreamArtifacts(safe)
}

// Flush releases any held-back bytes at end-of-stream. The result is run
// through the canonical strip pass (which also trims trailing whitespace
// — appropriate at end-of-stream where there's no next chunk to join).
func (f *streamArtifactFilter) Flush() string {
	if len(f.pending) == 0 {
		return ""
	}
	out := stripFunctionCallArtifacts(string(f.pending))
	f.pending = f.pending[:0]
	return out
}

// stripStreamArtifacts is the in-stream variant of
// stripFunctionCallArtifacts. It drops the punctuation/blank-line/TrimRight
// cleanup steps because those would corrupt the chunk boundary — trailing
// whitespace in the current chunk may need to join with the next chunk's
// prefix to form a single word.
func stripStreamArtifacts(s string) string {
	if s == "" {
		return s
	}
	out := s
	for i := 0; i < 4; i++ {
		next := out
		for _, re := range blockArtifactRes {
			next = re.ReplaceAllString(next, "")
		}
		if next == out {
			break
		}
		out = next
	}
	out = functionCallArtifactRe.ReplaceAllString(out, "")
	out = fenceArtifactRe.ReplaceAllString(out, "")
	return out
}

// splitForArtifacts decides which suffix of the buffered text must be
// withheld until more data arrives. The rules layer in order:
//
//  1. Trailing partial tag — a "<" (or "<|") near the end whose closer
//     hasn't arrived yet (handled by splitAtPartialTag).
//  2. Open-but-unclosed artifact block — once a known opener like
//     "<tool_call>" is in the safe portion without a matching closer, hold
//     from that opener onwards so subscribers never see the inner JSON.
func splitForArtifacts(s string) (safe, hold string) {
	safe, hold = splitAtPartialTag(s)
	openerIdx := lastUnclosedOpenerIdx(safe)
	if openerIdx >= 0 {
		hold = safe[openerIdx:] + hold
		safe = safe[:openerIdx]
	}
	return safe, hold
}

// splitAtPartialTag returns (safe, hold) where hold is the trailing slice
// that *might* still grow into a function-call tag once more bytes arrive.
//
// The decision is made on the rightmost '<' in the buffer:
//   - If a '>' appears after it, no partial tag trails the buffer; emit all.
//   - If the next byte after '<' looks like the start of a tag name
//     (letter, '/', or '|'), hold from that '<' onwards.
//   - Otherwise the '<' is prose ("5 < 10"), emit all.
func splitAtPartialTag(s string) (safe, hold string) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != '<' {
			continue
		}
		rest := s[i+1:]
		if strings.Contains(rest, ">") {
			return s, ""
		}
		if isPotentialTagStart(rest) {
			return s[:i], s[i:]
		}
		return s, ""
	}
	return s, ""
}

// isPotentialTagStart reports whether the bytes immediately following a '<'
// could be the prefix of a function-call tag or sentinel token. An empty
// rest is treated as potentially-partial because we have no information yet.
func isPotentialTagStart(rest string) bool {
	if rest == "" {
		return true
	}
	c := rest[0]
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c == '/':
		return true
	case c == '|':
		return true
	}
	return false
}

// openerRes / closerRes match a single opener / closer for each known
// artifact tag. Used by lastUnclosedOpenerIdx to detect an opened-but-
// unclosed block that must be held back so subscribers don't briefly see
// the inner content between opener and closer.
var openerRes, closerRes = func() ([]*regexp.Regexp, []*regexp.Regexp) {
	openers := make([]*regexp.Regexp, 0, len(blockArtifactTags))
	closers := make([]*regexp.Regexp, 0, len(blockArtifactTags))
	for _, t := range blockArtifactTags {
		q := regexp.QuoteMeta(t)
		openers = append(openers, regexp.MustCompile(fmt.Sprintf(`(?i)<\s*%s\b[^>]*>`, q)))
		closers = append(closers, regexp.MustCompile(fmt.Sprintf(`(?i)<\s*/\s*%s\s*>`, q)))
	}
	return openers, closers
}()

// lastUnclosedOpenerIdx returns the byte index of the leftmost opener of
// any known artifact tag that has no matching closer after it in s. -1 if
// no such opener exists.
//
// We hold from the *leftmost* unclosed opener so the held region encompasses
// everything that would later be wiped by the block-strip pass once the
// closer arrives. Holding only the rightmost opener would leak text between
// nested openers.
func lastUnclosedOpenerIdx(s string) int {
	leftmost := -1
	for i, openRe := range openerRes {
		closeRe := closerRes[i]
		opens := openRe.FindAllStringIndex(s, -1)
		if len(opens) == 0 {
			continue
		}
		closes := closeRe.FindAllStringIndex(s, -1)
		// Pair openers with closers in order. Any opener with no closer
		// after it is unclosed; we want the leftmost such opener.
		ci := 0
		for _, o := range opens {
			oStart := o[0]
			// Advance past closers that precede this opener (these closed
			// an earlier opener, not this one).
			for ci < len(closes) && closes[ci][0] < oStart {
				ci++
			}
			if ci >= len(closes) {
				if leftmost == -1 || oStart < leftmost {
					leftmost = oStart
				}
				break
			}
			ci++ // this closer pairs with this opener
		}
	}
	return leftmost
}
