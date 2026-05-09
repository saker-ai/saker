package env

import "testing"

func TestSanitizeName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"", "session"},
		{"  ", "session"},
		{"my-project", "my-project"},
		{"My Project Name", "my-project-name"},
		{"test_container_01", "test_container_01"},
		{"hello@world!", "hello-world"},
		{"  hello  ", "hello"},
		{"_leading_", "leading"},
		{"---leading---", "leading"},
		{"a", "a"},
		{"A_VERY_LONG_NAME_THAT_EXCEEDS_32_CHARACTERS_LIMIT", "a_very_long_name_that_exceeds_32"},
		{"!@#$%^&*()", "session"},
		{"Hello-World_123", "hello-world_123"},
	}

	for _, tc := range tests {
		if got := SanitizeName(tc.in); got != tc.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}