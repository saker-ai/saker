package tui

import (
	"fmt"
	"time"

	"github.com/saker-ai/saker/pkg/middleware"
)

// friendlyError transforms raw error text into a user-friendly message
// using the error classifier's recovery hints.
func (a *App) friendlyError(raw string) string {
	classified := middleware.ClassifyErrorString(raw)
	if classified == nil || classified.Category == middleware.ErrorCategoryUnknown {
		return raw
	}

	switch classified.Category {
	case middleware.ErrorCategoryTimeout:
		label := "Request timed out"
		if a.cfg.TimeoutMs > 0 {
			dur := time.Duration(a.cfg.TimeoutMs) * time.Millisecond
			label = fmt.Sprintf("Request timed out (%s limit)", formatDuration(dur))
		}
		return fmt.Sprintf("%s. Try increasing --timeout-ms or simplifying your request.", label)

	case middleware.ErrorCategoryAuth:
		return fmt.Sprintf("Authentication failed. %s", classified.Recovery)

	case middleware.ErrorCategoryRateLimit:
		return "Rate limited — please wait a moment and try again."

	case middleware.ErrorCategoryNetwork:
		return fmt.Sprintf("Network error. %s", classified.Recovery)

	case middleware.ErrorCategorySandbox:
		return fmt.Sprintf("Sandbox restriction: %s\n  ↳ %s", raw, classified.Recovery)

	default:
		if classified.Recovery != "" {
			return fmt.Sprintf("%s\n  ↳ %s", raw, classified.Recovery)
		}
		return raw
	}
}
