package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/logging"
	"github.com/cinience/saker/pkg/message"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/persona"
	"github.com/cinience/saker/pkg/runtime/commands"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/runtime/subagents"
	"github.com/google/uuid"
)

type preparedRun struct {
	ctx                 context.Context
	prompt              string
	contentBlocks       []model.ContentBlock
	history             *message.History
	normalized          Request
	recorder            *hookRecorder
	commandResults      []CommandExecution
	skillResults        []SkillExecution
	subagentResult      *subagents.Result
	mode                ModeContext
	toolWhitelist       map[string]struct{}
	detectedLanguage    string
	personaProfile      *persona.Profile
	personaSystemPrompt string
	personaPromptBlocks []string
	personaDisallowed   map[string]struct{}
	// maxIterationsOverride lets a code path force a specific cap for this
	// single run (used by the subagent runner to apply the
	// DefaultSubagentMaxIterations contract on top of the runtime-wide
	// MaxIterations). Zero falls back to rt.opts.MaxIterations; -1 means
	// explicit unlimited even if the runtime had a positive default.
	maxIterationsOverride int
}

type runResult struct {
	output *agent.ModelOutput
	usage  model.Usage
	reason string
}

func (rt *Runtime) prepare(ctx context.Context, req Request) (preparedRun, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fallbackSession := defaultSessionID(rt.mode.EntryPoint)
	normalized := req.normalized(rt.mode, fallbackSession)
	commandExists := func(string) bool { return false }
	if rt.cmdExec != nil {
		known := map[string]struct{}{}
		for _, def := range rt.cmdExec.List() {
			name := canonicalToolName(def.Name)
			if name != "" {
				known[name] = struct{}{}
			}
		}
		commandExists = func(name string) bool {
			_, ok := known[canonicalToolName(name)]
			return ok
		}
	}
	skillExists := func(string) bool { return false }
	if rt.skReg != nil {
		skillExists = func(name string) bool {
			_, ok := rt.skReg.Get(canonicalToolName(name))
			return ok
		}
	}
	parsedSkills, cleanedPrompt, missingSkills := extractPromptSkillInvocations(normalized.Prompt, skillExists, commandExists)
	if !rt.opts.DangerouslySkipPermissions {
		if err := unknownForcedSkillsError(missingSkills); err != nil {
			return preparedRun{}, err
		}
	}
	normalized.ForceSkills = mergeOrderedNames(normalized.ForceSkills, parsedSkills)
	normalized.Prompt = cleanedPrompt
	prompt := strings.TrimSpace(normalized.Prompt)
	if prompt == "" && len(normalized.ContentBlocks) == 0 && len(normalized.ForceSkills) == 0 {
		return preparedRun{}, errors.New("api: prompt is empty")
	}

	if normalized.SessionID == "" {
		normalized.SessionID = fallbackSession
	}

	// Auto-generate RequestID if not provided (UUID tracking)
	if normalized.RequestID == "" {
		normalized.RequestID = uuid.New().String()
	}

	history := rt.histories.Get(normalized.SessionID)
	logging.From(ctx).Debug("prepare", "session_id", normalized.SessionID, "request_id", normalized.RequestID, "history_len", history.Len(), "force_skills", len(normalized.ForceSkills))

	// Fork parent session's history if requested and child is empty.
	if normalized.ParentSessionID != "" && history.Len() == 0 {
		parentHistory := rt.histories.Get(normalized.ParentSessionID)
		for _, msg := range parentHistory.All() {
			history.Append(msg)
		}
	}

	recorder := defaultHookRecorder()

	if rt.compactor != nil {
		if _, _, err := rt.compactor.maybeCompact(ctx, history, normalized.SessionID, recorder); err != nil {
			return preparedRun{}, err
		}
	}

	activation := normalized.activationContext(prompt)

	cmdRes, cleanPrompt, err := rt.executeCommands(ctx, prompt, &normalized)
	if err != nil {
		return preparedRun{}, err
	}
	prompt = cleanPrompt
	activation.Prompt = prompt

	skillRes, promptAfterSkills, err := rt.executeSkills(ctx, prompt, activation, &normalized)
	if err != nil {
		return preparedRun{}, err
	}
	prompt = promptAfterSkills
	activation.Prompt = prompt
	subRes, promptAfterSubagent, err := rt.executeSubagent(ctx, prompt, activation, &normalized)
	if err != nil {
		return preparedRun{}, err
	}
	prompt = promptAfterSubagent
	activation.Prompt = prompt
	whitelist := combineToolWhitelists(normalized.ToolWhitelist, nil)

	// Auto-detect language from user prompt when no explicit language is configured
	// or the default English is in use.
	var detectedLang string
	if rt.opts.Language == "" || rt.opts.Language == "English" {
		detectedLang = detectLanguage(normalized.Prompt)
	}

	// Resolve persona and build persona-specific system prompt.
	var personaProf *persona.Profile
	var personaSysPrompt string
	var personaBlocks []string
	var personaDisallowed map[string]struct{}
	if rt.personaRegistry != nil {
		personaProf = resolveRequestPersona(normalized, rt.personaRegistry, rt.personaRouter)
		if personaProf != nil {
			personaSysPrompt, personaBlocks = buildPersonaSystemPrompt(
				rt.opts.SystemPrompt, rt.systemPromptBlocks, personaProf, rt.opts.ProjectRoot, rt.personaRegistry,
			)
			// Override language if persona specifies one.
			if personaProf.Language != "" {
				detectedLang = personaProf.Language
			}
			// Scope session ID for history isolation.
			normalized.SessionID = persona.ScopedSessionID(personaProf.ID, normalized.SessionID)
			// Merge persona's enabled tools into whitelist.
			if len(personaProf.EnabledTools) > 0 {
				whitelist = combineToolWhitelists(normalized.ToolWhitelist, personaProf.EnabledTools)
			}
			// Build disallowed tools set.
			if len(personaProf.DisallowedTools) > 0 {
				personaDisallowed = make(map[string]struct{}, len(personaProf.DisallowedTools))
				for _, t := range personaProf.DisallowedTools {
					personaDisallowed[canonicalToolName(t)] = struct{}{}
				}
			}
		}
	}

	return preparedRun{
		ctx:                 ctx,
		prompt:              prompt,
		contentBlocks:       normalized.ContentBlocks,
		history:             history,
		normalized:          normalized,
		recorder:            recorder,
		commandResults:      cmdRes,
		skillResults:        skillRes,
		subagentResult:      subRes,
		mode:                normalized.Mode,
		toolWhitelist:       whitelist,
		detectedLanguage:    detectedLang,
		personaProfile:      personaProf,
		personaSystemPrompt: personaSysPrompt,
		personaPromptBlocks: personaBlocks,
		personaDisallowed:   personaDisallowed,
	}, nil
}

func (rt *Runtime) executeCommands(ctx context.Context, prompt string, req *Request) ([]CommandExecution, string, error) {
	if rt.cmdExec == nil {
		return nil, prompt, nil
	}
	invocations, err := commands.Parse(prompt)
	if err != nil {
		if errors.Is(err, commands.ErrNoCommand) {
			return nil, prompt, nil
		}
		return nil, "", err
	}
	cleanPrompt := removeCommandLines(prompt, invocations)
	results, err := rt.cmdExec.Execute(ctx, invocations)
	if err != nil {
		return nil, "", err
	}
	execs := make([]CommandExecution, 0, len(results))
	for _, res := range results {
		def := definitionSnapshot(rt.cmdExec, res.Command)
		execs = append(execs, CommandExecution{Definition: def, Result: res})
		cleanPrompt = applyPromptMetadata(cleanPrompt, res.Metadata)
		mergeTags(req, res.Metadata)
		applyCommandMetadata(req, res.Metadata)
	}
	return execs, cleanPrompt, nil
}

func (rt *Runtime) executeSkills(ctx context.Context, prompt string, activation skills.ActivationContext, req *Request) ([]SkillExecution, string, error) {
	if rt.skReg == nil {
		return nil, prompt, nil
	}
	matches := rt.skReg.Match(activation)
	forced := orderedForcedSkills(rt.skReg, req.ForceSkills)
	matches = append(matches, forced...)
	if len(matches) == 0 {
		return nil, prompt, nil
	}
	prefix := ""
	execs := make([]SkillExecution, 0, len(matches))
	seen := map[string]struct{}{}
	for _, match := range matches {
		skill := match.Skill
		if skill == nil {
			continue
		}
		name := skill.Definition().Name
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		logging.From(ctx).Info("executing skill", "name", name, "score", match.Score, "reason", match.Reason)
		execStart := time.Now()
		res, err := skill.Execute(ctx, activation)
		execDuration := time.Since(execStart)
		execs = append(execs, SkillExecution{Definition: skill.Definition(), Result: res, Err: err, MatchReason: match.Reason})
		if rt.skillTracker != nil {
			rec := skills.SkillActivationRecord{
				Skill:      name,
				Scope:      skill.Definition().Metadata["skill.scope"],
				Source:     skills.ParseSource(match.Reason),
				Score:      match.Score,
				SessionID:  req.SessionID,
				Success:    err == nil,
				DurationMs: execDuration.Milliseconds(),
				Timestamp:  execStart,
			}
			if err != nil {
				rec.Error = err.Error()
			}
			if outMap, ok := res.Output.(map[string]string); ok {
				if body, ok := outMap["body"]; ok {
					rec.TokenUsage = len(body) / 4
				}
			}
			rt.skillTracker.Record(rec)
		}
		if err != nil {
			logging.From(ctx).Error("skill execution failed", "name", name, "error", err)
			return execs, "", err
		}
		prefix = combinePrompt(prefix, res.Output)
		activation.Metadata = mergeMetadata(activation.Metadata, res.Metadata)
		mergeTags(req, res.Metadata)
		applyCommandMetadata(req, res.Metadata)
	}
	prompt = prependPrompt(prompt, prefix)
	prompt = applyPromptMetadata(prompt, activation.Metadata)
	return execs, prompt, nil
}
