// loader_parse.go: SKILL.md frontmatter parsing, YAML decoding, and support file enumeration.
package skills

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cinience/saker/pkg/config"
	"gopkg.in/yaml.v3"
)

// ToolList supports YAML string or sequence, normalizing to a de-duplicated list.
type ToolList []string

func (t *ToolList) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Tag == "!!null" {
		*t = nil
		return nil
	}

	var tools []string
	switch value.Kind {
	case yaml.ScalarNode:
		for _, entry := range strings.Split(value.Value, ",") {
			tool := strings.TrimSpace(entry)
			if tool != "" {
				tools = append(tools, tool)
			}
		}
	case yaml.SequenceNode:
		for i, entry := range value.Content {
			if entry.Kind != yaml.ScalarNode {
				return fmt.Errorf("allowed-tools[%d]: expected string", i)
			}
			tool := strings.TrimSpace(entry.Value)
			if tool != "" {
				tools = append(tools, tool)
			}
		}
	default:
		return errors.New("allowed-tools: expected string or sequence")
	}

	seen := map[string]struct{}{}
	deduped := tools[:0]
	for _, tool := range tools {
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		deduped = append(deduped, tool)
	}

	if len(deduped) == 0 {
		*t = nil
		return nil
	}
	*t = ToolList(deduped)
	return nil
}

// SkillMetadata mirrors the YAML frontmatter fields inside SKILL.md.
type SkillMetadata struct {
	Name             string         `yaml:"name"`
	Description      string         `yaml:"description"`
	License          string         `yaml:"license,omitempty"`
	Compatibility    string         `yaml:"compatibility,omitempty"`
	Metadata         map[string]any `yaml:"metadata,omitempty"`
	AllowedTools     ToolList       `yaml:"allowed-tools,omitempty"`
	Keywords         []string       `yaml:"keywords,omitempty"`
	Learned          bool           `yaml:"learned,omitempty"`
	RelatedSkills    []string       `yaml:"related_skills,omitempty"`
	RequiresTools    []string       `yaml:"requires_tools,omitempty"`
	FallbackForTools []string       `yaml:"fallback_for_tools,omitempty"`
	WhenToUse        string         `yaml:"when_to_use,omitempty"`
	ArgumentHint     string         `yaml:"argument-hint,omitempty"`
	Arguments        []string       `yaml:"arguments,omitempty"`
	Model            string         `yaml:"model,omitempty"`
	Context          string         `yaml:"context,omitempty"`
	UserInvocable    *bool          `yaml:"user-invocable,omitempty"`
	Paths            []string       `yaml:"paths,omitempty"`
}

func parseSkillFile(path, dirName string, fsLayer *config.FS) (SkillFile, error) {
	meta, err := readFrontMatter(path, fsLayer)
	if err != nil {
		return SkillFile{}, fmt.Errorf("skills: read %s: %w", path, err)
	}
	if meta.Name != "" && dirName != "" && meta.Name != dirName {
		slog.Warn("skills: name does not match directory", "name", meta.Name, "directory", dirName, "path", path, "using", meta.Name)
	}
	if err := validateMetadata(meta); err != nil {
		return SkillFile{}, fmt.Errorf("skills: validate %s: %w", path, err)
	}

	return SkillFile{
		Name:     meta.Name,
		Path:     path,
		Metadata: meta,
		fs:       fsLayer,
	}, nil
}

func readFrontMatter(path string, fsLayer *config.FS) (SkillMetadata, error) {
	var (
		file fs.File
		err  error
	)
	if fsLayer != nil {
		file, err = fsLayer.Open(path)
	} else {
		file, err = os.Open(path)
	}
	if err != nil {
		return SkillMetadata{}, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	first, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return SkillMetadata{}, err
	}

	first = strings.TrimPrefix(first, "\uFEFF")
	if strings.TrimSpace(first) != "---" {
		return SkillMetadata{}, errors.New("missing YAML frontmatter")
	}

	var lines []string
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return SkillMetadata{}, readErr
		}
		if strings.TrimSpace(line) == "---" {
			metaText := strings.Join(lines, "")
			var meta SkillMetadata
			if err := yaml.Unmarshal([]byte(metaText), &meta); err != nil {
				return SkillMetadata{}, fmt.Errorf("decode YAML: %w", err)
			}
			return meta, nil
		}

		if line != "" {
			lines = append(lines, line)
		}

		if errors.Is(readErr, io.EOF) {
			return SkillMetadata{}, errors.New("missing closing frontmatter separator")
		}
	}
}

func parseFrontMatter(content string) (SkillMetadata, string, error) {
	trimmed := strings.TrimPrefix(content, "\uFEFF") // drop BOM if present
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return SkillMetadata{}, "", errors.New("missing YAML frontmatter")
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return SkillMetadata{}, "", errors.New("missing closing frontmatter separator")
	}

	metaText := strings.Join(lines[1:end], "\n")
	var meta SkillMetadata
	if err := yaml.Unmarshal([]byte(metaText), &meta); err != nil {
		return SkillMetadata{}, "", fmt.Errorf("decode YAML: %w", err)
	}

	body := strings.Join(lines[end+1:], "\n")
	body = strings.TrimPrefix(body, "\n")

	return meta, body, nil
}

func loadSupportFiles(dir string) (map[string][]string, []error) {
	return loadSupportFilesWithFS(dir, nil)
}

func loadSupportFilesWithFS(dir string, fsLayer *config.FS) (map[string][]string, []error) {
	out := map[string][]string{}
	var errs []error

	if fsLayer == nil {
		fsLayer = config.NewFS("", nil)
	}

	for _, sub := range []string{"scripts", "references", "assets"} {
		root := filepath.Join(dir, sub)
		info, err := fsLayer.Stat(root)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("skills: stat %s: %w", root, err))
			}
			continue
		}
		if !info.IsDir() {
			errs = append(errs, fmt.Errorf("skills: %s is not a directory", root))
			continue
		}

		var files []string
		if walkErr := fsLayer.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				errs = append(errs, fmt.Errorf("skills: walk %s: %w", path, walkErr))
				return nil
			}
			if d.IsDir() {
				return nil
			}

			rel, err := filepath.Rel(root, path)
			if err != nil {
				rel = d.Name()
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		}); walkErr != nil {
			errs = append(errs, fmt.Errorf("skills: walk %s: %w", root, walkErr))
			continue
		}

		sort.Strings(files)
		if len(files) > 0 {
			out[sub] = files
		}
	}

	if len(out) == 0 {
		return nil, errs
	}
	return out, errs
}

func loadSkillBody(path string) (string, error) {
	return loadSkillBodyFromFS(path, nil)
}

func loadSkillBodyFromFS(path string, fsLayer *config.FS) (string, error) {
	var (
		data []byte
		err  error
	)
	if fsLayer != nil {
		data, err = fsLayer.ReadFile(path)
	} else {
		data, err = readFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("skills: read %s: %w", path, err)
	}
	_, body, err := parseFrontMatter(string(data))
	if err != nil {
		return "", fmt.Errorf("skills: parse %s: %w", path, err)
	}
	return body, nil
}
