package config

import (
	"encoding/json"
	"testing"
)

// FuzzMergeSettings drives MergeSettings with arbitrary JSON-decoded
// Settings pairs. The merge must:
//  1. Never panic on any (lower, higher) pair.
//  2. Always return a non-nil Settings when at least one input is non-nil.
//  3. Be re-marshalable to JSON without error (no aliasing crashes).
//
// We deliberately do NOT assert idempotency: hook entries (and a few other
// list-shaped fields) are intentionally concatenated when both lower and
// higher provide them, so re-merging higher into the result legitimately
// duplicates those entries. The three properties above are the contract.
func FuzzMergeSettings(f *testing.F) {
	// Seeds: empty objects, partial overrides, conflicting keys, nested
	// structures, and known nil-ish edge cases.
	seedPairs := [][2]string{
		{`{}`, `{}`},
		{`{}`, `{"model":"sonnet"}`},
		{`{"model":"opus"}`, `{"model":"sonnet"}`},
		{`{"env":{"A":"1","B":"2"}}`, `{"env":{"B":"3","C":"4"}}`},
		{`{"permissions":{"allow":["a"],"deny":["b"]}}`, `{"permissions":{"allow":["c"],"defaultMode":"plan"}}`},
		{`{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"echo"}]}]}}`,
			`{"hooks":{"PostToolUse":[{"matcher":"x","hooks":[{"type":"command","command":"x"}]}]}}`},
		{`{"sandbox":{"enabled":true,"network":{"allowLocalBinding":false}}}`,
			`{"sandbox":{"enabled":false,"network":{"httpProxyPort":8080}}}`},
		{`{"storage":{"backend":"osfs","osfs":{"root":"/a"}}}`,
			`{"storage":{"backend":"s3","s3":{"bucket":"b","region":"r"}}}`},
		{`{"failover":{"enabled":true,"models":[{"provider":"a","model":"x"}]}}`,
			`{"failover":{"maxRetries":5}}`},
		{`{"cleanupPeriodDays":0,"includeCoAuthoredBy":false}`,
			`{"cleanupPeriodDays":30,"includeCoAuthoredBy":true}`},
	}
	for _, pair := range seedPairs {
		f.Add([]byte(pair[0]), []byte(pair[1]))
	}

	f.Fuzz(func(t *testing.T, lowerJSON, higherJSON []byte) {
		var lower, higher *Settings
		if len(lowerJSON) > 0 {
			var s Settings
			if err := json.Unmarshal(lowerJSON, &s); err != nil {
				t.Skip()
			}
			lower = &s
		}
		if len(higherJSON) > 0 {
			var s Settings
			if err := json.Unmarshal(higherJSON, &s); err != nil {
				t.Skip()
			}
			higher = &s
		}

		// Property 1: must not panic.
		got := MergeSettings(lower, higher)

		// Property 2: at least one non-nil → result must be non-nil.
		if (lower != nil || higher != nil) && got == nil {
			t.Fatalf("MergeSettings returned nil for non-nil inputs")
		}
		if lower == nil && higher == nil && got != nil {
			t.Fatalf("MergeSettings returned non-nil for two nil inputs")
		}

		// Property 3: result must round-trip through JSON (catches aliasing /
		// unmarshalable shapes that would explode encode-side).
		if got != nil {
			if _, err := json.Marshal(got); err != nil {
				t.Fatalf("merged settings failed to marshal: %v", err)
			}
		}

	})
}
