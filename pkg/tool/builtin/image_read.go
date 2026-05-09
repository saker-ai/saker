package toolbuiltin

import (
	"context"
	"errors"
	"fmt"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
)

const imageReadDescription = `Reads an image file from the local filesystem within the configured sandbox.
Use this when the model needs to inspect an image file as multimodal input.

Usage:
- The file_path parameter can be absolute or relative to the sandbox root
- Supported formats: png, jpeg, gif, webp, bmp
- The tool returns a text summary plus one image content block
- This tool reads image files only; use file_read for text files.`

var imageReadSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"file_path": map[string]any{
			"type":        "string",
			"description": "The absolute path to the image file to read",
		},
	},
	Required: []string{"file_path"},
}

// defaultMaxImageBytes is the size limit for ImageRead (5 MiB), higher than
// the shared defaultMaxFileBytes (1 MiB) because images are commonly larger.
const defaultMaxImageBytes = 5 << 20

type ImageReadTool struct {
	base *fileSandbox
}

func NewImageReadTool() *ImageReadTool {
	return NewImageReadToolWithRoot("")
}

func NewImageReadToolWithRoot(root string) *ImageReadTool {
	t := &ImageReadTool{base: newFileSandbox(root)}
	t.base.maxBytes = defaultMaxImageBytes
	return t
}

func NewImageReadToolWithSandbox(root string, sandbox *security.Sandbox) *ImageReadTool {
	t := &ImageReadTool{base: newFileSandboxWithSandbox(root, sandbox)}
	t.base.maxBytes = defaultMaxImageBytes
	return t
}

func (i *ImageReadTool) Name() string { return "ImageRead" }

func (i *ImageReadTool) Description() string { return imageReadDescription }

func (i *ImageReadTool) Schema() *tool.JSONSchema { return imageReadSchema }

func (i *ImageReadTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if i == nil || i.base == nil || i.base.sandbox == nil {
		return nil, errors.New("image_read tool is not initialised")
	}
	path, err := i.base.resolvePath(params["file_path"])
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	block, mediaType, size, err := LoadImageBlockFromFile(path, i.base.maxBytes)
	if err != nil {
		return nil, err
	}

	display := displayPath(path, i.base.root)
	return &tool.ToolResult{
		Success:       true,
		Output:        fmt.Sprintf("Image loaded: %s (%d bytes, %s)", display, size, mediaType),
		ContentBlocks: []model.ContentBlock{*block},
		Artifacts: []artifact.ArtifactRef{
			artifact.NewLocalFileRef(path, artifact.ArtifactKindImage),
		},
		Data: map[string]any{
			"path":          display,
			"absolute_path": path,
			"media_type":    mediaType,
			"size_bytes":    size,
		},
	}, nil
}
