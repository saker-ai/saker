package skillhub

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CollectDirFiles reads every regular file under root and returns a
// map path→bytes suitable for PublishRequest.Files. Subdirectories are
// preserved in the key as forward-slash-separated paths.
// Hidden files (basename starts with '.') are skipped.
func CollectDirFiles(root string, maxBytes int64) (map[string][]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 5 * 1024 * 1024
	}
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != root {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxBytes {
			return fmt.Errorf("file %s exceeds max size", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		files[rel] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, hasSkill := files["SKILL.md"]; !hasSkill {
		return nil, errors.New("SKILL.md not found in skill directory")
	}
	return files, nil
}

// PublishLearnedOptions configures PublishLearned.
type PublishLearnedOptions struct {
	// Handle is the current user handle; required to build a per-user slug.
	Handle string
	// SlugPrefix defaults to "learned-"; resulting slug: <handle>/<SlugPrefix><name>.
	SlugPrefix string
	// Version defaults to "0.0.1".
	Version string
	// Changelog defaults to "auto-published learned skill".
	Changelog string
	// Visibility currently only supports "private" (public learned skills go
	// through request-public flow separately).
	Visibility string
}

// PublishLearned uploads a learned skill directory under the current user's
// namespace. Returns the resulting publish response.
func (c *Client) PublishLearned(ctx context.Context, skillDir string, opts PublishLearnedOptions) (*PublishResponse, error) {
	if opts.Handle == "" {
		return nil, errors.New("opts.Handle is required")
	}
	if opts.SlugPrefix == "" {
		opts.SlugPrefix = "learned-"
	}
	if opts.Version == "" {
		opts.Version = "0.0.1"
	}
	if opts.Changelog == "" {
		opts.Changelog = "auto-published learned skill"
	}
	name := filepath.Base(filepath.Clean(skillDir))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return nil, fmt.Errorf("invalid skillDir: %s", skillDir)
	}
	// Strip a leading learned- prefix if the dir already has it to avoid
	// "learned-learned-foo" double-prefix.
	name = strings.TrimPrefix(name, "learned-")

	slug := opts.Handle + "/" + opts.SlugPrefix + name
	files, err := CollectDirFiles(skillDir, 0)
	if err != nil {
		return nil, err
	}

	return c.Publish(ctx, PublishRequest{
		Slug:      slug,
		Version:   opts.Version,
		Category:  "general",
		Kind:      "learned",
		Changelog: opts.Changelog,
		Files:     files,
	})
}
