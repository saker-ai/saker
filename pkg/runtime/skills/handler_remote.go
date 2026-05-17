package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileClient abstracts fetching individual files from a skill registry so
// the handler is not coupled to a concrete HTTP client.
type FileClient interface {
	GetFile(ctx context.Context, slug, version, path string) ([]byte, error)
}

// RemoteSkillEntry holds the in-memory representation of a single skill
// fetched from a remote registry. It is constructed by LoadFromRemote and
// carries everything needed by the handler without touching the filesystem.
type RemoteSkillEntry struct {
	Slug     string
	Version  string
	Body     string
	Metadata SkillMetadata
	Files    []string // all file paths in the version archive
	Client   FileClient
}

// remoteSkillHandler serves skill content entirely from memory. When scripts
// or assets are needed at execution time it materializes them into a
// temporary directory via the FileClient.
type remoteSkillHandler struct {
	entry RemoteSkillEntry

	mu      sync.Mutex
	tempDir string
	closed  bool
}

func newRemoteSkillHandler(entry RemoteSkillEntry) *remoteSkillHandler {
	return &remoteSkillHandler{entry: entry}
}

func (h *remoteSkillHandler) Execute(ctx context.Context, ac ActivationContext) (Result, error) {
	if h == nil {
		return Result{}, fmt.Errorf("skills: remote handler is nil")
	}

	output := map[string]any{"body": h.entry.Body}
	meta := map[string]any{}

	if len(h.entry.Metadata.AllowedTools) > 0 {
		meta["allowed-tools"] = []string(h.entry.Metadata.AllowedTools)
	}
	if m := strings.TrimSpace(h.entry.Metadata.Model); m != "" {
		meta["api.model_tier"] = m
	}
	if c := strings.TrimSpace(h.entry.Metadata.Context); c != "" && c != "inline" {
		meta["execution_context"] = c
	}
	meta["source"] = "remote:" + h.entry.Slug

	support := h.classifySupportFiles()
	if len(support) > 0 {
		if err := h.materializeFiles(ctx, support); err != nil {
			meta["support-materialize-error"] = err.Error()
		} else {
			output["support_files"] = support
			count := 0
			for _, files := range support {
				count += len(files)
			}
			meta["support-file-count"] = count
			meta["support-dir"] = h.tempDir
		}
	}

	if len(meta) == 0 {
		meta = nil
	}

	result := Result{
		Skill:    h.entry.Metadata.Name,
		Output:   output,
		Metadata: meta,
	}

	return h.applyArgs(result, ac), nil
}

func (h *remoteSkillHandler) applyArgs(result Result, ac ActivationContext) Result {
	args := ac.Prompt
	if args == "" {
		return result
	}
	out, ok := result.Output.(map[string]any)
	if !ok {
		return result
	}
	body, ok := out["body"].(string)
	if !ok || body == "" {
		return result
	}
	substituted := SubstituteArguments(body, args, h.entry.Metadata.Arguments)
	cloned := make(map[string]any, len(out))
	for k, v := range out {
		cloned[k] = v
	}
	cloned["body"] = substituted
	r := result
	r.Output = cloned
	return r
}

// classifySupportFiles partitions the version file list into scripts/,
// references/, and assets/ buckets, mirroring loadSupportFilesWithFS.
func (h *remoteSkillHandler) classifySupportFiles() map[string][]string {
	out := map[string][]string{}
	for _, path := range h.entry.Files {
		for _, prefix := range []string{"scripts/", "references/", "assets/"} {
			if strings.HasPrefix(path, prefix) {
				rel := strings.TrimPrefix(path, prefix)
				if rel != "" {
					key := strings.TrimSuffix(prefix, "/")
					out[key] = append(out[key], rel)
				}
				break
			}
		}
	}
	return out
}

// materializeFiles downloads support files on demand and writes them to a
// temporary directory so the agent can execute scripts via bash.
func (h *remoteSkillHandler) materializeFiles(ctx context.Context, support map[string][]string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return fmt.Errorf("handler closed")
	}
	if h.entry.Client == nil {
		return nil
	}

	if h.tempDir == "" {
		dir, err := os.MkdirTemp("", "saker-remote-skill-*")
		if err != nil {
			return fmt.Errorf("create temp dir: %w", err)
		}
		h.tempDir = dir
	}

	for prefix, files := range support {
		for _, rel := range files {
			remotePath := prefix + "/" + rel
			localPath := filepath.Join(h.tempDir, prefix, rel)

			if _, err := os.Stat(localPath); err == nil {
				continue
			}

			data, err := h.entry.Client.GetFile(ctx, h.entry.Slug, h.entry.Version, remotePath)
			if err != nil {
				return fmt.Errorf("fetch %s: %w", remotePath, err)
			}

			if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
				return err
			}
			perm := os.FileMode(0o644)
			if prefix == "scripts" {
				perm = 0o755
			}
			if err := os.WriteFile(localPath, data, perm); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close cleans up any temporary directory created for script materialization.
func (h *remoteSkillHandler) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	if h.tempDir != "" {
		os.RemoveAll(h.tempDir)
		h.tempDir = ""
	}
	return nil
}

// BodyLength reports the in-memory body size.
func (h *remoteSkillHandler) BodyLength() (int, bool) {
	if h == nil {
		return 0, false
	}
	return len(h.entry.Body), true
}
