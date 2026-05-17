package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/saker-ai/saker/pkg/middleware"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/runtime/skills"
)

type AvailableSkill struct {
	Name          string   `json:"Name"`
	Description   string   `json:"Description"`
	Scope         string   `json:"Scope,omitempty"`
	RelatedSkills []string `json:"RelatedSkills,omitempty"`
	Keywords      []string `json:"Keywords,omitempty"`
}

// SkillContentResult holds the full content of a skill for on-demand loading.
type SkillContentResult struct {
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	Scope        string              `json:"scope,omitempty"`
	Body         string              `json:"body"`
	SupportFiles map[string][]string `json:"support_files,omitempty"`
}

type ModelTurnStat struct {
	Iteration    int       `json:"iteration"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	TotalTokens  int       `json:"total_tokens"`
	StopReason   string    `json:"stop_reason"`
	Preview      string    `json:"preview"`
	Timestamp    time.Time `json:"timestamp"`
}

type ModelTurnRecorder struct {
	mu        sync.RWMutex
	bySession map[string][]ModelTurnStat
}

func NewModelTurnRecorder() *ModelTurnRecorder {
	return &ModelTurnRecorder{bySession: make(map[string][]ModelTurnStat)}
}

func (r *ModelTurnRecorder) Record(sessionID string, stat ModelTurnStat) {
	if r == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	r.mu.Lock()
	defer r.mu.Unlock()
	items := append(r.bySession[sessionID], stat)
	if len(items) > 256 {
		items = items[len(items)-256:]
	}
	r.bySession[sessionID] = items
}

func (r *ModelTurnRecorder) Count(sessionID string) int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.bySession[strings.TrimSpace(sessionID)])
}

func (r *ModelTurnRecorder) Since(sessionID string, offset int) []ModelTurnStat {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := r.bySession[strings.TrimSpace(sessionID)]
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return nil
	}
	out := make([]ModelTurnStat, len(items)-offset)
	copy(out, items[offset:])
	return out
}

func ModelTurnRecorderMiddleware(recorder *ModelTurnRecorder) middleware.Middleware {
	return middleware.Funcs{
		Identifier: "api-model-turn-recorder",
		OnAfterModel: func(_ context.Context, st *middleware.State) error {
			if st == nil || recorder == nil {
				return nil
			}
			values := st.Values
			sessionID, _ := values["session_id"].(string)
			usage, _ := values["model.usage"].(model.Usage)
			stopReason, _ := values["model.stop_reason"].(string)
			recorder.Record(sessionID, ModelTurnStat{
				Iteration:    st.Iteration,
				InputTokens:  usage.InputTokens,
				OutputTokens: usage.OutputTokens,
				TotalTokens:  usage.TotalTokens,
				StopReason:   strings.TrimSpace(stopReason),
				Preview:      modelTurnPreview(st),
				Timestamp:    time.Now().UTC(),
			})
			return nil
		},
	}
}

func modelTurnPreview(st *middleware.State) string {
	if st == nil {
		return ""
	}
	if st.Values != nil {
		if resp, ok := st.Values["model.response"].(*model.Response); ok && resp != nil {
			return strings.TrimSpace(resp.Message.TextContent())
		}
	}
	return strings.TrimSpace(modelOutputPreview(st.ModelOutput))
}

func modelOutputPreview(v any) string {
	switch typed := v.(type) {
	case nil:
		return ""
	case *model.Response:
		if typed == nil {
			return ""
		}
		return strings.TrimSpace(typed.Message.TextContent())
	case model.Response:
		return strings.TrimSpace(typed.Message.TextContent())
	case interface{ TextContent() string }:
		return strings.TrimSpace(typed.TextContent())
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	default:
		return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(toString(v)), "\n", " "), "\r", " "))
	}
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(interface{ String() string }); ok {
		return s.String()
	}
	return ""
}

func (rt *Runtime) AvailableSkills() []AvailableSkill {
	if rt == nil || rt.skReg == nil {
		return nil
	}
	defs := rt.skReg.List()
	if len(defs) == 0 {
		return nil
	}
	out := make([]AvailableSkill, 0, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		scope := ""
		if len(def.Metadata) > 0 {
			scope = def.Metadata[skills.MetadataKeySkillScope]
		}
		out = append(out, AvailableSkill{
			Name:          name,
			Description:   strings.TrimSpace(def.Description),
			Scope:         scope,
			RelatedSkills: def.RelatedSkills,
			Keywords:      extractKeywordsFromMetadata(def),
		})
	}
	return out
}

// RemoveLearnedSkill deletes a learned skill from disk and unregisters it.
func (rt *Runtime) RemoveLearnedSkill(name string) error {
	if rt == nil || rt.skillLearner == nil {
		return fmt.Errorf("skill learner not initialized")
	}
	if err := rt.skillLearner.Remove(name); err != nil {
		return err
	}
	if rt.skReg != nil {
		rt.skReg.Unregister(name)
	}
	return nil
}

// PromoteLearnedSkill moves a learned skill to the defined skills directory.
func (rt *Runtime) PromoteLearnedSkill(name string) error {
	if rt == nil || rt.skillLearner == nil {
		return fmt.Errorf("skill learner not initialized")
	}
	if err := rt.skillLearner.Promote(name); err != nil {
		return err
	}
	// Unregister and let the loader re-discover it from the new location.
	if rt.skReg != nil {
		rt.skReg.Unregister(name)
	}
	return nil
}

// modelSkillRefiner uses the runtime model to refine learned skill content.
type modelSkillRefiner struct {
	model model.Model
}

const refinePromptTemplate = `You are a skill extraction assistant. Based on the following completed agent task, generate a reusable SKILL.md file.

Task prompt: %s
Tools used: %s
Result summary: %s
%s
Requirements:
1. Output MUST start with YAML frontmatter between --- markers
2. Include these frontmatter fields: name (lowercase slug), description (concise), learned: true, keywords (list of 3-8 activation keywords)
3. After frontmatter, write a markdown body with generalized steps (not task-specific)
4. Keep the skill reusable for similar future tasks
5. Output ONLY the SKILL.md content, nothing else`

func (r *modelSkillRefiner) Refine(ctx context.Context, name string, input skills.LearningInput, existing string) (string, error) {
	if r == nil || r.model == nil {
		return "", fmt.Errorf("model not available")
	}

	toolsSummary := formatToolSummary(input.ToolCalls)
	output := strings.TrimSpace(input.Output)
	if len(output) > 500 {
		output = output[:497] + "..."
	}

	existingContext := ""
	if existing != "" {
		existingContext = fmt.Sprintf("\nExisting skill content (merge and improve):\n%s", truncateForRefine(existing, 800))
	}

	prompt := fmt.Sprintf(refinePromptTemplate, input.Prompt, toolsSummary, output, existingContext)

	resp, err := r.model.Complete(ctx, model.Request{
		Messages: []model.Message{{Role: "user", Content: prompt}},
		System:   "You extract reusable agent skills from task transcripts. Be concise and practical.",
	})
	if err != nil {
		return "", err
	}

	content := strings.TrimSpace(resp.Message.TextContent())
	if !strings.HasPrefix(content, "---") {
		return "", fmt.Errorf("invalid skill content: missing frontmatter")
	}
	return content, nil
}

func formatToolSummary(calls []skills.ToolCallSummary) string {
	if len(calls) == 0 {
		return "(none)"
	}
	var parts []string
	for _, tc := range calls {
		parts = append(parts, tc.Name)
	}
	return strings.Join(parts, ", ")
}

func truncateForRefine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// SkillContent returns the full content of a skill for on-demand loading (tier-2).
func (rt *Runtime) SkillContent(name string) (*SkillContentResult, error) {
	if rt == nil || rt.skReg == nil {
		return nil, fmt.Errorf("skill registry not initialized")
	}
	sk, ok := rt.skReg.Get(name)
	if !ok {
		return nil, fmt.Errorf("skill %q not found", name)
	}

	def := sk.Definition()
	skillCtx, skillCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer skillCancel()
	result, err := sk.Execute(skillCtx, skills.ActivationContext{Prompt: ""})
	if err != nil {
		return nil, fmt.Errorf("load skill content: %w", err)
	}

	scope := ""
	if len(def.Metadata) > 0 {
		scope = def.Metadata[skills.MetadataKeySkillScope]
	}

	body := ""
	var supportFiles map[string][]string
	if output, ok := result.Output.(map[string]any); ok {
		if b, ok := output["body"].(string); ok {
			body = b
		}
		if sf, ok := output["support_files"].(map[string][]string); ok {
			supportFiles = sf
		}
	}

	return &SkillContentResult{
		Name:         def.Name,
		Description:  def.Description,
		Scope:        scope,
		Body:         body,
		SupportFiles: supportFiles,
	}, nil
}

// PatchLearnedSkill applies a targeted patch to a learned skill.
func (rt *Runtime) PatchLearnedSkill(name, oldText, newText string, replaceAll bool) error {
	if rt == nil || rt.skillLearner == nil {
		return fmt.Errorf("skill learner not initialized")
	}
	return rt.skillLearner.Patch(name, oldText, newText, replaceAll)
}

// SkillAnalyticsData returns aggregated statistics for all skills.
func (rt *Runtime) SkillAnalyticsData() map[string]*skills.SkillStats {
	if rt == nil || rt.skillTracker == nil {
		return nil
	}
	return rt.skillTracker.GetStats()
}

// SkillActivationHistory returns recent activation records for a skill.
func (rt *Runtime) SkillActivationHistory(name string, limit int) []skills.SkillActivationRecord {
	if rt == nil || rt.skillTracker == nil {
		return nil
	}
	return rt.skillTracker.GetHistory(name, limit)
}

// ReloadSkills reloads filesystem-backed skills into the live registry so
// imported skills become available without restarting the server.
func (rt *Runtime) ReloadSkills() []error {
	if rt == nil || rt.skReg == nil {
		return []error{fmt.Errorf("skill registry not initialized")}
	}
	merged, errs := loadSkillRegistrations(rt.opts)
	if err := rt.skReg.ReplaceAll(merged); err != nil {
		errs = append(errs, err)
	}
	return errs
}

// SetRemoteSkillSources updates the remote skill sources used by the loader
// pipeline. The change takes effect on the next ReloadSkills call.
func (rt *Runtime) SetRemoteSkillSources(sources []skills.RemoteSkillSource) {
	if rt == nil {
		return
	}
	rt.opts.RemoteSkillSources = sources
}

func extractKeywordsFromMetadata(def skills.Definition) []string {
	for _, m := range def.Matchers {
		if km, ok := m.(skills.KeywordMatcher); ok {
			if len(km.Any) > 0 {
				return km.Any
			}
		}
	}
	return nil
}
