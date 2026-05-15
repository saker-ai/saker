package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/api"
)

const titleMaxInputLen = 500
const titleMaxOutputLen = 80

// generateThreadTitle uses the runtime's model to produce a short title
// from the first user/assistant exchange. It is designed to be called in
// a goroutine so it never blocks the main turn.
func generateThreadTitle(rt *api.Runtime, userMsg, assistantMsg string) (string, error) {
	if len(userMsg) > titleMaxInputLen {
		userMsg = userMsg[:titleMaxInputLen]
	}
	if len(assistantMsg) > titleMaxInputLen {
		assistantMsg = assistantMsg[:titleMaxInputLen]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prompt := fmt.Sprintf("Generate a concise 3-7 word title for this conversation. Return ONLY the title, nothing else.\n\nUser: %s\n\nAssistant: %s", userMsg, assistantMsg)

	resp, err := rt.Run(ctx, api.Request{
		Prompt:    prompt,
		SessionID: "title-gen-" + time.Now().Format("20060102150405"),
		Ephemeral: true, // don't persist throwaway title-gen sessions
	})
	if err != nil {
		return "", err
	}
	if resp.Result == nil {
		return "", nil
	}

	title := cleanTitle(resp.Result.Output)
	if title == "" {
		return "", nil
	}
	return title, nil
}

// cleanTitle strips quotes, common prefixes, and enforces length limits.
func cleanTitle(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// Remove surrounding quotes.
	if (strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"")) ||
		(strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'")) {
		s = s[1 : len(s)-1]
	}

	// Strip common prefixes the model might add.
	for _, prefix := range []string{"Title:", "title:", "Title :", "**", "## "} {
		s = strings.TrimPrefix(s, prefix)
	}
	s = strings.TrimSuffix(s, "**")
	s = strings.TrimSpace(s)

	// Take only the first line.
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}

	// Enforce max length.
	if len(s) > titleMaxOutputLen {
		s = s[:titleMaxOutputLen]
	}

	return strings.TrimSpace(s)
}
