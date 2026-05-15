// skills_import.go: HTTP handlers and high-level skill import orchestration.
package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/saker-ai/saker/pkg/api"
)

type skillImportSourceType string

const (
	skillImportSourcePath    skillImportSourceType = "path"
	skillImportSourceGit     skillImportSourceType = "git"
	skillImportSourceArchive skillImportSourceType = "archive"
)

type skillImportScope string

const (
	skillImportScopeLocal  skillImportScope = "local"
	skillImportScopeGlobal skillImportScope = "global"
)

type skillImportParams struct {
	SourceType       skillImportSourceType   `json:"source_type"`
	SourcePath       string                  `json:"source_path"`
	SourcePaths      []string                `json:"source_paths"`
	RepoURL          string                  `json:"repo_url"`
	ArchiveURL       string                  `json:"archive_url"`
	TargetScope      skillImportScope        `json:"target_scope"`
	ConflictStrategy skillImportConflictMode `json:"conflict_strategy"`
}

type skillImportConflictMode string

const (
	skillImportConflictOverwrite skillImportConflictMode = "overwrite"
	skillImportConflictSkip      skillImportConflictMode = "skip"
	skillImportConflictError     skillImportConflictMode = "error"
)

type skillImportItemResult struct {
	SkillID    string `json:"skill_id"`
	SourcePath string `json:"source_path,omitempty"`
	Path       string `json:"path"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

func (h *Handler) handleSkillImportPreview(req Request) Response {
	var params skillImportParams
	if err := decodeParams(req.Params, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	if h.runtime == nil {
		return h.internalError(req.ID, "runtime not initialized")
	}

	result, err := previewSkillImport(h.runtime, params)
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	return h.success(req.ID, result)
}

func (h *Handler) handleSkillImport(req Request) Response {
	var params skillImportParams
	if err := decodeParams(req.Params, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	if h.runtime == nil {
		return h.internalError(req.ID, "runtime not initialized")
	}

	taskID := h.taskTracker.Create("skill/import", "")
	go h.runSkillImportTask(taskID, params)
	return h.success(req.ID, map[string]any{"taskId": taskID})
}

func (h *Handler) runSkillImportTask(taskID string, params skillImportParams) {
	logLine := func(line string) {
		h.taskTracker.AppendLog(taskID, line)
	}
	progress := func(value int, message string) {
		h.taskTracker.UpdateProgress(taskID, value, message)
	}

	progress(5, "validating import request")
	logLine("Validating import request")

	sourceType, paths, err := normalizeSkillImportParams(params)
	if err != nil {
		h.taskTracker.Fail(taskID, err.Error())
		return
	}

	progress(15, "preparing import source")
	logLine("Preparing import source")

	sourceRoot, cleanup, err := prepareSkillImportSource(sourceType, params)
	if err != nil {
		h.taskTracker.Fail(taskID, err.Error())
		return
	}
	if cleanup != nil {
		defer cleanup()
	}

	if sourceType != skillImportSourcePath && len(paths) == 0 {
		progress(35, "discovering skill directories")
		logLine("Discovering skill directories")
		paths, err = discoverSkillImportPaths(sourceRoot)
		if err != nil {
			h.taskTracker.Fail(taskID, err.Error())
			return
		}
		logLine(fmt.Sprintf("Discovered %d skill directories", len(paths)))
	}

	targetRoot, err := resolveSkillImportTargetRoot(h.runtime, params.TargetScope)
	if err != nil {
		h.taskTracker.Fail(taskID, err.Error())
		return
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		h.taskTracker.Fail(taskID, fmt.Sprintf("create target root: %v", err))
		return
	}

	imported := make([]string, 0, len(paths))
	items := make([]skillImportItemResult, 0, len(paths))
	for idx, sourcePath := range paths {
		progress(45+((idx)*45)/maxInt(len(paths), 1), "importing skills")
		logLine("Importing " + sourcePath)

		skillSource := sourcePath
		if sourceType != skillImportSourcePath {
			skillSource = filepath.Join(sourceRoot, filepath.Clean(sourcePath))
		}

		skillID, err := validateImportedSkill(skillSource)
		if err != nil {
			h.taskTracker.Fail(taskID, err.Error())
			return
		}
		targetDir := filepath.Join(targetRoot, skillID)
		conflictAction, err := prepareTargetDir(targetDir, params.ConflictStrategy)
		if err != nil {
			h.taskTracker.Fail(taskID, err.Error())
			return
		}
		if conflictAction == "skipped" {
			logLine(fmt.Sprintf("Skipped existing skill %s", skillID))
			items = append(items, skillImportItemResult{
				SkillID:    skillID,
				SourcePath: skillSource,
				Path:       targetDir,
				Status:     "skipped",
				Message:    "target already exists",
			})
			continue
		}
		if err := copyDir(skillSource, targetDir); err != nil {
			h.taskTracker.Fail(taskID, err.Error())
			return
		}
		imported = append(imported, targetDir)
		items = append(items, skillImportItemResult{
			SkillID:    skillID,
			SourcePath: skillSource,
			Path:       targetDir,
			Status:     "imported",
			Message:    conflictAction,
		})
	}

	progress(92, "reloading skills")
	logLine("Reloading skills")
	if errs := h.runtime.ReloadSkills(); len(errs) > 0 {
		for _, reloadErr := range errs {
			logLine("Reload warning: " + reloadErr.Error())
		}
		h.taskTracker.Fail(taskID, errs[0].Error())
		return
	}

	logLine("Import completed")
	h.taskTracker.UpdateProgress(taskID, 100, "skill import completed")
	h.taskTracker.Complete(taskID, map[string]any{
		"ok":               true,
		"message":          "imported",
		"targetScope":      string(params.TargetScope),
		"path":             firstString(imported),
		"paths":            imported,
		"items":            items,
		"importedSkills":   collectImportItemSkillIDs(items, "imported"),
		"skippedSkills":    collectImportItemSkillIDs(items, "skipped"),
		"conflictStrategy": string(normalizeConflictStrategy(params.ConflictStrategy)),
	})
}

func previewSkillImport(rt *api.Runtime, params skillImportParams) (map[string]any, error) {
	sourceType, paths, err := normalizeSkillImportParams(params)
	if err != nil {
		return nil, err
	}
	sourceRoot, cleanup, err := prepareSkillImportSource(sourceType, params)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if sourceType != skillImportSourcePath && len(paths) == 0 {
		paths, err = discoverSkillImportPaths(sourceRoot)
		if err != nil {
			return nil, err
		}
	}

	targetRoot, err := resolveSkillImportTargetRoot(rt, params.TargetScope)
	if err != nil {
		return nil, err
	}
	items, err := buildSkillImportPreviewItems(sourceType, paths, sourceRoot, targetRoot)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"items":             items,
		"targetScope":       string(params.TargetScope),
		"conflictStrategy":  string(normalizeConflictStrategy(params.ConflictStrategy)),
		"readySkills":       collectImportItemSkillIDs(items, "ready"),
		"conflictingSkills": collectImportItemSkillIDs(items, "conflict"),
	}, nil
}

func buildSkillImportPreviewItems(sourceType skillImportSourceType, paths []string, sourceRoot string, targetRoot string) ([]skillImportItemResult, error) {
	items := make([]skillImportItemResult, 0, len(paths))
	for _, sourcePath := range paths {
		skillSource := sourcePath
		if sourceType != skillImportSourcePath {
			skillSource = filepath.Join(sourceRoot, filepath.Clean(sourcePath))
		}
		skillID, err := validateImportedSkill(skillSource)
		if err != nil {
			return nil, err
		}
		targetDir := filepath.Join(targetRoot, skillID)
		status := "ready"
		message := "new import"
		if _, err := os.Stat(targetDir); err == nil {
			status = "conflict"
			message = "target already exists"
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		items = append(items, skillImportItemResult{
			SkillID:    skillID,
			SourcePath: skillSource,
			Path:       targetDir,
			Status:     status,
			Message:    message,
		})
	}
	return items, nil
}

func decodeParams(input map[string]any, out any) error {
	raw, err := jsonMarshal(input)
	if err != nil {
		return err
	}
	return jsonUnmarshal(raw, out)
}

var (
	jsonMarshal   = func(v any) ([]byte, error) { return json.Marshal(v) }
	jsonUnmarshal = func(data []byte, v any) error { return json.Unmarshal(data, v) }
)

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func firstString(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return items[0]
}

func collectImportItemSkillIDs(items []skillImportItemResult, status string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Status == status {
			out = append(out, item.SkillID)
		}
	}
	return out
}
