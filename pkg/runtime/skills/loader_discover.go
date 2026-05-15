// loader_discover.go: Filesystem walking, directory scanning, and skill scope classification.
package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/saker-ai/saker/pkg/config"
)

func loadSkillDir(root string, recursive bool, fsLayer *config.FS) ([]SkillFile, []error) {
	var (
		results []SkillFile
		errs    []error
	)

	if fsLayer == nil {
		fsLayer = config.NewFS("", nil)
	}

	info, err := fsLayer.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("skills: stat %s: %w", root, err)}
	}
	if !info.IsDir() {
		return nil, []error{fmt.Errorf("skills: path %s is not a directory", root)}
	}

	if !recursive {
		entries, err := fsLayer.ReadDir(root)
		if err != nil {
			return nil, []error{fmt.Errorf("skills: read dir %s: %w", root, err)}
		}

		for _, entry := range entries {
			isDir := entry.IsDir()
			// Resolve symlinks: DirEntry.IsDir() returns false for symlinks,
			// so stat the target to check if it's actually a directory.
			if !isDir && entry.Type()&fs.ModeSymlink != 0 {
				if target, err := fsLayer.Stat(filepath.Join(root, entry.Name())); err == nil {
					isDir = target.IsDir()
				}
			}
			if !isDir {
				continue
			}

			dirName := entry.Name()
			path := filepath.Join(root, dirName, "SKILL.md")
			file, parseErr := parseSkillFile(path, dirName, fsLayer)
			if parseErr != nil {
				if errors.Is(parseErr, fs.ErrNotExist) {
					continue
				}
				errs = append(errs, parseErr)
				continue
			}

			results = append(results, file)
		}
		return results, errs
	}

	// WalkDir does not follow symlinks into directories, so after the
	// main walk we scan each immediate symlinked child that points to a
	// directory.  We only follow one level deep (the child itself) to
	// avoid symlink cycles.
	walkErr := fsLayer.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			errs = append(errs, fmt.Errorf("skills: walk %s: %w", path, walkErr))
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(d.Name(), "SKILL.md") {
			return nil
		}
		dirName := filepath.Base(filepath.Dir(path))
		file, parseErr := parseSkillFile(path, dirName, fsLayer)
		if parseErr != nil {
			if errors.Is(parseErr, fs.ErrNotExist) {
				return nil
			}
			errs = append(errs, parseErr)
			return nil
		}
		results = append(results, file)
		return nil
	})
	if walkErr != nil {
		errs = append(errs, walkErr)
	}

	// Check immediate children of root for symlinks pointing to directories.
	// For each such symlink, look for a SKILL.md inside (non-recursive to
	// avoid cycles).
	if entries, err := fsLayer.ReadDir(root); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&fs.ModeSymlink == 0 {
				continue
			}
			full := filepath.Join(root, entry.Name())
			if target, err := fsLayer.Stat(full); err == nil && target.IsDir() {
				skillPath := filepath.Join(full, "SKILL.md")
				file, parseErr := parseSkillFile(skillPath, entry.Name(), fsLayer)
				if parseErr != nil {
					if !errors.Is(parseErr, fs.ErrNotExist) {
						errs = append(errs, parseErr)
					}
					continue
				}
				results = append(results, file)
			}
		}
	}
	return results, errs
}

func resolveSkillRoots(opts LoaderOptions) []string {
	roots := make([]string, 0, len(opts.Directories)+2)
	seen := map[string]struct{}{}
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		if !filepath.IsAbs(dir) {
			if strings.TrimSpace(opts.ProjectRoot) != "" {
				dir = filepath.Join(opts.ProjectRoot, dir)
			}
		}
		dir = filepath.Clean(dir)
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		roots = append(roots, dir)
	}
	for _, dir := range opts.Directories {
		add(dir)
	}
	if len(roots) == 0 {
		base := resolveConfigRoot(opts.ProjectRoot, opts.ConfigRoot)
		if base != "" {
			add(filepath.Join(base, "skills"))
			add(filepath.Join(base, "learned-skills"))
			// Subscribed skills are immutable snapshots pulled from a skillhub
			// registry (see pkg/skillhub). They are discovered the same way
			// as local skills but classified as SkillScopeSubscribed.
			add(filepath.Join(base, "subscribed-skills"))
		}
	}
	// Always scan user-level skills directory ~/.agents/skills,
	// regardless of whether custom Directories were provided.
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		add(filepath.Join(home, ".agents", "skills"))
	}
	return roots
}

func resolveConfigRoot(projectRoot, configRoot string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	configRoot = strings.TrimSpace(configRoot)
	if configRoot == "" {
		if projectRoot == "" {
			return ""
		}
		return filepath.Join(projectRoot, ".saker")
	}
	if filepath.IsAbs(configRoot) {
		return filepath.Clean(configRoot)
	}
	if projectRoot == "" {
		return filepath.Clean(configRoot)
	}
	return filepath.Join(projectRoot, configRoot)
}

// deduplicateByRealpath removes skill files that resolve to the same real path
// (e.g. via symlinks), keeping the first occurrence.
func deduplicateByRealpath(files []SkillFile) []SkillFile {
	seen := map[string]struct{}{}
	result := make([]SkillFile, 0, len(files))
	for _, f := range files {
		real, err := filepath.EvalSymlinks(f.Path)
		if err != nil {
			real = f.Path
		}
		real = filepath.Clean(real)
		if _, ok := seen[real]; ok {
			continue
		}
		seen[real] = struct{}{}
		result = append(result, f)
	}
	return result
}

func classifySkillScopeFromPath(path string) SkillScope {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	switch {
	case strings.Contains(clean, "/.saker/learned-skills/"):
		return SkillScopeLearned
	case strings.Contains(clean, "/.saker/subscribed-skills/"):
		return SkillScopeSubscribed
	case strings.Contains(clean, "/.saker/skills/"):
		return SkillScopeRepo
	case strings.Contains(clean, "/.agents/skills/"):
		return SkillScopeUser
	default:
		return SkillScopeCustom
	}
}

func classifySkillScope(path string, opts LoaderOptions) SkillScope {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" {
		return SkillScopeCustom
	}
	configRoot := resolveConfigRoot(opts.ProjectRoot, opts.ConfigRoot)
	if configRoot != "" {
		learnedRoot := filepath.Join(configRoot, "learned-skills")
		if pathWithinRoot(clean, learnedRoot) {
			return SkillScopeLearned
		}
		subscribedRoot := filepath.Join(configRoot, "subscribed-skills")
		if pathWithinRoot(clean, subscribedRoot) {
			return SkillScopeSubscribed
		}
		repoRoot := filepath.Join(configRoot, "skills")
		if pathWithinRoot(clean, repoRoot) {
			return SkillScopeRepo
		}
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		userRoot := filepath.Join(home, ".agents", "skills")
		if pathWithinRoot(clean, userRoot) {
			return SkillScopeUser
		}
	}
	return SkillScopeCustom
}

func pathWithinRoot(path, root string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	root = filepath.Clean(strings.TrimSpace(root))
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
