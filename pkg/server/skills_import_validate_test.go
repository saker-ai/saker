package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeConflictStrategy(t *testing.T) {
	t.Parallel()
	require.Equal(t, skillImportConflictOverwrite, normalizeConflictStrategy(""))
	require.Equal(t, skillImportConflictOverwrite, normalizeConflictStrategy(skillImportConflictOverwrite))
	require.Equal(t, skillImportConflictSkip, normalizeConflictStrategy(skillImportConflictSkip))
	require.Equal(t, skillImportConflictError, normalizeConflictStrategy(skillImportConflictError))
	require.Equal(t, skillImportConflictMode(""), normalizeConflictStrategy(skillImportConflictMode("nonsense")))
}

func TestNormalizeSkillImportParams_PathHappyPath(t *testing.T) {
	t.Parallel()
	src, paths, err := normalizeSkillImportParams(skillImportParams{
		SourcePath:  "/abs/skill",
		TargetScope: skillImportScopeLocal,
	})
	require.NoError(t, err)
	require.Equal(t, skillImportSourcePath, src)
	require.Equal(t, []string{"/abs/skill"}, paths)
}

func TestNormalizeSkillImportParams_PathUsesSourcePathsArray(t *testing.T) {
	t.Parallel()
	_, paths, err := normalizeSkillImportParams(skillImportParams{
		SourcePaths: []string{"/a", "/b"},
		TargetScope: skillImportScopeLocal,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"/a", "/b"}, paths)
}

func TestNormalizeSkillImportParams_PathEmptyEntryRejected(t *testing.T) {
	t.Parallel()
	_, _, err := normalizeSkillImportParams(skillImportParams{
		SourcePaths: []string{"   "},
		TargetScope: skillImportScopeLocal,
	})
	require.Error(t, err)
}

func TestNormalizeSkillImportParams_PathMissing(t *testing.T) {
	t.Parallel()
	_, _, err := normalizeSkillImportParams(skillImportParams{TargetScope: skillImportScopeLocal})
	require.Error(t, err)
	require.Contains(t, err.Error(), "source_path is required")
}

func TestNormalizeSkillImportParams_BadConflict(t *testing.T) {
	t.Parallel()
	_, _, err := normalizeSkillImportParams(skillImportParams{
		SourcePath:       "/x",
		TargetScope:      skillImportScopeLocal,
		ConflictStrategy: skillImportConflictMode("garbage"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "conflict_strategy")
}

func TestNormalizeSkillImportParams_BadScope(t *testing.T) {
	t.Parallel()
	_, _, err := normalizeSkillImportParams(skillImportParams{SourcePath: "/x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "target_scope")
}

func TestNormalizeSkillImportParams_GitMissingURL(t *testing.T) {
	t.Parallel()
	_, _, err := normalizeSkillImportParams(skillImportParams{
		SourceType:  skillImportSourceGit,
		TargetScope: skillImportScopeLocal,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "repo_url")
}

func TestNormalizeSkillImportParams_ArchiveMissingURL(t *testing.T) {
	t.Parallel()
	_, _, err := normalizeSkillImportParams(skillImportParams{
		SourceType:  skillImportSourceArchive,
		TargetScope: skillImportScopeLocal,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "archive_url")
}

func TestNormalizeSkillImportParams_GitWithBadScope(t *testing.T) {
	t.Parallel()
	_, _, err := normalizeSkillImportParams(skillImportParams{
		SourceType: skillImportSourceGit,
		RepoURL:    "https://x/y.git",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "target_scope")
}

func TestNormalizeSkillImportParams_GitWithRelativeSourcePaths(t *testing.T) {
	t.Parallel()
	_, paths, err := normalizeSkillImportParams(skillImportParams{
		SourceType:  skillImportSourceGit,
		RepoURL:     "https://x/y.git",
		SourcePaths: []string{"alpha", "beta"},
		TargetScope: skillImportScopeLocal,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "beta"}, paths)
}

func TestNormalizeSkillImportParams_GitRejectsAbsoluteSourcePath(t *testing.T) {
	t.Parallel()
	_, _, err := normalizeSkillImportParams(skillImportParams{
		SourceType:  skillImportSourceGit,
		RepoURL:     "https://x/y.git",
		SourcePaths: []string{"/etc"},
		TargetScope: skillImportScopeLocal,
	})
	require.Error(t, err)
}

func TestNormalizeSkillImportParams_BadSourceType(t *testing.T) {
	t.Parallel()
	_, _, err := normalizeSkillImportParams(skillImportParams{
		SourceType:  skillImportSourceType("garbage"),
		TargetScope: skillImportScopeLocal,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid source_type")
}

func TestValidateRelativeSkillImportPath(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateRelativeSkillImportPath("alpha/beta"))
	require.NoError(t, validateRelativeSkillImportPath("alpha"))

	require.Error(t, validateRelativeSkillImportPath(""))
	require.Error(t, validateRelativeSkillImportPath("   "))
	require.Error(t, validateRelativeSkillImportPath("."))
	require.Error(t, validateRelativeSkillImportPath("/abs"))
	require.Error(t, validateRelativeSkillImportPath("../escape"))
}

func TestParseImportedSkillName(t *testing.T) {
	t.Parallel()
	name, err := parseImportedSkillName("---\nname: alpha\ndescription: x\n---\nbody\n")
	require.NoError(t, err)
	require.Equal(t, "alpha", name)

	// BOM tolerated. Use the escape so the source itself stays BOM-free.
	name, err = parseImportedSkillName("\ufeff---\nname: bom-skill\n---\nbody")
	require.NoError(t, err)
	require.Equal(t, "bom-skill", name)

	// Missing frontmatter.
	_, err = parseImportedSkillName("body without frontmatter")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing YAML frontmatter")

	// Unterminated frontmatter.
	_, err = parseImportedSkillName("---\nname: alpha\nstill going\n")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unterminated")

	// Bad YAML.
	_, err = parseImportedSkillName("---\nname: : :\n---\nbody")
	require.Error(t, err)

	// Empty name returns empty string (no error).
	name, err = parseImportedSkillName("---\ndescription: only\n---\nbody")
	require.NoError(t, err)
	require.Empty(t, name)
}

func TestIsValidImportedSkillDir(t *testing.T) {
	t.Parallel()
	require.True(t, isValidImportedSkillDir("alpha"))
	require.True(t, isValidImportedSkillDir("alpha-beta_1"))

	require.False(t, isValidImportedSkillDir(""))
	require.False(t, isValidImportedSkillDir("with/slash"))
	require.False(t, isValidImportedSkillDir(`with\backslash`))
	require.False(t, isValidImportedSkillDir(".."))
	require.False(t, isValidImportedSkillDir("."))
	require.False(t, isValidImportedSkillDir("/abs"))
}

func TestValidateImportedSkill_Happy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(`---
name: my-skill
description: hi
---
body
`), 0o644))

	name, err := validateImportedSkill(dir)
	require.NoError(t, err)
	require.Equal(t, "my-skill", name)
}

func TestValidateImportedSkill_NameFromDirWhenMissing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "fallback-name")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(`---
description: no name
---
body
`), 0o644))

	name, err := validateImportedSkill(dir)
	require.NoError(t, err)
	require.Equal(t, "fallback-name", name)
}

func TestValidateImportedSkill_MissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := validateImportedSkill(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing SKILL.md")
}

func TestValidateImportedSkill_InvalidName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(`---
name: with/slash
---
body
`), 0o644))

	_, err := validateImportedSkill(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid skill name")
}

func TestValidateImportedSkill_BadFrontmatter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("no frontmatter at all"), 0o644))

	_, err := validateImportedSkill(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid SKILL.md")
}

func TestDiscoverSkillImportPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mk := func(p, body string) {
		require.NoError(t, os.MkdirAll(filepath.Join(root, p), 0o755))
		if body != "" {
			require.NoError(t, os.WriteFile(filepath.Join(root, p, "SKILL.md"), []byte(body), 0o644))
		}
	}
	// Two skills at different depths.
	mk("alpha", "x")
	mk("nested/beta", "x")
	// Excluded dirs.
	mk(".git/hooks", "")
	mk("node_modules/pkg", "")

	paths, err := discoverSkillImportPaths(root)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"alpha", filepath.Join("nested", "beta")}, paths)
}

func TestDiscoverSkillImportPaths_NothingFound(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "x"), 0o755))

	_, err := discoverSkillImportPaths(root)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no skills discovered")
}

func TestDiscoverSkillImportPaths_BadRoot(t *testing.T) {
	t.Parallel()
	_, err := discoverSkillImportPaths(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}
