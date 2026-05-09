package api

import "github.com/pemistahl/lingua-go"

// langDetector is a package-level detector limited to common languages
// with low-accuracy mode to minimize memory footprint.
var langDetector = lingua.NewLanguageDetectorBuilder().
	FromLanguages(
		lingua.English, lingua.Chinese,
		lingua.Japanese, lingua.Korean,
		lingua.French, lingua.German, lingua.Spanish,
		lingua.Russian, lingua.Arabic,
	).
	WithLowAccuracyMode().
	Build()

var linguaToName = map[lingua.Language]string{
	lingua.English:  "English",
	lingua.Chinese:  "Chinese",
	lingua.Japanese: "Japanese",
	lingua.Korean:   "Korean",
	lingua.French:   "French",
	lingua.German:   "German",
	lingua.Spanish:  "Spanish",
	lingua.Russian:  "Russian",
	lingua.Arabic:   "Arabic",
}

// detectLanguage detects the language of the given text.
// Returns a language name (e.g. "Chinese") or empty string on failure.
func detectLanguage(text string) string {
	if len(text) == 0 {
		return ""
	}
	sample := []rune(text)
	if len(sample) > 200 {
		sample = sample[:200]
	}
	lang, ok := langDetector.DetectLanguageOf(string(sample))
	if !ok {
		return ""
	}
	if name, exists := linguaToName[lang]; exists {
		return name
	}
	return ""
}
