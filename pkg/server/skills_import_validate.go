// skills_import_validate.go: parameter normalization, manifest parsing, and skill discovery for imports.
package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type skillImportFrontmatter struct {
	Name string `yaml:"name"`
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
