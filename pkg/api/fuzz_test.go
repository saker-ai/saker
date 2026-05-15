package api

import (
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/message"
)

// FuzzLooksLikeBase64 stresses the heuristic that decides whether a tool
// result string is mostly base64 (and therefore should be replaced with a
// placeholder before being sent to a summary model).
//
// Properties under test:
//  1. Must not panic on any UTF-8 input, including unicode runes that
//     exceed the byte length.
//  2. Result must be deterministic — calling twice with the same input
//     yields the same answer.
//  3. The classification by looksLikeBase64 must be consistent with
//     stripBase64FromResult, which delegates to it.
func FuzzLooksLikeBase64(f *testing.F) {
	seeds := []string{
		"",                                      // empty
		"hello world",                           // far too short
		strings.Repeat("A", 600),                // pure base64 alphabet
		strings.Repeat("AB12+/=", 200),          // padded base64 mix
		strings.Repeat("a", 2000) + "!!!",       // mostly b64 but long
		strings.Repeat("ABC ", 500),             // spaces drop ratio
		strings.Repeat("héllo", 200),            // unicode (multi-byte)
		strings.Repeat("AAAA", 400) + "\x00END", // embedded NUL
		strings.Repeat("xyz123/+=", 300),        // long, b64-shaped
		"=" + strings.Repeat("A", 1500),         // leading pad
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		// Determinism.
		a := looksLikeBase64(s)
		b := looksLikeBase64(s)
		if a != b {
			t.Fatalf("looksLikeBase64 non-deterministic for input len=%d", len(s))
		}

		// Consistency with the public wrapper. stripBase64FromResult only
		// reports a strip when looksLikeBase64 is true AND len >= base64MinLen.
		stripped, didStrip := stripBase64FromResult(s)
		if didStrip {
			if !looksLikeBase64(s) {
				t.Fatalf("stripBase64FromResult reported strip but looksLikeBase64=false (len=%d)", len(s))
			}
			if stripped == s {
				t.Fatalf("stripBase64FromResult reported strip but returned original string")
			}
		} else if stripped != s {
			t.Fatalf("stripBase64FromResult mutated string without reporting strip")
		}

		// stripMediaContent must also stay panic-free with this string riding
		// inside a tool-call result, exercising the deeper code path.
		msgs := []message.Message{{
			Role: "assistant",
			ToolCalls: []message.ToolCall{
				{ID: "fuzz", Name: "tool", Result: s},
			},
		}}
		_ = stripMediaContent(msgs)
	})
}
