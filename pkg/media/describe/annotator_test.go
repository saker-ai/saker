package describe

import "testing"

func TestStripMarkdownFence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain JSON",
			in:   `{"visual":"test","action":"walk"}`,
			want: `{"visual":"test","action":"walk"}`,
		},
		{
			name: "fenced with json tag",
			in:   "```json\n{\"visual\":\"test\"}\n```",
			want: `{"visual":"test"}`,
		},
		{
			name: "fenced without tag",
			in:   "```\n{\"visual\":\"test\"}\n```",
			want: `{"visual":"test"}`,
		},
		{
			name: "fenced with extra whitespace",
			in:   "  ```json\n{\"visual\":\"test\"}\n```  ",
			want: `{"visual":"test"}`,
		},
		{
			name: "fenced JSON tag uppercase",
			in:   "```JSON\n{\"visual\":\"test\"}\n```",
			want: `{"visual":"test"}`,
		},
		{
			name: "multiline fenced",
			in:   "```json\n{\n  \"visual\": \"scene\",\n  \"action\": \"run\"\n}\n```",
			want: "{\n  \"visual\": \"scene\",\n  \"action\": \"run\"\n}",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "no fence just whitespace",
			in:   "  {\"visual\":\"test\"}  ",
			want: `{"visual":"test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stripMarkdownFence(tt.in)
			if got != tt.want {
				t.Errorf("stripMarkdownFence(%q)\n got  %q\n want %q", tt.in, got, tt.want)
			}
		})
	}
}
