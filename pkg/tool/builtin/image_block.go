package toolbuiltin

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/saker-ai/saker/pkg/model"
)

// SupportedImageMediaTypes is the canonical whitelist of image MIME types we
// will base64-encode and surface to the LLM as ContentBlockImage.
var SupportedImageMediaTypes = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/gif":  {},
	"image/webp": {},
	"image/bmp":  {},
}

// EncodeImageBytes converts already-loaded bytes into a model.ContentBlock,
// detecting the MIME type and rejecting unsupported formats. Callers that have
// already enforced size limits/whitelists can use this directly; tools reading
// from disk should prefer LoadImageBlockFromFile.
func EncodeImageBytes(data []byte) (*model.ContentBlock, error) {
	if len(data) == 0 {
		return nil, errors.New("image data is empty")
	}
	mediaType := strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
	if semi := strings.Index(mediaType, ";"); semi >= 0 {
		mediaType = strings.TrimSpace(mediaType[:semi])
	}
	if _, ok := SupportedImageMediaTypes[mediaType]; !ok {
		return nil, fmt.Errorf("unsupported image media type %q", mediaType)
	}
	return &model.ContentBlock{
		Type:      model.ContentBlockImage,
		MediaType: mediaType,
		Data:      base64.StdEncoding.EncodeToString(data),
	}, nil
}

// LoadImageBlockFromFile reads an image from disk and returns a populated
// ContentBlock plus its detected media type. Pass maxBytes <= 0 to disable
// the size cap. Callers should perform any path-traversal / sandbox checks
// before invoking this helper.
func LoadImageBlockFromFile(path string, maxBytes int64) (*model.ContentBlock, string, int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, "", 0, fmt.Errorf("%s is a directory", path)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, "", 0, fmt.Errorf("image %d bytes exceeds %d byte limit", info.Size(), maxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", 0, fmt.Errorf("read %s: %w", path, err)
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, "", 0, fmt.Errorf("image %d bytes exceeds %d byte limit", len(data), maxBytes)
	}
	block, err := EncodeImageBytes(data)
	if err != nil {
		return nil, "", 0, err
	}
	return block, block.MediaType, len(data), nil
}
