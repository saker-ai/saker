package api

import "testing"

func TestDetectLanguage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"chinese", "你好世界，今天天气怎么样？", "Chinese"},
		{"english", "Hello world, how are you today?", "English"},
		{"japanese", "こんにちは世界、元気ですか？", "Japanese"},
		{"korean", "안녕하세요 세계, 오늘 기분이 어때요?", "Korean"},
		{"french", "Bonjour le monde, comment allez-vous aujourd'hui?", "French"},
		{"german", "Hallo Welt, wie geht es Ihnen heute?", "German"},
		{"spanish", "Hola mundo, cómo estás hoy?", "Spanish"},
		{"russian", "Привет мир, как дела сегодня?", "Russian"},
		{"arabic", "مرحبا بالعالم، كيف حالك اليوم؟", "Arabic"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := detectLanguage(tc.input)
			if got != tc.want {
				t.Errorf("detectLanguage(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestDetectLanguageTruncatesLongInput(t *testing.T) {
	t.Parallel()
	// Build a 500-rune Chinese string
	long := ""
	for i := 0; i < 500; i++ {
		long += "中"
	}
	got := detectLanguage(long)
	if got != "Chinese" {
		t.Errorf("expected Chinese for long input, got %q", got)
	}
}
