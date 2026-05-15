// loader.go: Loader entry points, core types, and filesystem operation infrastructure for skills.
package skills

import (
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/saker-ai/saker/pkg/config"
)

// fileOps abstracts filesystem operations for testability.
type fileOps struct {
	readFile func(string) ([]byte, error)
	openFile func(string) (fs.File, error)
	statFile func(string) (fs.FileInfo, error)
}

var (
	fileOpOverridesMu sync.RWMutex
	fileOpOverrides   = struct {
		read func(string) ([]byte, error)
		stat func(string) (fs.FileInfo, error)
	}{}
)

func readFileOverrideOrOS(path string) ([]byte, error) {
	fileOpOverridesMu.RLock()
	override := fileOpOverrides.read
	fileOpOverridesMu.RUnlock()
	if override != nil {
		return override(path)
	}
	return os.ReadFile(path)
}

func statFileOverrideOrOS(path string) (fs.FileInfo, error) {
	fileOpOverridesMu.RLock()
	override := fileOpOverrides.stat
	fileOpOverridesMu.RUnlock()
	if override != nil {
		return override(path)
	}
	return os.Stat(path)
}

// LoaderOptions controls how skills are discovered from the filesystem.
type LoaderOptions struct {
	ProjectRoot string
	ConfigRoot  string
	// Directories overrides the discovery roots for SKILL.md files.
	// When empty, defaults to "<ConfigRoot>/skills".
	Directories []string
	// Recursive controls whether skills are discovered recursively from each
	// root directory. Nil defaults to true.
	Recursive *bool
	// FS is the filesystem abstraction layer for loading skills.
	// If nil, falls back to os.* functions for backward compatibility.
	FS *config.FS
	// DisabledSkills lists skill names to exclude from loading.
	DisabledSkills []string
}

// SkillFile captures an on-disk SKILL.md entry.
type SkillFile struct {
	Name     string
	Path     string
	Metadata SkillMetadata
	fs       *config.FS
}

// readFile is swappable in tests to track filesystem IO.
var readFile = os.ReadFile

// SkillRegistration wires a definition to its handler.
type SkillRegistration struct {
	Definition Definition
	Handler    Handler
}

// LoadOutcomeFromFS loads skills from the filesystem and returns the structured
// discovery outcome. Errors are aggregated so one broken file will not block
// others. Duplicate names are skipped with a warning entry in the error list.
func LoadOutcomeFromFS(opts LoaderOptions) *SkillLoadOutcome {
	var (
		registrations []SkillRegistration
		errs          []error
		allFiles      []SkillFile
		origins       = map[string]LoadOrigin{}
	)

	fsLayer := opts.FS
	if fsLayer == nil {
		fsLayer = config.NewFS(opts.ProjectRoot, nil)
	}

	ops := resolveFileOps(opts.FS)
	roots := resolveSkillRoots(opts)
	recursive := true
	if opts.Recursive != nil {
		recursive = *opts.Recursive
	}
	for _, root := range roots {
		files, loadErrs := loadSkillDir(root, recursive, fsLayer)
		errs = append(errs, loadErrs...)
		allFiles = append(allFiles, files...)
	}

	// Deduplicate by resolved real path to handle symlinks.
	allFiles = deduplicateByRealpath(allFiles)

	if len(allFiles) == 0 {
		return &SkillLoadOutcome{Errors: errs}
	}

	sort.Slice(allFiles, func(i, j int) bool {
		if allFiles[i].Metadata.Name != allFiles[j].Metadata.Name {
			return allFiles[i].Metadata.Name < allFiles[j].Metadata.Name
		}
		return allFiles[i].Path < allFiles[j].Path
	})

	disabled := make(map[string]struct{}, len(opts.DisabledSkills))
	for _, name := range opts.DisabledSkills {
		disabled[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}

	seen := map[string]string{}
	for _, file := range allFiles {
		// Skip disabled skills.
		if _, isDisabled := disabled[strings.ToLower(file.Metadata.Name)]; isDisabled {
			continue
		}
		if prev, ok := seen[file.Metadata.Name]; ok {
			errs = append(errs, fmt.Errorf("skills: duplicate skill %q at %s (already from %s)", file.Metadata.Name, file.Path, prev))
			continue
		}
		seen[file.Metadata.Name] = file.Path

		userInvocable := true
		if file.Metadata.UserInvocable != nil {
			userInvocable = *file.Metadata.UserInvocable
		}
		var matchers []Matcher
		if len(file.Metadata.Keywords) > 0 {
			matchers = append(matchers, KeywordMatcher{Any: file.Metadata.Keywords})
		}
		def := Definition{
			Name:             file.Metadata.Name,
			Description:      file.Metadata.Description,
			Metadata:         buildDefinitionMetadata(file),
			Matchers:         matchers,
			WhenToUse:        file.Metadata.WhenToUse,
			ArgumentHint:     file.Metadata.ArgumentHint,
			Arguments:        file.Metadata.Arguments,
			Model:            file.Metadata.Model,
			ExecutionContext: file.Metadata.Context,
			UserInvocable:    userInvocable,
			AllowedTools:     []string(file.Metadata.AllowedTools),
			Paths:            file.Metadata.Paths,
			RelatedSkills:    file.Metadata.RelatedSkills,
			RequiresTools:    file.Metadata.RequiresTools,
			FallbackForTools: file.Metadata.FallbackForTools,
		}
		reg := SkillRegistration{
			Definition: def,
			Handler:    buildHandler(file, ops),
		}
		registrations = append(registrations, reg)
		origins[def.Name] = LoadOrigin{
			Path:   file.Path,
			Scope:  classifySkillScope(file.Path, opts),
			Origin: "filesystem",
		}
	}

	return &SkillLoadOutcome{
		Registrations: registrations,
		Errors:        errs,
		Origins:       origins,
	}
}

// LoadFromFS loads skills from the filesystem. Errors are aggregated so one
// broken file will not block others. Duplicate names are skipped with a
// warning entry in the error list.
func LoadFromFS(opts LoaderOptions) ([]SkillRegistration, []error) {
	outcome := LoadOutcomeFromFS(opts)
	if outcome == nil {
		return nil, nil
	}
	return outcome.Registrations, outcome.Errors
}

func resolveFileOps(fsLayer *config.FS) fileOps {
	if fsLayer != nil {
		return fileOps{
			readFile: fsLayer.ReadFile,
			openFile: fsLayer.Open,
			statFile: fsLayer.Stat,
		}
	}
	return fileOps{
		readFile: readFileOverrideOrOS,
		openFile: func(path string) (fs.File, error) { return os.Open(path) },
		statFile: statFileOverrideOrOS,
	}
}

// SetReadFileForTest swaps the file reader; intended for white-box tests only.
func SetReadFileForTest(fn func(string) ([]byte, error)) (restore func()) {
	prev := readFile
	readFile = fn
	return func() {
		readFile = prev
	}
}

// SetSkillFileOpsForTest swaps filesystem helpers; intended for white-box tests only.
func SetSkillFileOpsForTest(
	read func(string) ([]byte, error),
	stat func(string) (fs.FileInfo, error),
) (restore func()) {
	fileOpOverridesMu.Lock()
	prev := fileOpOverrides
	if read != nil {
		fileOpOverrides.read = read
	}
	if stat != nil {
		fileOpOverrides.stat = stat
	}
	fileOpOverridesMu.Unlock()
	return func() {
		fileOpOverridesMu.Lock()
		fileOpOverrides = prev
		fileOpOverridesMu.Unlock()
	}
}
