package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/saker-ai/saker/pkg/runtime/skills"
	"github.com/saker-ai/saker/pkg/tool"
)

const skillToolDescriptionHeader = `Execute a skill within the main conversation

<skills_instructions>
When users ask you to perform tasks, check if any of the available skills match. Skills provide specialized capabilities and domain knowledge.

When users reference a "slash command" or "/<something>" (e.g., "/commit", "/review-pr"), they are referring to a skill. Use this tool to invoke it.

How to invoke:
- Use this tool with the skill name and optional arguments
- Examples:
  - skill: "pdf" - invoke the pdf skill
  - skill: "commit", args: "-m 'Fix bug'" - invoke with arguments
  - skill: "review-pr", args: "123" - invoke with arguments

Important:
- Available skills are listed in <available_skills> below
- When a skill matches the user's request, invoke it BEFORE generating any other response
- Do not invoke a skill that is already running
- Do not use this tool for built-in CLI commands (like /help, /clear, etc.)
</skills_instructions>

<available_skills>
`

var skillSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"skill": map[string]interface{}{
			"type":        "string",
			"description": "The skill name. E.g., \"pdf\", \"commit\", or \"review-pr\"",
		},
		"args": map[string]interface{}{
			"type":        "string",
			"description": "Optional arguments for the skill",
		},
	},
	Required: []string{"skill"},
}

// ActivationContextProvider resolves the activation context for manual skill calls.
type ActivationContextProvider func(context.Context) skills.ActivationContext

// SkillTool adapts the runtime skills registry into a tool.
type SkillTool struct {
	registry            *skills.Registry
	provider            ActivationContextProvider
	contextWindowTokens int // model context window size; 0 uses default budget
}

// NewSkillTool wires the registry with an optional activation provider.
func NewSkillTool(reg *skills.Registry, provider ActivationContextProvider) *SkillTool {
	if provider == nil {
		provider = defaultActivationProvider
	}
	return &SkillTool{registry: reg, provider: provider}
}

// SetContextWindow sets the model context window size (in tokens) for budget calculation.
func (s *SkillTool) SetContextWindow(tokens int) {
	if s != nil {
		s.contextWindowTokens = tokens
	}
}

func (s *SkillTool) Name() string { return "skill" }

func (s *SkillTool) Description() string {
	var defs []skills.Definition
	var ctxTokens int
	if s != nil {
		ctxTokens = s.contextWindowTokens
		if s.registry != nil {
			defs = s.registry.List()
		}
	}
	return buildSkillDescription(defs, ctxTokens)
}

func (s *SkillTool) Schema() *tool.JSONSchema { return skillSchema }

// Budget constants matching Claude Code's approach.
// Skill listing gets 1% of the context window (in characters).
const (
	skillBudgetContextPercent = 0.01
	charsPerToken             = 4
	defaultCharBudget         = 8_000 // 1% of 200k × 4
	maxListingDescChars       = 250   // per-entry hard cap
	minDescLength             = 20    // below this, switch to names-only
)

func getCharBudget(contextWindowTokens int) int {
	if contextWindowTokens > 0 {
		return int(float64(contextWindowTokens) * float64(charsPerToken) * skillBudgetContextPercent)
	}
	return defaultCharBudget
}

func buildSkillDescription(defs []skills.Definition, contextWindowTokens int) string {
	// Filter to user-invocable skills only.
	filtered := make([]skills.Definition, 0, len(defs))
	for _, def := range defs {
		if def.UserInvocable {
			filtered = append(filtered, def)
		}
	}

	var b strings.Builder
	b.WriteString(skillToolDescriptionHeader)
	if len(filtered) == 0 {
		b.WriteString("</available_skills>\n")
		return b.String()
	}
	b.WriteString(formatSkillsWithinBudget(filtered, contextWindowTokens))
	b.WriteString("</available_skills>\n")
	return b.String()
}

// formatSkillsWithinBudget implements a three-tier degradation strategy:
//  1. Full descriptions (capped at maxListingDescChars per entry)
//  2. Proportionally truncated descriptions
//  3. Names only (extreme case)
func formatSkillsWithinBudget(defs []skills.Definition, contextWindowTokens int) string {
	budget := getCharBudget(contextWindowTokens)

	// Build full entries with per-entry capped descriptions.
	type entry struct {
		name string
		desc string
		full string // rendered XML
	}
	entries := make([]entry, len(defs))
	fullTotal := 0
	for i, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			name = "unknown"
		}
		desc := compactDescription(strings.TrimSpace(def.Description), maxListingDescChars)
		if desc == "" {
			desc = "No description provided."
		}
		// Append when_to_use and argument_hint for richer context.
		if wtu := strings.TrimSpace(def.WhenToUse); wtu != "" {
			desc += " | When: " + compactDescription(wtu, 120)
		}
		if hint := strings.TrimSpace(def.ArgumentHint); hint != "" {
			desc += " | Args: " + hint
		}
		full := formatSkillEntry(name, desc)
		entries[i] = entry{name: name, desc: desc, full: full}
		fullTotal += len(full)
	}

	// Tier 1: full descriptions fit within budget.
	if fullTotal <= budget {
		var b strings.Builder
		for _, e := range entries {
			b.WriteString(e.full)
		}
		return b.String()
	}

	// Compute per-entry overhead (XML tags around name) to find available space for descriptions.
	// Each entry is: <skill>\n<name>NAME</name>\n<description>DESC</description>\n</skill>\n
	// Overhead per entry ≈ 56 chars + len(name)
	const xmlOverheadPerEntry = 56 // tags, newlines
	nameOverhead := 0
	for _, e := range entries {
		nameOverhead += xmlOverheadPerEntry + len(e.name)
	}
	availableForDescs := budget - nameOverhead
	if len(entries) == 0 {
		return ""
	}
	maxDescLen := availableForDescs / len(entries)

	// Tier 3: names only (extreme case — not enough room for any descriptions).
	if maxDescLen < minDescLength {
		var b strings.Builder
		for _, e := range entries {
			formatSkillNameOnly(&b, e.name)
		}
		return b.String()
	}

	// Tier 2: proportionally truncated descriptions.
	var b strings.Builder
	for _, e := range entries {
		desc := e.desc
		if len(desc) > maxDescLen {
			desc = desc[:maxDescLen-1] + "…"
		}
		b.WriteString(formatSkillEntry(e.name, desc))
	}
	return b.String()
}

func formatSkillEntry(name, description string) string {
	return fmt.Sprintf("<skill>\n<name>%s</name>\n<description>%s</description>\n</skill>\n",
		escapeXML(name), escapeXML(description))
}

func formatSkillNameOnly(b *strings.Builder, name string) {
	fmt.Fprintf(b, "<skill>\n<name>%s</name>\n</skill>\n", escapeXML(name))
}

// compactDescription collapses a multi-line description into a single line,
// keeping up to 3 meaningful lines, capped at maxLen characters.
func compactDescription(desc string, maxLen int) string {
	if desc == "" {
		return desc
	}
	lines := strings.SplitN(desc, "\n", 4) // keep up to 3 lines
	kept := make([]string, 0, 3)
	for _, line := range lines[:min(len(lines), 3)] {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	result := strings.Join(kept, " ")
	if len(result) > maxLen {
		return result[:maxLen-1] + "…"
	}
	return result
}

func (s *SkillTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if s == nil || s.registry == nil {
		return nil, errors.New("skill registry is not initialised")
	}
	name, err := parseSkillName(params)
	if err != nil {
		return nil, err
	}

	// Resolve the skill to access its definition.
	sk, ok := s.registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a registered skill. The Skill meta-tool only dispatches user-defined skills (.saker/skills/*.md). If you meant to call a built-in tool such as canvas_table_write, file_write, or bash, invoke it directly as a top-level tool — do NOT wrap it in Skill(skill=...). %s", skills.ErrUnknownSkill, name, s.availableSkillsHint())
	}
	def := sk.Definition()

	// Build activation context with arguments.
	act := s.provider(ctx)
	if args, _ := params["args"].(string); args != "" {
		act.Prompt = args
	}

	result, err := sk.Execute(ctx, act)
	if err != nil {
		return nil, err
	}
	output := formatSkillOutput(result)
	data := map[string]interface{}{
		"skill":    result.Skill,
		"output":   result.Output,
		"metadata": result.Metadata,
	}
	// Propagate skill definition metadata for runtime use (model override, allowed-tools, fork).
	if def.Model != "" {
		data["model"] = def.Model
	}
	if len(def.AllowedTools) > 0 {
		data["allowed_tools"] = def.AllowedTools
	}
	if def.ExecutionContext != "" && def.ExecutionContext != "inline" {
		data["execution_context"] = def.ExecutionContext
	}
	return &tool.ToolResult{
		Success: true,
		Output:  output,
		Data:    data,
	}, nil
}

// availableSkillsHint returns a short suffix listing registered skill names so
// the model can self-correct after calling Skill with a wrong name. Returns "" when
// no skills are registered.
func (s *SkillTool) availableSkillsHint() string {
	if s == nil || s.registry == nil {
		return ""
	}
	defs := s.registry.List()
	if len(defs) == 0 {
		return "No user-defined skills are registered in this project."
	}
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		if d.Name != "" {
			names = append(names, d.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	const maxNames = 12
	if len(names) > maxNames {
		return fmt.Sprintf("Registered skills (%d total, first %d): %s.", len(names), maxNames, strings.Join(names[:maxNames], ", "))
	}
	return fmt.Sprintf("Registered skills: %s.", strings.Join(names, ", "))
}

func parseSkillName(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	// Prefer "skill" param; fall back to "command" for backward compatibility.
	raw, ok := params["skill"]
	if !ok {
		raw, ok = params["command"]
	}
	if !ok {
		return "", errors.New("skill is required")
	}
	name, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("skill must be string: %w", err)
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", errors.New("skill cannot be empty")
	}
	return name, nil
}

func formatSkillOutput(result skills.Result) string {
	switch v := result.Output.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			return v
		}
	case fmt.Stringer:
		if text := strings.TrimSpace(v.String()); text != "" {
			return text
		}
	case nil:
	default:
		if data, err := json.Marshal(v); err == nil {
			text := strings.TrimSpace(string(data))
			if text != "" && text != "null" {
				return text
			}
		}
	}
	if result.Skill == "" {
		return "skill executed"
	}
	return fmt.Sprintf("skill %s executed", result.Skill)
}

type activationContextKey struct{}

// WithSkillActivationContext attaches a skills.ActivationContext to the context.
func WithSkillActivationContext(ctx context.Context, ac skills.ActivationContext) context.Context {
	return context.WithValue(ctx, activationContextKey{}, ac.Clone())
}

// SkillActivationContextFromContext extracts an activation context if present.
func SkillActivationContextFromContext(ctx context.Context) (skills.ActivationContext, bool) {
	if ctx == nil {
		return skills.ActivationContext{}, false
	}
	ac, ok := ctx.Value(activationContextKey{}).(skills.ActivationContext)
	if !ok {
		return skills.ActivationContext{}, false
	}
	return ac, true
}

func defaultActivationProvider(ctx context.Context) skills.ActivationContext {
	if ac, ok := SkillActivationContextFromContext(ctx); ok {
		return ac
	}
	return skills.ActivationContext{}
}

func skillLocation(def skills.Definition) string {
	if len(def.Metadata) == 0 {
		return ""
	}
	for _, key := range []string{"location", "source", "origin"} {
		if value := strings.TrimSpace(def.Metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

var skillDescriptionEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func escapeXML(value string) string {
	if value == "" {
		return ""
	}
	return skillDescriptionEscaper.Replace(value)
}
