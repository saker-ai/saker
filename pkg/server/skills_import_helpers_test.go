package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaxInt(t *testing.T) {
	t.Parallel()
	require.Equal(t, 5, maxInt(5, 3))
	require.Equal(t, 5, maxInt(3, 5))
	require.Equal(t, 0, maxInt(0, 0))
	require.Equal(t, -1, maxInt(-1, -2))
}

func TestFirstString(t *testing.T) {
	t.Parallel()
	require.Equal(t, "", firstString(nil))
	require.Equal(t, "", firstString([]string{}))
	require.Equal(t, "alpha", firstString([]string{"alpha", "beta"}))
}

func TestCollectImportItemSkillIDs(t *testing.T) {
	t.Parallel()
	items := []skillImportItemResult{
		{SkillID: "a", Status: "ready"},
		{SkillID: "b", Status: "conflict"},
		{SkillID: "c", Status: "ready"},
		{SkillID: "d", Status: "skipped"},
	}
	require.Equal(t, []string{"a", "c"}, collectImportItemSkillIDs(items, "ready"))
	require.Equal(t, []string{"b"}, collectImportItemSkillIDs(items, "conflict"))
	require.Empty(t, collectImportItemSkillIDs(items, "missing"))
	require.Empty(t, collectImportItemSkillIDs(nil, "ready"))
}

func TestDecodeParams(t *testing.T) {
	t.Parallel()
	type Out struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	var out Out
	in := map[string]any{"name": "alpha", "n": 7}
	require.NoError(t, decodeParams(in, &out))
	require.Equal(t, "alpha", out.Name)
	require.Equal(t, 7, out.N)
}

func TestDecodeParams_MarshalError(t *testing.T) {
	origMarshal := jsonMarshal
	jsonMarshal = func(v any) ([]byte, error) { return nil, errors.New("marshal boom") }
	defer func() { jsonMarshal = origMarshal }()

	var out struct{}
	err := decodeParams(map[string]any{"x": 1}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "marshal boom")
}

func TestDecodeParams_UnmarshalError(t *testing.T) {
	origUnmarshal := jsonUnmarshal
	jsonUnmarshal = func(data []byte, v any) error { return errors.New("unmarshal boom") }
	defer func() { jsonUnmarshal = origUnmarshal }()

	var out struct{}
	err := decodeParams(map[string]any{"x": 1}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal boom")
}

// buildSkillImportPreviewItems for skillImportSourcePath uses the absolute
// source path verbatim (no sourceRoot join). It validates each path produces
// a known skill ID and notes whether the target already exists.
func TestBuildSkillImportPreviewItems_NewItem(t *testing.T) {
	srcDir := t.TempDir()
	skillRoot := filepath.Join(srcDir, "my-skill")
	require.NoError(t, os.MkdirAll(skillRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(`---
name: my-skill
description: test
---
# Skill body
`), 0o644))

	target := t.TempDir()
	items, err := buildSkillImportPreviewItems(skillImportSourcePath, []string{skillRoot}, "", target)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "my-skill", items[0].SkillID)
	require.Equal(t, "ready", items[0].Status)
	require.Equal(t, "new import", items[0].Message)
}

func TestBuildSkillImportPreviewItems_Conflict(t *testing.T) {
	srcDir := t.TempDir()
	skillRoot := filepath.Join(srcDir, "dup-skill")
	require.NoError(t, os.MkdirAll(skillRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(`---
name: dup-skill
description: conflict
---
body
`), 0o644))

	target := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(target, "dup-skill"), 0o755))

	items, err := buildSkillImportPreviewItems(skillImportSourcePath, []string{skillRoot}, "", target)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "conflict", items[0].Status)
	require.Equal(t, "target already exists", items[0].Message)
}

func TestBuildSkillImportPreviewItems_InvalidSource(t *testing.T) {
	target := t.TempDir()
	missing := filepath.Join(t.TempDir(), "no-such-skill")
	_, err := buildSkillImportPreviewItems(skillImportSourcePath, []string{missing}, "", target)
	require.Error(t, err)
}

func TestBuildSkillImportPreviewItems_GitJoinsSourceRoot(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "alpha")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: alpha
description: from git
---
body
`), 0o644))

	target := t.TempDir()
	items, err := buildSkillImportPreviewItems(skillImportSourceGit, []string{"alpha"}, root, target)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "alpha", items[0].SkillID)
	require.Equal(t, filepath.Join(root, "alpha"), items[0].SourcePath)
}
