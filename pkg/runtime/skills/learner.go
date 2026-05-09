package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// SkillRefiner optionally refines learned skill content using a model.
// It receives the raw learning input and returns refined SKILL.md content.
// If it returns an error, the caller falls back to template-based generation.
type SkillRefiner interface {
	Refine(ctx context.Context, name string, input LearningInput, existing string) (string, error)
}

// Learner extracts reusable skills from completed agent tasks and saves them
// as SKILL.md files for future use.
type Learner struct {
	skillsDir string
	registry  *Registry
	refiner   SkillRefiner
	guard     *SkillGuard
	minTurns  int
	minTools  int
	mu        sync.Mutex
}

// LearnedSkillInfo describes a learned skill on disk.
type LearnedSkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

// LearningInput captures the context of a completed agent task.
type LearningInput struct {
	SessionID string
	Prompt    string
	Output    string
	ToolCalls []ToolCallSummary
	TurnCount int
	Success   bool
}

// ToolCallSummary is a brief record of a tool invocation.
type ToolCallSummary struct {
	Name   string
	Params string
}

// NewLearner creates a skill learner that writes to the given directory.
func NewLearner(skillsDir string, registry *Registry) *Learner {
	return &Learner{
		skillsDir: skillsDir,
		registry:  registry,
		guard:     NewSkillGuard(),
		minTurns:  3,
		minTools:  2,
	}
}

// SetRefiner attaches an optional LLM-based refiner for skill extraction.
func (l *Learner) SetRefiner(r SkillRefiner) {
	if l != nil {
		l.mu.Lock()
		l.refiner = r
		l.mu.Unlock()
	}
}

// ShouldLearn decides whether a completed task is worth extracting as a skill.
// Returns true for new skills and for updating existing learned skills.
func (l *Learner) ShouldLearn(input LearningInput) bool {
	if !input.Success {
		return false
	}
	if input.TurnCount < l.minTurns {
		return false
	}
	if len(input.ToolCalls) < l.minTools {
		return false
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return false
	}
	name := skillName(input.Prompt)
	if l.registry != nil {
		if sk, ok := l.registry.Get(name); ok {
			// Allow update only if the existing skill is a learned one.
			scope := sk.definition.Metadata[MetadataKeySkillScope]
			return scope == string(SkillScopeLearned)
		}
	}
	return true
}

// isUpdate checks if a learned skill with this name already exists.
func (l *Learner) isUpdate(name string) bool {
	if l.registry == nil {
		return false
	}
	sk, ok := l.registry.Get(name)
	if !ok {
		return false
	}
	return sk.definition.Metadata[MetadataKeySkillScope] == string(SkillScopeLearned)
}

// Learn extracts a skill from a completed task and saves it as SKILL.md.
func (l *Learner) Learn(input LearningInput) error {
	if !l.ShouldLearn(input) {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	name := skillName(input.Prompt)
	dir := filepath.Join(l.skillsDir, name)
	skillPath := filepath.Join(dir, "SKILL.md")

	// Read existing content for iterative updates.
	var existing string
	if data, err := os.ReadFile(skillPath); err == nil {
		existing = string(data)
	}

	// Try LLM refinement first.
	var content string
	if l.refiner != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		refined, err := l.refiner.Refine(ctx, name, input, existing)
		cancel()
		if err == nil && strings.TrimSpace(refined) != "" {
			content = refined
		}
	}

	// Fallback to template-based generation.
	if content == "" {
		if existing != "" {
			content = appendToSkillMD(existing, input)
		} else {
			content = buildSkillMD(name, input)
		}
	}

	// Security scan before writing.
	if l.guard != nil {
		result := l.guard.Scan(content)
		if !result.IsSafe() {
			return fmt.Errorf("skills learner: security scan blocked: %s", result.Summary())
		}
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("skills learner: mkdir %s: %w", dir, err)
	}

	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("skills learner: write %s: %w", skillPath, err)
	}
	return nil
}

// Remove deletes a learned skill from disk.
func (l *Learner) Remove(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("skills learner: name is required")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	dir := filepath.Join(l.skillsDir, name)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("skills learner: skill %q not found", name)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("skills learner: remove %s: %w", dir, err)
	}
	return nil
}

// Promote moves a learned skill from learned-skills/ to skills/ (sibling directory).
func (l *Learner) Promote(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("skills learner: name is required")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	srcDir := filepath.Join(l.skillsDir, name)
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return fmt.Errorf("skills learner: skill %q not found", name)
	}

	// Security scan before promoting.
	skillPath := filepath.Join(srcDir, "SKILL.md")
	if l.guard != nil {
		if data, err := os.ReadFile(skillPath); err == nil {
			result := l.guard.Scan(string(data))
			if !result.IsSafe() {
				return fmt.Errorf("skills learner: promote blocked by security scan: %s", result.Summary())
			}
		}
	}

	// Target: sibling "skills" directory.
	dstDir := filepath.Join(filepath.Dir(l.skillsDir), "skills", name)
	if _, err := os.Stat(dstDir); err == nil {
		return fmt.Errorf("skills learner: skill %q already exists in skills/", name)
	}

	if err := os.MkdirAll(filepath.Dir(dstDir), 0755); err != nil {
		return fmt.Errorf("skills learner: mkdir %s: %w", filepath.Dir(dstDir), err)
	}

	if err := os.Rename(srcDir, dstDir); err != nil {
		return fmt.Errorf("skills learner: promote %s: %w", name, err)
	}

	// Remove learned: true from the promoted SKILL.md.
	skillPath = filepath.Join(dstDir, "SKILL.md")
	if data, err := os.ReadFile(skillPath); err == nil {
		cleaned := removeFrontmatterField(string(data), "learned")
		_ = os.WriteFile(skillPath, []byte(cleaned), 0644)
	}

	return nil
}

// List returns metadata for all learned skills on disk.
func (l *Learner) List() []LearnedSkillInfo {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries, err := os.ReadDir(l.skillsDir)
	if err != nil {
		return nil
	}

	var out []LearnedSkillInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(l.skillsDir, entry.Name(), "SKILL.md")
		meta, err := readFrontMatter(skillPath, nil)
		if err != nil {
			continue
		}
		out = append(out, LearnedSkillInfo{
			Name:        meta.Name,
			Description: strings.TrimSpace(meta.Description),
			Path:        skillPath,
		})
	}
	return out
}

// SkillsDir returns the learned skills directory path.
func (l *Learner) SkillsDir() string {
	if l == nil {
		return ""
	}
	return l.skillsDir
}

// buildSkillMD generates the SKILL.md content with YAML frontmatter.
func buildSkillMD(name string, input LearningInput) string {
	desc := truncateStr(input.Prompt, 80)
	tools := extractToolNames(input.ToolCalls)
	keywords := extractKeywords(input.Prompt)

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", name))
	sb.WriteString(fmt.Sprintf("description: %s\n", desc))
	sb.WriteString("learned: true\n")
	if len(keywords) > 0 {
		sb.WriteString("keywords:\n")
		for _, kw := range keywords {
			sb.WriteString(fmt.Sprintf("  - %s\n", kw))
		}
	}
	if len(tools) > 0 {
		sb.WriteString("allowed-tools:\n")
		for _, t := range tools {
			sb.WriteString(fmt.Sprintf("  - %s\n", t))
		}
	}
	sb.WriteString("---\n\n")

	// Body: summarize the task and steps.
	sb.WriteString(fmt.Sprintf("# %s\n\n", desc))
	sb.WriteString("## Steps\n\n")
	for i, tc := range input.ToolCalls {
		params := tc.Params
		if len(params) > 60 {
			params = params[:57] + "..."
		}
		sb.WriteString(fmt.Sprintf("%d. `%s`", i+1, tc.Name))
		if params != "" {
			sb.WriteString(fmt.Sprintf(" — %s", params))
		}
		sb.WriteString("\n")
	}

	if output := strings.TrimSpace(input.Output); output != "" {
		sb.WriteString("\n## Result\n\n")
		if len(output) > 500 {
			output = output[:497] + "..."
		}
		sb.WriteString(output)
		sb.WriteString("\n")
	}

	return sb.String()
}

// appendToSkillMD appends new experience to an existing SKILL.md.
func appendToSkillMD(existing string, input LearningInput) string {
	var sb strings.Builder
	sb.WriteString(strings.TrimRight(existing, "\n"))
	sb.WriteString("\n\n## Update\n\n")
	for i, tc := range input.ToolCalls {
		params := tc.Params
		if len(params) > 60 {
			params = params[:57] + "..."
		}
		sb.WriteString(fmt.Sprintf("%d. `%s`", i+1, tc.Name))
		if params != "" {
			sb.WriteString(fmt.Sprintf(" — %s", params))
		}
		sb.WriteString("\n")
	}
	if output := strings.TrimSpace(input.Output); output != "" {
		sb.WriteString("\n")
		if len(output) > 300 {
			output = output[:297] + "..."
		}
		sb.WriteString(output)
		sb.WriteString("\n")
	}
	return sb.String()
}

// skillName derives a slug from the user prompt.
func skillName(prompt string) string {
	s := strings.ToLower(strings.TrimSpace(prompt))
	var sb strings.Builder
	for _, r := range s {
		if sb.Len() >= 40 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			if sb.Len() > 0 {
				sb.WriteByte('-')
			}
		case r >= 0x4e00 && r <= 0x9fff: // CJK
			sb.WriteRune(r)
		}
	}
	result := strings.TrimRight(sb.String(), "-")
	if result == "" {
		result = "learned-skill"
	}
	return result
}

// extractToolNames returns deduplicated tool names preserving order.
func extractToolNames(calls []ToolCallSummary) []string {
	seen := map[string]bool{}
	var out []string
	for _, tc := range calls {
		name := strings.TrimSpace(tc.Name)
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// extractKeywords extracts meaningful keywords from a prompt for auto-activation.
// Simple heuristic: split on whitespace, filter stop words and short tokens.
func extractKeywords(prompt string) []string {
	words := strings.Fields(strings.ToLower(prompt))
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		// Strip punctuation.
		w = strings.TrimFunc(w, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if len(w) < 3 || stopWords[w] {
			continue
		}
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
		if len(out) >= 8 {
			break
		}
	}
	return out
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "are": true, "was": true, "were": true,
	"been": true, "have": true, "has": true, "had": true, "not": true,
	"but": true, "can": true, "all": true, "will": true, "would": true,
	"could": true, "should": true, "into": true, "about": true,
	"than": true, "then": true, "them": true, "these": true, "those": true,
	"some": true, "such": true, "each": true, "which": true, "their": true,
	"there": true, "where": true, "when": true, "what": true, "how": true,
	"please": true, "help": true, "using": true, "use": true,
}

func truncateStr(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// Patch applies a targeted find-and-replace to a learned skill's SKILL.md.
func (l *Learner) Patch(name, oldText, newText string, replaceAll bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("skills learner: name is required")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	skillPath := filepath.Join(l.skillsDir, name, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return fmt.Errorf("skills learner: read %s: %w", skillPath, err)
	}

	result := FuzzyPatch(string(data), oldText, newText, replaceAll)
	if result.Error != nil {
		return fmt.Errorf("skills learner: patch %s: %w", name, result.Error)
	}
	if !result.Applied {
		return fmt.Errorf("skills learner: patch %s: no changes applied", name)
	}

	// Security scan the patched content.
	if l.guard != nil {
		scan := l.guard.Scan(result.Preview)
		if !scan.IsSafe() {
			return fmt.Errorf("skills learner: patch blocked by security scan: %s", scan.Summary())
		}
	}

	return os.WriteFile(skillPath, []byte(result.Preview), 0644)
}

// removeFrontmatterField removes a specific field from YAML frontmatter.
func removeFrontmatterField(content, field string) string {
	lines := strings.Split(content, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return content
	}
	var out []string
	out = append(out, lines[0])
	inFrontmatter := true
	prefix := field + ":"
	for i := 1; i < len(lines); i++ {
		if inFrontmatter && strings.TrimSpace(lines[i]) == "---" {
			inFrontmatter = false
			out = append(out, lines[i])
			continue
		}
		if inFrontmatter && strings.HasPrefix(strings.TrimSpace(lines[i]), prefix) {
			continue // skip this field
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n")
}
