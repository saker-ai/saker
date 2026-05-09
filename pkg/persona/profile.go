// Package persona implements multi-persona identity and routing for the agent runtime.
// A persona defines voice, tools, model overrides, and routing rules that are applied
// as a request-time overlay on the shared Runtime — no per-persona Runtime instances.
package persona

import (
	"os"
	"path/filepath"
	"strings"
)

// Profile defines a complete persona identity and configuration overlay.
type Profile struct {
	// Identity
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Emoji       string `json:"emoji,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	Creature    string `json:"creature,omitempty"`
	Vibe        string `json:"vibe,omitempty"`
	Theme       string `json:"theme,omitempty"`

	// Soul (system prompt layer)
	Soul         string `json:"soul,omitempty"`         // inline soul text
	SoulFile     string `json:"soulFile,omitempty"`     // path to SOUL.md
	Instructions string `json:"instructions,omitempty"` // inline instructions
	InstructFile string `json:"instructFile,omitempty"` // path to AGENTS.md

	// Model overrides
	Model         string   `json:"model,omitempty"`
	ThinkingLevel string   `json:"thinkingLevel,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Temperature   *float64 `json:"temperature,omitempty"`

	// Tool policy
	EnabledTools    []string `json:"enabledTools,omitempty"`
	DisallowedTools []string `json:"disallowedTools,omitempty"`
	MCPServers      []string `json:"mcpServers,omitempty"`

	// Skills
	EnabledSkills  []string `json:"enabledSkills,omitempty"`
	DisabledSkills []string `json:"disabledSkills,omitempty"`

	// Sandbox override
	Sandbox *SandboxOverride `json:"sandbox,omitempty"`

	// Language
	Language string `json:"language,omitempty"`

	// Inheritance
	Inherit string `json:"inherit,omitempty"`

	// Channel bindings (for routing)
	Channels []ChannelBinding `json:"channels,omitempty"`
}

// SandboxOverride allows a persona to customize sandbox settings.
type SandboxOverride struct {
	Enabled      *bool    `json:"enabled,omitempty"`
	NetworkAllow []string `json:"networkAllow,omitempty"`
}

// ChannelBinding maps a channel pattern to a persona.
type ChannelBinding struct {
	Channel   string `json:"channel"`            // glob pattern
	Peer      string `json:"peer,omitempty"`     // user/group filter
	PersonaID string `json:"persona"`            // target persona ID
	Priority  int    `json:"priority,omitempty"` // higher = matched first
}

// ResolvedSoul returns the soul text, reading from SoulFile if needed.
// projectRoot is used to resolve relative SoulFile paths.
func (p *Profile) ResolvedSoul(projectRoot string) string {
	if p.Soul != "" {
		return p.Soul
	}
	if p.SoulFile == "" {
		return ""
	}
	return readFileRelative(projectRoot, p.SoulFile)
}

// ResolvedInstructions returns instruction text, reading from InstructFile if needed.
func (p *Profile) ResolvedInstructions(projectRoot string) string {
	if p.Instructions != "" {
		return p.Instructions
	}
	if p.InstructFile == "" {
		return ""
	}
	return readFileRelative(projectRoot, p.InstructFile)
}

func readFileRelative(root, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	// Resolve symlinks and normalize to prevent path traversal.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	if !strings.HasPrefix(resolved, absRoot+string(filepath.Separator)) && resolved != absRoot {
		return "" // path escapes project root
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// mergeProfile copies non-zero fields from src onto dst.
func mergeProfile(dst, src *Profile) {
	if src.Name != "" {
		dst.Name = src.Name
	}
	if src.Description != "" {
		dst.Description = src.Description
	}
	if src.Emoji != "" {
		dst.Emoji = src.Emoji
	}
	if src.Avatar != "" {
		dst.Avatar = src.Avatar
	}
	if src.Creature != "" {
		dst.Creature = src.Creature
	}
	if src.Vibe != "" {
		dst.Vibe = src.Vibe
	}
	if src.Theme != "" {
		dst.Theme = src.Theme
	}
	if src.Soul != "" {
		dst.Soul = src.Soul
	}
	if src.SoulFile != "" {
		dst.SoulFile = src.SoulFile
	}
	if src.Instructions != "" {
		dst.Instructions = src.Instructions
	}
	if src.InstructFile != "" {
		dst.InstructFile = src.InstructFile
	}
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.ThinkingLevel != "" {
		dst.ThinkingLevel = src.ThinkingLevel
	}
	if src.MaxTokens != 0 {
		dst.MaxTokens = src.MaxTokens
	}
	if src.Temperature != nil {
		dst.Temperature = src.Temperature
	}
	if len(src.EnabledTools) > 0 {
		dst.EnabledTools = src.EnabledTools
	}
	if len(src.DisallowedTools) > 0 {
		dst.DisallowedTools = src.DisallowedTools
	}
	if len(src.MCPServers) > 0 {
		dst.MCPServers = src.MCPServers
	}
	if len(src.EnabledSkills) > 0 {
		dst.EnabledSkills = src.EnabledSkills
	}
	if len(src.DisabledSkills) > 0 {
		dst.DisabledSkills = src.DisabledSkills
	}
	if src.Sandbox != nil {
		dst.Sandbox = src.Sandbox
	}
	if src.Language != "" {
		dst.Language = src.Language
	}
	if len(src.Channels) > 0 {
		dst.Channels = src.Channels
	}
}
