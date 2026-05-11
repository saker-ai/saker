package middleware

import (
	"context"
	"sort"
	"strings"

	"github.com/cinience/saker/pkg/runtime/skills"
)

// trace_skills.go owns the optional ForceSkills body-size diff logging that
// runs when WithSkillTracing(true) is set. It hooks BeforeAgent/AfterAgent in
// trace_hooks.go; everything else (sessions/render/IO) lives in
// trace_lifecycle.go.

func (m *TraceMiddleware) traceSkillsSnapshot(ctx context.Context, st *State, before bool) {
	if m == nil || !m.traceSkills || st == nil {
		return
	}
	ensureStateValues(st)
	names := forceSkillsFromState(st.Values)
	if len(names) == 0 {
		return
	}
	registry := registryFromState(st.Values)
	if registry == nil {
		return
	}
	snapshot := skillBodies(registry, names)
	if before {
		st.Values[traceSkillNamesKey] = names
		st.Values[traceSkillBeforeKey] = snapshot
		return
	}

	beforeSnapshot, ok := st.Values[traceSkillBeforeKey].(map[string]int)
	if !ok || len(beforeSnapshot) == 0 {
		return
	}

	ordered := orderedSkillNames(names, beforeSnapshot, snapshot)
	for _, name := range ordered {
		m.logf("skill=%s body_before=%d body_after=%d", name, beforeSnapshot[name], snapshot[name])
	}
}

func forceSkillsFromState(values map[string]any) []string {
	if len(values) == 0 {
		return nil
	}
	if names := stringList(values[forceSkillsValue]); len(names) > 0 {
		return names
	}
	return stringList(values[traceSkillNamesKey])
}

func registryFromState(values map[string]any) *skills.Registry {
	if len(values) == 0 {
		return nil
	}
	if reg, ok := values[skillsRegistryValue].(*skills.Registry); ok {
		return reg
	}
	return nil
}

func skillBodies(reg *skills.Registry, names []string) map[string]int {
	if reg == nil || len(names) == 0 {
		return nil
	}
	out := make(map[string]int, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		skill, ok := reg.Get(key)
		if !ok {
			continue
		}
		out[skill.Definition().Name] = skillBodySize(skill.Handler())
	}
	return out
}

type bodySizer interface {
	BodyLength() (int, bool)
}

func skillBodySize(handler skills.Handler) int {
	if handler == nil {
		return 0
	}
	if sizer, ok := handler.(bodySizer); ok && sizer != nil {
		if size, loaded := sizer.BodyLength(); loaded {
			return size
		}
		return 0
	}
	return 0
}

func stringList(value any) []string {
	switch v := value.(type) {
	case []string:
		return dedupeStrings(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, val := range v {
			if s := anyToString(val); s != "" {
				out = append(out, s)
			}
		}
		return dedupeStrings(out)
	default:
		if s := anyToString(v); s != "" {
			return []string{s}
		}
	}
	return nil
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		val := strings.ToLower(strings.TrimSpace(value))
		if val == "" {
			continue
		}
		if _, ok := seen[val]; ok {
			continue
		}
		seen[val] = struct{}{}
		result = append(result, val)
	}
	return result
}

func orderedSkillNames(names []string, before, after map[string]int) []string {
	seen := map[string]struct{}{}
	order := make([]string, 0, len(names))
	for _, name := range names {
		norm := strings.TrimSpace(name)
		if norm == "" {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		order = append(order, norm)
	}

	var extras []string
	for key := range before {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		extras = append(extras, key)
	}
	for key := range after {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		extras = append(extras, key)
	}
	if len(extras) > 0 {
		sort.Strings(extras)
		order = append(order, extras...)
	}
	return order
}
