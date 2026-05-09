package server

import "testing"

// build assembles a string from segments to avoid embedding literal
// function-call XML in source (which can confuse downstream tools that
// scan for closing tags).
func build(segs ...string) string {
	out := ""
	for _, s := range segs {
		out += s
	}
	return out
}

func TestStripFunctionCallArtifacts(t *testing.T) {
	t.Parallel()

	closeTriple := build("</param", "eter>\n</func", "tion>\n</tool_", "call>")
	openFn := build("<func", "tion=generate_image>")
	claudeInvoke := build("<function_calls><inv", "oke name=\"foo\"><para", "meter name=\"x\">1</par", "ameter></in", "voke></function_calls>")
	fenceTok := build("<|FunctionCall", "Begin|>")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty",
			in:   "",
			want: "",
		},
		{
			name: "regression_eddaff17_qwen_close_tags",
			// The garbled tail from the eddaff17 incident must collapse to nothing.
			in:   closeTriple + "\n\n\n" + closeTriple,
			want: "",
		},
		{
			name: "leading_prose_with_trailing_xml",
			in:   "图片已生成，可以在画布看到。\n" + closeTriple,
			want: "图片已生成，可以在画布看到。",
		},
		{
			name: "embedded_function_open_tag",
			in:   "Sure, calling tool now " + openFn,
			want: "Sure, calling tool now",
		},
		{
			name: "claude_invoke_block",
			in:   "Hello world\n" + claudeInvoke,
			want: "Hello world",
		},
		{
			name: "fence_token",
			in:   "result: ok " + fenceTok,
			want: "result: ok",
		},
		{
			name: "plain_text_untouched",
			in:   "Just normal prose.\n第二行中文。",
			want: "Just normal prose.\n第二行中文。",
		},
		{
			name: "preserves_legit_angle_brackets",
			// "<3" is a heart, not a tag — must stay.
			in:   "I love this <3",
			want: "I love this <3",
		},
		{
			name: "collapses_extra_blank_lines",
			in:   "para1\n\n\n\n\npara2",
			want: "para1\n\npara2",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stripFunctionCallArtifacts(tc.in)
			if got != tc.want {
				t.Errorf("stripFunctionCallArtifacts(%q):\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}
