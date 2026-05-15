// skills_import_persist.go: target-root resolution, conflict handling, and on-disk persistence for imported skills.
package server

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/saker-ai/saker/pkg/api"
)

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
