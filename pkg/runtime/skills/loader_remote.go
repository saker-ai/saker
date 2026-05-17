package skills

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// RemoteSkillSource configures a remote SkillHub registry from which skills
// are loaded dynamically into memory without writing to disk.
type RemoteSkillSource struct {
	Registry string   // base URL, e.g. "http://localhost:8080"
	Token    string   // bearer token (may be empty for public skills)
	Slugs    []string // if non-empty, only load these slugs; otherwise load all
	// MaxConcurrency limits parallel GetFile requests. Zero defaults to 8.
	MaxConcurrency int
}

// RemoteSkillClient is the minimal interface the remote loader requires from
// a skillhub client. This keeps the skills package decoupled from pkg/skillhub.
type RemoteSkillClient interface {
	FileClient
	ListAllSkills(ctx context.Context, maxPages int) ([]RemoteSkillMeta, error)
	GetSkill(ctx context.Context, slug string) (*RemoteSkillMeta, error)
}

// RemoteSkillMeta carries the metadata returned by the SkillHub list/get API.
type RemoteSkillMeta struct {
	Slug        string
	DisplayName string
	Summary     string
	Category    string
	Kind        string
	Tags        []string
	OwnerHandle string
	Files       []string // file paths in the latest version (optional)
}

// RemoteLoadOutcome is the structured result of remote skill loading.
type RemoteLoadOutcome struct {
	Registrations []SkillRegistration
	Errors        []error
	Origins       map[string]LoadOrigin
}

// LoadFromRemote fetches skills from a remote SkillHub instance and returns
// in-memory SkillRegistration entries. The skill body (SKILL.md) is parsed
// entirely in memory; support files (scripts/) are materialized lazily on
// first execution.
func LoadFromRemote(ctx context.Context, client RemoteSkillClient, src RemoteSkillSource) *RemoteLoadOutcome {
	outcome := &RemoteLoadOutcome{
		Origins: map[string]LoadOrigin{},
	}

	slugs, err := resolveRemoteSlugs(ctx, client, src)
	if err != nil {
		outcome.Errors = append(outcome.Errors, fmt.Errorf("remote: list skills: %w", err))
		return outcome
	}

	if len(slugs) == 0 {
		return outcome
	}

	concurrency := src.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 8
	}

	type result struct {
		reg    SkillRegistration
		origin LoadOrigin
		err    error
	}

	results := make([]result, len(slugs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, meta := range slugs {
		wg.Add(1)
		go func(idx int, m RemoteSkillMeta) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			reg, origin, loadErr := loadOneRemoteSkill(ctx, client, m, src.Registry)
			results[idx] = result{reg: reg, origin: origin, err: loadErr}
		}(i, meta)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			outcome.Errors = append(outcome.Errors, r.err)
			continue
		}
		outcome.Registrations = append(outcome.Registrations, r.reg)
		outcome.Origins[r.reg.Definition.Name] = r.origin
	}

	slog.Info("remote skills loaded",
		"count", len(outcome.Registrations),
		"errors", len(outcome.Errors),
		"registry", src.Registry,
	)
	return outcome
}

func resolveRemoteSlugs(ctx context.Context, client RemoteSkillClient, src RemoteSkillSource) ([]RemoteSkillMeta, error) {
	if len(src.Slugs) > 0 {
		var metas []RemoteSkillMeta
		for _, slug := range src.Slugs {
			m, err := client.GetSkill(ctx, slug)
			if err != nil {
				slog.Warn("remote: skip slug", "slug", slug, "error", err)
				continue
			}
			metas = append(metas, *m)
		}
		return metas, nil
	}
	return client.ListAllSkills(ctx, 20)
}

func loadOneRemoteSkill(ctx context.Context, client RemoteSkillClient, meta RemoteSkillMeta, registry string) (SkillRegistration, LoadOrigin, error) {
	content, err := client.GetFile(ctx, meta.Slug, "latest", "SKILL.md")
	if err != nil {
		return SkillRegistration{}, LoadOrigin{}, fmt.Errorf("remote: fetch %s/SKILL.md: %w", meta.Slug, err)
	}

	skillMeta, body, err := parseFrontMatter(string(content))
	if err != nil {
		return SkillRegistration{}, LoadOrigin{}, fmt.Errorf("remote: parse %s: %w", meta.Slug, err)
	}

	if skillMeta.Name == "" {
		skillMeta.Name = slugToSkillName(meta.Slug)
	}

	if err := validateMetadata(skillMeta); err != nil {
		return SkillRegistration{}, LoadOrigin{}, fmt.Errorf("remote: validate %s: %w", meta.Slug, err)
	}

	def := buildRemoteDefinition(skillMeta, meta, registry)
	handler := newRemoteSkillHandler(RemoteSkillEntry{
		Slug:     meta.Slug,
		Version:  "latest",
		Body:     body,
		Metadata: skillMeta,
		Files:    meta.Files,
		Client:   client,
	})

	origin := LoadOrigin{
		Path:   registry + "/skills/" + meta.Slug,
		Scope:  SkillScopeRemote,
		Origin: "remote",
	}

	return SkillRegistration{Definition: def, Handler: handler}, origin, nil
}

func buildRemoteDefinition(meta SkillMetadata, remoteMeta RemoteSkillMeta, registry string) Definition {
	def := Definition{
		Name:        meta.Name,
		Description: meta.Description,
		WhenToUse:   meta.WhenToUse,
		ArgumentHint: meta.ArgumentHint,
		Arguments:   append([]string(nil), meta.Arguments...),
		Model:       meta.Model,
		AllowedTools: append([]string(nil), meta.AllowedTools...),
		Paths:        append([]string(nil), meta.Paths...),
		RelatedSkills: append([]string(nil), meta.RelatedSkills...),
		RequiresTools: append([]string(nil), meta.RequiresTools...),
		FallbackForTools: append([]string(nil), meta.FallbackForTools...),
	}

	if meta.UserInvocable != nil {
		def.UserInvocable = *meta.UserInvocable
	} else {
		def.UserInvocable = true
	}

	if ctx := strings.TrimSpace(meta.Context); ctx != "" {
		def.ExecutionContext = ctx
	}

	if len(meta.Keywords) > 0 {
		def.Matchers = []Matcher{KeywordMatcher{Any: append([]string(nil), meta.Keywords...)}}
	}

	defMeta := map[string]string{
		"source":             "remote:" + registry + "/" + remoteMeta.Slug,
		MetadataKeySkillOrigin: "remote",
		MetadataKeySkillScope:  string(SkillScopeRemote),
		MetadataKeySkillID:     meta.Name + "::remote:" + remoteMeta.Slug,
	}
	if len(meta.AllowedTools) > 0 {
		defMeta["allowed-tools"] = strings.Join(meta.AllowedTools, ",")
	}
	if meta.License != "" {
		defMeta["license"] = meta.License
	}
	if remoteMeta.OwnerHandle != "" {
		defMeta["owner"] = remoteMeta.OwnerHandle
	}
	if remoteMeta.Category != "" {
		defMeta["category"] = remoteMeta.Category
	}
	for k, v := range meta.Metadata {
		defMeta[k] = fmt.Sprint(v)
	}
	def.Metadata = defMeta

	return def
}

// slugToSkillName converts a skillhub slug (e.g. "cinience/my-cool-skill")
// to a valid skill name by taking the last segment.
func slugToSkillName(slug string) string {
	if idx := strings.LastIndex(slug, "/"); idx >= 0 {
		slug = slug[idx+1:]
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return "unnamed"
	}
	return slug
}

// CloseRemoteHandlers iterates registrations and closes any remoteSkillHandler
// instances to clean up temp directories. Safe to call on mixed registration slices.
func CloseRemoteHandlers(regs []SkillRegistration) {
	for _, reg := range regs {
		if rh, ok := reg.Handler.(*remoteSkillHandler); ok {
			rh.Close()
		}
	}
}
