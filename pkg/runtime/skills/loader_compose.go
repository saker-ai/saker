// loader_compose.go: Definition metadata composition, handler assembly, and lazy skill loading.
package skills

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func buildDefinitionMetadata(file SkillFile) map[string]string {
	var meta map[string]string
	if len(file.Metadata.Metadata) > 0 {
		meta = make(map[string]string, len(file.Metadata.Metadata)+4)
		for k, v := range file.Metadata.Metadata {
			meta[k] = fmt.Sprint(v)
		}
	}

	if tools := file.Metadata.AllowedTools; len(tools) > 0 {
		if meta == nil {
			meta = map[string]string{}
		}
		meta["allowed-tools"] = strings.Join(tools, ",")
	}

	if license := strings.TrimSpace(file.Metadata.License); license != "" {
		if meta == nil {
			meta = map[string]string{}
		}
		meta["license"] = license
	}

	if compat := strings.TrimSpace(file.Metadata.Compatibility); compat != "" {
		if meta == nil {
			meta = map[string]string{}
		}
		meta["compatibility"] = compat
	}

	if file.Path != "" {
		if meta == nil {
			meta = map[string]string{}
		}
		meta["source"] = file.Path
		meta[MetadataKeySkillPath] = file.Path
		meta[MetadataKeySkillOrigin] = "filesystem"
		meta[MetadataKeySkillID] = canonicalSkillID(file)
		meta[MetadataKeySkillScope] = string(classifySkillScopeFromPath(file.Path))
	}

	return meta
}

func canonicalSkillID(file SkillFile) string {
	name := strings.TrimSpace(file.Metadata.Name)
	path := filepath.Clean(strings.TrimSpace(file.Path))
	switch {
	case name != "" && path != "":
		return name + "::" + path
	case name != "":
		return name
	default:
		return path
	}
}

func buildHandler(file SkillFile, ops fileOps) Handler {
	return &lazySkillHandler{
		path: file.Path,
		file: file,
		ops:  ops,
	}
}

func loadSkillContent(file SkillFile) (Result, error) {
	body, err := loadSkillBodyFromFS(file.Path, file.fs)
	if err != nil {
		return Result{}, err
	}

	support, supportErrs := loadSupportFilesWithFS(filepath.Dir(file.Path), file.fs)
	if err := errors.Join(supportErrs...); err != nil {
		return Result{}, err
	}

	output := map[string]any{"body": body}
	meta := map[string]any{}

	if len(file.Metadata.AllowedTools) > 0 {
		meta["allowed-tools"] = []string(file.Metadata.AllowedTools)
	}
	if model := strings.TrimSpace(file.Metadata.Model); model != "" {
		meta["api.model_tier"] = model
	}
	if ctx := strings.TrimSpace(file.Metadata.Context); ctx != "" && ctx != "inline" {
		meta["execution_context"] = ctx
	}
	meta["source"] = file.Path

	if len(support) > 0 {
		output["support_files"] = support
		count := 0
		for _, files := range support {
			count += len(files)
		}
		meta["support-file-count"] = count
	}

	if len(meta) == 0 {
		meta = nil
	}

	return Result{
		Skill:    file.Metadata.Name,
		Output:   output,
		Metadata: meta,
	}, nil
}

// lazySkillHandler defers loading the skill body until first execution and
// supports hot-reload by checking file modification time on each access.
type lazySkillHandler struct {
	path string
	file SkillFile
	ops  fileOps

	mu      sync.Mutex
	cached  Result
	loadErr error
	loaded  bool
	modTime time.Time
}

func (h *lazySkillHandler) Execute(_ context.Context, ac ActivationContext) (Result, error) {
	if h == nil {
		return Result{}, errors.New("skills: handler is nil")
	}

	info, err := h.ops.statFile(h.path)
	if err != nil {
		return Result{}, fmt.Errorf("skills: stat %s: %w", h.path, err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.loaded && !info.ModTime().After(h.modTime) {
		if h.loadErr != nil {
			return Result{}, h.loadErr
		}
		return h.applyArgs(h.cached, ac), nil
	}

	h.cached, h.loadErr = loadSkillContent(h.file)
	h.loaded = true
	h.modTime = info.ModTime()

	if h.loadErr != nil {
		return Result{}, h.loadErr
	}
	return h.applyArgs(h.cached, ac), nil
}

// applyArgs performs argument substitution on the cached skill body.
func (h *lazySkillHandler) applyArgs(result Result, ac ActivationContext) Result {
	args := ac.Prompt
	if args == "" {
		return result
	}
	// Substitute in the body field of the output map.
	out, ok := result.Output.(map[string]any)
	if !ok {
		return result
	}
	body, ok := out["body"].(string)
	if !ok || body == "" {
		return result
	}
	substituted := SubstituteArguments(body, args, h.file.Metadata.Arguments)
	// Clone the output map to avoid mutating the cache.
	cloned := make(map[string]any, len(out))
	for k, v := range out {
		cloned[k] = v
	}
	cloned["body"] = substituted
	r := result
	r.Output = cloned
	return r
}

// BodyLength reports the cached body length without triggering a load. The
// second return value indicates whether a body has been loaded.
func (h *lazySkillHandler) BodyLength() (int, bool) {
	if h == nil {
		return 0, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.loaded {
		return 0, false
	}
	return skillBodyLength(h.cached), true
}

func skillBodyLength(res Result) int {
	if res.Output == nil {
		return 0
	}
	if output, ok := res.Output.(map[string]any); ok {
		if body, ok := output["body"].(string); ok {
			return len(body)
		}
		if raw, ok := output["body"].([]byte); ok {
			return len(raw)
		}
	}
	return 0
}
