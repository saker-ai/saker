package aigo

import (
	"errors"
	"strings"
	"testing"

	"github.com/godeps/aigo/tooldef"
)

// TestFormatInvalidParamsIncludesActionableHints regresses the eddaff17
// thread incident: when the model emits a generate_image call with no
// prompt, the validation error returned to the model must carry enough
// context (required fields, provided fields, hint) for the model to
// self-correct on the next iteration. The bare "parameter X is required"
// error did not give the model anything to act on.
func TestFormatInvalidParamsIncludesActionableHints(t *testing.T) {
	t.Parallel()

	def := tooldef.GenerateImage()
	// Mimic the eddaff17 case: the model passed size+aspect_ratio but
	// forgot the required prompt field.
	provided := map[string]interface{}{
		"size":         "1024x1024",
		"aspect_ratio": "1:1",
	}
	err := errors.New(`parameter "prompt" is required`)

	out := formatInvalidParams(def, provided, err)

	for _, want := range []string{
		`Invalid parameters for tool "generate_image"`,
		`parameter "prompt" is required`,
		"required: [prompt]",
		"provided: [aspect_ratio, size]", // sorted, no duplicates
		"hint: re-emit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatInvalidParams output missing %q\nfull:\n%s", want, out)
		}
	}
}

// TestFormatInvalidParamsHandlesEmptyParams ensures we don't print "[]"
// surrounding garbage when the model called with no params at all, AND that
// the zero-params branch surfaces the upstream-proxy hint (the eddaff17
// fingerprint) so a confused model retries the WHOLE call rather than
// patching one missing field.
func TestFormatInvalidParamsHandlesEmptyParams(t *testing.T) {
	t.Parallel()

	def := tooldef.GenerateImage()
	out := formatInvalidParams(def, map[string]interface{}{}, errors.New(`parameter "prompt" is required`))

	for _, want := range []string{
		"provided: []",
		"required: [prompt]",
		"note: tool was called with no parameters at all",
		"API proxy may have dropped tool_use.input",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatInvalidParams empty-params output missing %q\nfull:\n%s", want, out)
		}
	}
}

// TestFormatInvalidParamsOmitsProxyNoteWhenSomeFieldsProvided ensures the
// upstream-proxy hint only fires when zero parameters arrived. If the model
// supplied something — even just an unrelated field — that's a normal
// validation miss and the hint would be misleading.
func TestFormatInvalidParamsOmitsProxyNoteWhenSomeFieldsProvided(t *testing.T) {
	t.Parallel()

	def := tooldef.GenerateImage()
	out := formatInvalidParams(def,
		map[string]interface{}{"size": "1024x1024"},
		errors.New(`parameter "prompt" is required`),
	)

	if strings.Contains(out, "tool was called with no parameters at all") {
		t.Errorf("proxy hint must not appear when the model supplied at least one field, got:\n%s", out)
	}
}
