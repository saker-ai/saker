package toolbuiltin

import (
	"strings"
	"unicode"
)

// fuzzyMatch attempts to find old in content using progressively looser
// matching strategies. It returns the actual substring in content that
// matched, or "" if no strategy succeeds. When replaceAll is true and
// multiple matches are found, it returns all matches.
//
// Strategies (tried in order):
//  1. Exact match (caller already checked — skip here)
//  2. Whitespace-normalised: collapse runs of whitespace to single space
//  3. Leading/trailing trim per line
//  4. Indent-normalised: strip common leading indent
//  5. CR/LF normalised: unify line endings
//  6. Blank-line collapsed: remove consecutive blank lines
//  7. Trailing-whitespace stripped per line
//  8. Combined: all normalisations at once
func fuzzyMatch(content, old string) (matched string, ok bool) {
	strategies := []struct {
		name string
		norm func(string) string
	}{
		{"whitespace-normalised", normaliseWhitespace},
		{"trim-lines", trimLines},
		{"indent-normalised", normaliseIndent},
		{"line-ending", normaliseLineEndings},
		{"blank-line-collapse", collapseBlankLines},
		{"trailing-ws", stripTrailingWhitespace},
		{"combined", combinedNormalise},
	}

	for _, s := range strategies {
		if match := findNormalisedMatch(content, old, s.norm); match != "" {
			return match, true
		}
	}
	return "", false
}

// findNormalisedMatch normalises both content and old, finds old in the
// normalised content, then maps the position back to the original content.
func findNormalisedMatch(content, old string, norm func(string) string) string {
	normContent := norm(content)
	normOld := norm(old)
	if normOld == "" {
		return ""
	}

	idx := strings.Index(normContent, normOld)
	if idx < 0 {
		return ""
	}

	// Map normalised position back to original content using line-based
	// alignment. Split both into lines and find the matching line range.
	origLines := strings.Split(content, "\n")
	normLines := strings.Split(normContent, "\n")
	oldNormLines := strings.Split(normOld, "\n")

	if len(oldNormLines) == 0 {
		return ""
	}

	// Find start line in normalised content.
	startLine := -1
	for i := 0; i < len(normLines); i++ {
		if strings.Contains(normLines[i], oldNormLines[0]) ||
			normLines[i] == oldNormLines[0] {
			// Verify consecutive lines match.
			if i+len(oldNormLines) <= len(normLines) {
				allMatch := true
				for j := 0; j < len(oldNormLines); j++ {
					if normLines[i+j] != oldNormLines[j] {
						allMatch = false
						break
					}
				}
				if allMatch {
					startLine = i
					break
				}
			}
		}
	}

	if startLine < 0 || startLine+len(oldNormLines) > len(origLines) {
		// Fallback: try simple character-position mapping.
		return fallbackExtract(content, normContent, normOld, idx)
	}

	endLine := startLine + len(oldNormLines)
	return strings.Join(origLines[startLine:endLine], "\n")
}

// fallbackExtract tries to extract the original substring by mapping
// character positions through a simple offset approach.
func fallbackExtract(content, normContent, normOld string, normIdx int) string {
	// Count non-whitespace characters before normIdx to find approximate
	// position in original content.
	targetNonWS := countNonWhitespace(normContent[:normIdx])
	origStart := findNonWSPosition(content, targetNonWS)

	targetEndNonWS := countNonWhitespace(normContent[:normIdx+len(normOld)])
	origEnd := findNonWSPosition(content, targetEndNonWS)

	if origStart < 0 || origEnd < 0 || origEnd > len(content) {
		return ""
	}

	// Extend to line boundaries for cleaner results.
	for origStart > 0 && content[origStart-1] != '\n' {
		origStart--
	}
	for origEnd < len(content) && content[origEnd] != '\n' {
		origEnd++
	}

	candidate := content[origStart:origEnd]
	if candidate == "" {
		return ""
	}
	return candidate
}

func countNonWhitespace(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

func findNonWSPosition(s string, target int) int {
	n := 0
	for i, r := range s {
		if !unicode.IsSpace(r) {
			if n == target {
				return i
			}
			n++
		}
	}
	return len(s)
}

// --- Normalisation functions ---

func normaliseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) && r != '\n' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
		} else {
			b.WriteRune(r)
			inSpace = false
		}
	}
	return b.String()
}

func trimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

func normaliseIndent(s string) string {
	lines := strings.Split(s, "\n")
	// Find minimum indent (ignoring empty lines).
	minIndent := -1
	for _, line := range lines {
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(trimmed)
		if minIndent < 0 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		return s
	}
	for i, line := range lines {
		if len(line) >= minIndent {
			lines[i] = line[minIndent:]
		}
	}
	return strings.Join(lines, "\n")
}

func normaliseLineEndings(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	prevBlank := false
	for _, line := range lines {
		blank := strings.TrimSpace(line) == ""
		if blank && prevBlank {
			continue
		}
		result = append(result, line)
		prevBlank = blank
	}
	return strings.Join(result, "\n")
}

func stripTrailingWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	return strings.Join(lines, "\n")
}

func combinedNormalise(s string) string {
	s = normaliseLineEndings(s)
	s = stripTrailingWhitespace(s)
	s = collapseBlankLines(s)
	s = normaliseIndent(s)
	return s
}
