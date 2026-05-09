package skills

import (
	"fmt"
	"strings"
)

// FuzzyPatchResult captures the outcome of a fuzzy find-and-replace operation.
type FuzzyPatchResult struct {
	Applied bool
	Matches int
	Preview string // returned on failure so the agent can self-correct
	Error   error
}

// FuzzyPatch performs a targeted find-and-replace within content, tolerating
// minor whitespace and indentation differences.
func FuzzyPatch(content, oldText, newText string, replaceAll bool) FuzzyPatchResult {
	if oldText == "" {
		return FuzzyPatchResult{Error: fmt.Errorf("old_text is empty")}
	}
	if oldText == newText {
		return FuzzyPatchResult{Error: fmt.Errorf("old_text and new_text are identical")}
	}

	// Try exact match first.
	count := strings.Count(content, oldText)
	if count > 0 {
		if count > 1 && !replaceAll {
			return FuzzyPatchResult{
				Matches: count,
				Preview: extractContext(content, oldText, 200),
				Error:   fmt.Errorf("found %d matches, use replace_all=true or provide more context", count),
			}
		}
		if replaceAll {
			return FuzzyPatchResult{
				Applied: true,
				Matches: count,
				Preview: strings.ReplaceAll(content, oldText, newText),
			}
		}
		return FuzzyPatchResult{
			Applied: true,
			Matches: 1,
			Preview: strings.Replace(content, oldText, newText, 1),
		}
	}

	// Normalize whitespace for fuzzy matching.
	normalized := normalizeWhitespace(content)
	normalizedOld := normalizeWhitespace(oldText)

	count = strings.Count(normalized, normalizedOld)
	if count == 0 {
		// Try line-by-line anchor matching.
		result := anchorMatch(content, oldText, newText, replaceAll)
		if result.Applied {
			return result
		}
		return FuzzyPatchResult{
			Matches: 0,
			Preview: extractPreview(content, 500),
			Error:   fmt.Errorf("no match found (exact or fuzzy)"),
		}
	}

	if count > 1 && !replaceAll {
		return FuzzyPatchResult{
			Matches: count,
			Preview: extractContext(normalized, normalizedOld, 200),
			Error:   fmt.Errorf("found %d fuzzy matches, provide more context", count),
		}
	}

	// Map normalized match positions back to original content.
	replaced := fuzzyReplace(content, oldText, newText, replaceAll)
	if replaced == content {
		return FuzzyPatchResult{
			Matches: 0,
			Preview: extractPreview(content, 500),
			Error:   fmt.Errorf("fuzzy match found but replacement failed"),
		}
	}

	return FuzzyPatchResult{
		Applied: true,
		Matches: count,
		Preview: replaced,
	}
}

// normalizeWhitespace collapses runs of whitespace to single spaces and trims lines.
func normalizeWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Collapse internal whitespace.
		fields := strings.Fields(trimmed)
		out = append(out, strings.Join(fields, " "))
	}
	return strings.Join(out, "\n")
}

// fuzzyReplace performs replacement by matching normalized content line-by-line.
func fuzzyReplace(content, oldText, newText string, all bool) string {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(strings.TrimSpace(oldText), "\n")
	newLines := strings.Split(newText, "\n")

	if len(oldLines) == 0 {
		return content
	}

	normalizedOldLines := make([]string, len(oldLines))
	for i, l := range oldLines {
		normalizedOldLines[i] = strings.Join(strings.Fields(strings.TrimSpace(l)), " ")
	}

	var result []string
	replaced := false
	i := 0
	for i < len(contentLines) {
		if (!replaced || all) && i+len(normalizedOldLines) <= len(contentLines) {
			match := true
			for j, normOld := range normalizedOldLines {
				normContent := strings.Join(strings.Fields(strings.TrimSpace(contentLines[i+j])), " ")
				if normContent != normOld {
					match = false
					break
				}
			}
			if match {
				// Preserve indentation from first matched line.
				indent := leadingWhitespace(contentLines[i])
				for _, nl := range newLines {
					if strings.TrimSpace(nl) == "" {
						result = append(result, "")
					} else {
						result = append(result, indent+strings.TrimSpace(nl))
					}
				}
				i += len(normalizedOldLines)
				replaced = true
				continue
			}
		}
		result = append(result, contentLines[i])
		i++
	}

	return strings.Join(result, "\n")
}

// anchorMatch uses the first and last lines as anchors for a block match.
func anchorMatch(content, oldText, newText string, all bool) FuzzyPatchResult {
	oldLines := strings.Split(strings.TrimSpace(oldText), "\n")
	if len(oldLines) < 2 {
		return FuzzyPatchResult{}
	}

	firstNorm := strings.Join(strings.Fields(strings.TrimSpace(oldLines[0])), " ")
	lastNorm := strings.Join(strings.Fields(strings.TrimSpace(oldLines[len(oldLines)-1])), " ")

	contentLines := strings.Split(content, "\n")
	var matches []int

	for i := 0; i <= len(contentLines)-len(oldLines); i++ {
		cFirst := strings.Join(strings.Fields(strings.TrimSpace(contentLines[i])), " ")
		cLast := strings.Join(strings.Fields(strings.TrimSpace(contentLines[i+len(oldLines)-1])), " ")
		if cFirst == firstNorm && cLast == lastNorm {
			matches = append(matches, i)
		}
	}

	if len(matches) == 0 {
		return FuzzyPatchResult{}
	}
	if len(matches) > 1 && !all {
		return FuzzyPatchResult{
			Matches: len(matches),
			Error:   fmt.Errorf("anchor match found %d candidates", len(matches)),
		}
	}

	newLines := strings.Split(newText, "\n")
	// Apply replacements in reverse order to preserve line indices.
	result := make([]string, len(contentLines))
	copy(result, contentLines)

	limit := len(matches)
	if !all {
		limit = 1
	}
	for idx := limit - 1; idx >= 0; idx-- {
		pos := matches[idx]
		indent := leadingWhitespace(result[pos])
		var replacement []string
		for _, nl := range newLines {
			if strings.TrimSpace(nl) == "" {
				replacement = append(replacement, "")
			} else {
				replacement = append(replacement, indent+strings.TrimSpace(nl))
			}
		}
		result = append(result[:pos], append(replacement, result[pos+len(oldLines):]...)...)
	}

	return FuzzyPatchResult{
		Applied: true,
		Matches: limit,
		Preview: strings.Join(result, "\n"),
	}
}

func leadingWhitespace(s string) string {
	trimmed := strings.TrimLeft(s, " \t")
	return s[:len(s)-len(trimmed)]
}

func extractContext(content, needle string, maxLen int) string {
	idx := strings.Index(content, needle)
	if idx < 0 {
		return extractPreview(content, maxLen)
	}
	start := idx - 50
	if start < 0 {
		start = 0
	}
	end := idx + len(needle) + 50
	if end > len(content) {
		end = len(content)
	}
	preview := content[start:end]
	if len(preview) > maxLen {
		preview = preview[:maxLen]
	}
	return preview
}

func extractPreview(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen-3] + "..."
}
