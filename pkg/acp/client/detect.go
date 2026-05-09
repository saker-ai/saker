package client

import (
	"os/exec"
)

// KnownAgent describes a well-known ACP-capable agent CLI.
type KnownAgent struct {
	Name   string // target name used in subagent_type (e.g. "claude-code")
	Binary string // executable name on PATH
	Args   []string
	Desc   string // short description for tool listing
}

// knownAgents is the built-in catalog of ACP-capable CLIs.
var knownAgents = []KnownAgent{
	{Name: "claude-code", Binary: "claude", Args: []string{"--acp"}, Desc: "Claude Code agent (Anthropic)"},
	{Name: "codex", Binary: "codex", Args: []string{"--acp"}, Desc: "Codex agent (OpenAI)"},
	{Name: "gemini-cli", Binary: "gemini", Args: []string{"--acp"}, Desc: "Gemini CLI agent (Google)"},
	{Name: "amp", Binary: "amp", Args: []string{"--acp"}, Desc: "Amp agent"},
}

// DetectedAgent is an agent found on the system PATH.
type DetectedAgent struct {
	KnownAgent
	Path string // resolved binary path
}

// DetectAgents scans PATH for known ACP-capable agent CLIs.
func DetectAgents() []DetectedAgent {
	var found []DetectedAgent
	for _, ka := range knownAgents {
		path, err := exec.LookPath(ka.Binary)
		if err != nil {
			continue
		}
		found = append(found, DetectedAgent{
			KnownAgent: ka,
			Path:       path,
		})
	}
	return found
}
