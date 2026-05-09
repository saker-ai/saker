package server

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cinience/saker/pkg/api"
	git "github.com/go-git/go-git/v5"
	transport "github.com/go-git/go-git/v5/plumbing/transport"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"gopkg.in/yaml.v3"
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

type skillImportFrontmatter struct {
	Name string `yaml:"name"`
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

func normalizeSkillImportParams(params skillImportParams) (skillImportSourceType, []string, error) {
	sourceType := params.SourceType
	if sourceType == "" {
		sourceType = skillImportSourcePath
	}
	if params.ConflictStrategy = normalizeConflictStrategy(params.ConflictStrategy); params.ConflictStrategy == "" {
		return "", nil, errors.New("conflict_strategy must be overwrite, skip, or error")
	}
	switch sourceType {
	case skillImportSourcePath:
		paths := append([]string(nil), params.SourcePaths...)
		if len(paths) == 0 && strings.TrimSpace(params.SourcePath) != "" {
			paths = []string{strings.TrimSpace(params.SourcePath)}
		}
		if len(paths) == 0 {
			return "", nil, errors.New("source_path is required")
		}
		for _, path := range paths {
			if strings.TrimSpace(path) == "" {
				return "", nil, errors.New("source_path entries must not be empty")
			}
		}
		if params.TargetScope != skillImportScopeLocal && params.TargetScope != skillImportScopeGlobal {
			return "", nil, errors.New("target_scope must be local or global")
		}
		return sourceType, paths, nil
	case skillImportSourceGit:
		if strings.TrimSpace(params.RepoURL) == "" {
			return "", nil, errors.New("repo_url is required")
		}
	case skillImportSourceArchive:
		if strings.TrimSpace(params.ArchiveURL) == "" {
			return "", nil, errors.New("archive_url is required")
		}
	default:
		return "", nil, fmt.Errorf("invalid source_type %q", params.SourceType)
	}
	if params.TargetScope != skillImportScopeLocal && params.TargetScope != skillImportScopeGlobal {
		return "", nil, errors.New("target_scope must be local or global")
	}
	for _, path := range params.SourcePaths {
		if err := validateRelativeSkillImportPath(path); err != nil {
			return "", nil, err
		}
	}
	return sourceType, append([]string(nil), params.SourcePaths...), nil
}

func resolveSkillImportTargetRoot(rt *api.Runtime, scope skillImportScope) (string, error) {
	switch scope {
	case skillImportScopeLocal:
		return filepath.Join(rt.ConfigRoot(), "skills"), nil
	case skillImportScopeGlobal:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".agents", "skills"), nil
	default:
		return "", fmt.Errorf("invalid target scope %q", scope)
	}
}

func normalizeConflictStrategy(mode skillImportConflictMode) skillImportConflictMode {
	switch mode {
	case "", skillImportConflictOverwrite:
		return skillImportConflictOverwrite
	case skillImportConflictSkip, skillImportConflictError:
		return mode
	default:
		return ""
	}
}

func prepareSkillImportSource(sourceType skillImportSourceType, params skillImportParams) (string, func(), error) {
	switch sourceType {
	case skillImportSourcePath:
		return "", nil, nil
	case skillImportSourceGit:
		return cloneGitSkillImport(params.RepoURL)
	case skillImportSourceArchive:
		return downloadSkillArchive(params.ArchiveURL)
	default:
		return "", nil, fmt.Errorf("unsupported source_type %q", sourceType)
	}
}

func discoverSkillImportPaths(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case ".git", "node_modules":
			if path != root {
				return filepath.SkipDir
			}
		}
		if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err == nil {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths = append(paths, rel)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("no skills discovered from source")
	}
	return paths, nil
}

func validateRelativeSkillImportPath(path string) error {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "." || cleaned == "" {
		return errors.New("source_paths entries must not be empty")
	}
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return errors.New("source_paths must stay within the imported source")
	}
	return nil
}

func validateImportedSkill(skillSource string) (string, error) {
	skillFile := filepath.Join(skillSource, "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		return "", fmt.Errorf("skill %q missing SKILL.md: %w", skillSource, err)
	}
	name, err := parseImportedSkillName(string(data))
	if err != nil {
		return "", fmt.Errorf("invalid SKILL.md in %q: %w", skillSource, err)
	}
	if name == "" {
		name = filepath.Base(filepath.Clean(skillSource))
	}
	if !isValidImportedSkillDir(name) {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	return name, nil
}

func parseImportedSkillName(raw string) (string, error) {
	raw = strings.TrimPrefix(raw, "\uFEFF")
	if !strings.HasPrefix(raw, "---") {
		return "", errors.New("missing YAML frontmatter")
	}
	rest := strings.TrimPrefix(raw, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", errors.New("unterminated YAML frontmatter")
	}
	metaText := strings.TrimSpace(rest[:end])
	var meta skillImportFrontmatter
	if err := yaml.Unmarshal([]byte(metaText), &meta); err != nil {
		return "", err
	}
	return strings.TrimSpace(meta.Name), nil
}

func isValidImportedSkillDir(name string) bool {
	if name == "" {
		return false
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == "" || filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return false
	}
	return cleaned == name
}

func cloneGitSkillImport(repoURL string) (string, func(), error) {
	if info, err := os.Stat(repoURL); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(repoURL, ".git")); err == nil {
			return repoURL, nil, nil
		}
	}
	tmpDir, err := os.MkdirTemp("", "saker-skill-import-git-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	auth, err := resolveGitCloneAuth(repoURL)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := git.PlainClone(tmpDir, false, &git.CloneOptions{
		URL:   repoURL,
		Depth: 1,
		Auth:  auth,
	}); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git clone failed: %w", err)
	}
	return tmpDir, cleanup, nil
}

func resolveGitCloneAuth(repoURL string) (transport.AuthMethod, error) {
	if !isSSHRepoURL(repoURL) {
		return nil, nil
	}
	if strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK")) != "" {
		auth, err := gitssh.NewSSHAgentAuth("git")
		if err == nil {
			return auth, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	for _, keyName := range []string{"id_ed25519", "id_rsa", "id_ecdsa", "id_dsa"} {
		keyPath := filepath.Join(home, ".ssh", keyName)
		if _, err := os.Stat(keyPath); err != nil {
			continue
		}
		auth, err := gitssh.NewPublicKeysFromFile("git", keyPath, "")
		if err == nil {
			return auth, nil
		}
	}
	return nil, errors.New("git clone failed: SSH repository requires authentication; configure SSH_AUTH_SOCK or use an HTTPS repository URL")
}

func isSSHRepoURL(repoURL string) bool {
	if strings.HasPrefix(repoURL, "ssh://") {
		return true
	}
	return strings.Contains(repoURL, "@") && strings.Contains(repoURL, ":") && !strings.Contains(repoURL, "://")
}

func downloadSkillArchive(archiveURL string) (string, func(), error) {
	resp, err := http.Get(archiveURL)
	if err != nil {
		return "", nil, fmt.Errorf("download archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("download archive: status %d", resp.StatusCode)
	}
	tmpDir, err := os.MkdirTemp("", "saker-skill-import-archive-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	archiveName := filepath.Base(strings.Split(strings.TrimSpace(archiveURL), "?")[0])
	if archiveName == "." || archiveName == "" || archiveName == string(filepath.Separator) {
		archiveName = "source"
	}
	archivePath := filepath.Join(tmpDir, archiveName)
	file, err := os.Create(archivePath)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := extractSkillArchive(archivePath, extractDir); err != nil {
		cleanup()
		return "", nil, err
	}
	rootDir, err := detectSingleRootDir(extractDir)
	if err != nil {
		rootDir = extractDir
	}
	return rootDir, cleanup, nil
}

func extractSkillArchive(archivePath string, dest string) error {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return unzipArchive(archivePath, dest)
	default:
		return untarArchive(archivePath, dest)
	}
}

func unzipArchive(path string, dest string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		target, err := safeArchivePath(dest, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		if err := writeCopiedFile(target, rc, file.Mode()); err != nil {
			_ = rc.Close()
			return err
		}
		_ = rc.Close()
	}
	return nil
}

func untarArchive(path string, dest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var reader io.Reader = file
	if gzReader, gzErr := gzip.NewReader(file); gzErr == nil {
		defer gzReader.Close()
		reader = gzReader
	} else {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		reader = file
	}

	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeArchivePath(dest, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeCopiedFile(target, tarReader, os.FileMode(header.Mode)); err != nil {
				return err
			}
		}
	}
}

func safeArchivePath(root string, name string) (string, error) {
	cleaned := filepath.Clean(name)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("unsafe archive entry %q", name)
	}
	return filepath.Join(root, cleaned), nil
}

func detectSingleRootDir(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return "", errors.New("archive has multiple roots")
	}
	return filepath.Join(root, entries[0].Name()), nil
}

func copyDir(source, target string) error {
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(target, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()
		return writeCopiedFile(dst, srcFile, info.Mode())
	})
}

func writeCopiedFile(target string, reader io.Reader, mode os.FileMode) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, reader); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
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

func prepareTargetDir(targetDir string, mode skillImportConflictMode) (string, error) {
	if _, err := os.Stat(targetDir); err != nil {
		if os.IsNotExist(err) {
			return "created", nil
		}
		return "", err
	}
	switch normalizeConflictStrategy(mode) {
	case skillImportConflictOverwrite:
		if err := os.RemoveAll(targetDir); err != nil {
			return "", err
		}
		return "overwritten", nil
	case skillImportConflictSkip:
		return "skipped", nil
	case skillImportConflictError:
		return "", fmt.Errorf("target skill already exists: %s", filepath.Base(targetDir))
	default:
		return "", fmt.Errorf("unsupported conflict strategy %q", mode)
	}
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
