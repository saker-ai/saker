package api

import (
	"path/filepath"
	"strings"

	"github.com/saker-ai/saker/pkg/message"
)

// resolveConfigBase returns the .saker config directory path, or "" if disabled.
func resolveConfigBase(projectRoot, configRoot string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	configRoot = strings.TrimSpace(configRoot)
	if projectRoot == "" && configRoot == "" {
		return ""
	}
	base := configRoot
	if base == "" {
		base = filepath.Join(projectRoot, ".saker")
	} else if !filepath.IsAbs(base) && projectRoot != "" {
		base = filepath.Join(projectRoot, base)
	}
	return base
}

func (rt *Runtime) persistHistory(sessionID string, history *message.History) {
	if rt == nil || history == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	snapshot := history.All()
	if len(snapshot) == 0 {
		return
	}
	rt.persistToConversation(sessionID, history)
}
