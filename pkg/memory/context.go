package memory

import (
	"fmt"
	"strings"
)

// BuildContext reads the memory index and returns a formatted context string
// suitable for system prompt injection. It respects the maxTokens budget
// by truncating at the approximate character equivalent.
func (s *Store) BuildContext(maxTokens int) (string, error) {
	index, err := s.LoadIndex()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(index) == "" {
		return "", nil
	}

	if maxTokens > 0 {
		charBudget := maxTokens * 4
		if len(index) > charBudget {
			index = index[:charBudget]
		}
	}

	return fmt.Sprintf("## Session Memory\n\n%s", strings.TrimSpace(index)), nil
}
