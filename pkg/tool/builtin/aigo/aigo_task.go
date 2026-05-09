package aigo

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/godeps/aigo"
)

// cameraAnglePrompts maps camera_angle enum values to natural English descriptions
// for prompt prepending when the provider doesn't support camera_angle natively.
var cameraAnglePrompts = map[string]string{
	"front":      "front view",
	"side":       "side view",
	"back":       "rear view",
	"top-down":   "bird's eye view",
	"low-angle":  "low angle shot",
	"high-angle": "high angle shot",
	"45-degree":  "three-quarter view",
	"close-up":   "extreme close-up",
}

// buildTask converts tool params into an aigo.AgentTask based on the tool name.
func buildTask(toolName string, params map[string]interface{}) sdk.AgentTask {
	switch toolName {
	case "generate_image":
		return buildImageTask(params)
	case "generate_video":
		return buildVideoTask(params)
	case "text_to_speech":
		return buildTTSTask(params)
	case "generate_music":
		return buildMusicTask(params)
	case "design_voice":
		return buildVoiceDesignTask(params)
	case "edit_image":
		return buildEditImageTask(params)
	case "edit_video":
		return buildEditVideoTask(params)
	case "transcribe_audio":
		return buildTranscribeTask(params)
	default:
		return sdk.AgentTask{Prompt: stringParam(params, "prompt")}
	}
}

func buildImageTask(p map[string]interface{}) sdk.AgentTask {
	task := sdk.AgentTask{
		Prompt:         stringParam(p, "prompt"),
		NegativePrompt: stringParam(p, "negative_prompt"),
		Size:           stringParam(p, "size"),
		Width:          intParam(p, "width"),
		Height:         intParam(p, "height"),
	}

	structured := &sdk.AgentTaskStructured{
		ImageSize:        stringParam(p, "size"),
		ImageAspectRatio: stringParam(p, "aspect_ratio"),
		ImageResolution:  stringParam(p, "resolution"),
		ImageCameraAngle: stringParam(p, "camera_angle"),
	}
	if structured.ImageAspectRatio != "" || structured.ImageResolution != "" || structured.ImageCameraAngle != "" {
		task.Structured = structured
	}

	// Prepend camera angle to prompt for providers that don't support it natively.
	if angle := stringParam(p, "camera_angle"); angle != "" {
		if desc, ok := cameraAnglePrompts[angle]; ok {
			task.Prompt = desc + ", " + task.Prompt
		} else {
			task.Prompt = angle + " shot, " + task.Prompt
		}
	}

	// Reference images for image-to-image generation.
	seenRefs := make(map[string]struct{})
	for _, ref := range stringSliceParam(p, "reference_images") {
		appendReferenceAsset(&task, seenRefs, sdk.ReferenceTypeImage, ref)
	}
	if ref := stringParam(p, "reference_image"); ref != "" {
		appendReferenceAsset(&task, seenRefs, sdk.ReferenceTypeImage, ref)
	}

	return task
}

func buildVideoTask(p map[string]interface{}) sdk.AgentTask {
	task := sdk.AgentTask{
		Prompt:   stringParam(p, "prompt"),
		Duration: intParam(p, "duration"),
		Size:     stringParam(p, "size"),
	}

	// Structured video options.
	structured := &sdk.AgentTaskStructured{
		VideoDuration:    intParam(p, "duration"),
		VideoSize:        stringParam(p, "size"),
		VideoAspectRatio: stringParam(p, "aspect_ratio"),
		VideoResolution:  stringParam(p, "resolution"),
	}
	if v, ok := p["audio"]; ok {
		if b, ok := v.(bool); ok {
			structured.VideoAudio = &b
		}
	}
	if v, ok := p["watermark"]; ok {
		if b, ok := v.(bool); ok {
			structured.VideoWatermark = &b
		}
	}
	task.Structured = structured

	// Reference assets: image and/or video.
	seenRefs := make(map[string]struct{})
	for _, ref := range stringSliceParam(p, "reference_images") {
		appendReferenceAsset(&task, seenRefs, sdk.ReferenceTypeImage, ref)
	}
	if ref := stringParam(p, "reference_image"); ref != "" {
		appendReferenceAsset(&task, seenRefs, sdk.ReferenceTypeImage, ref)
	}
	if ref := stringParam(p, "reference_video"); ref != "" {
		appendReferenceAsset(&task, seenRefs, sdk.ReferenceTypeVideo, ref)
	}
	return task
}

func appendReferenceAsset(task *sdk.AgentTask, seen map[string]struct{}, refType sdk.ReferenceType, raw string) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return
	}
	key := string(refType) + ":" + ref
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	task.References = append(task.References, sdk.ReferenceAsset{Type: refType, URL: resolveLocalRef(ref)})
}

func stringSliceParam(p map[string]interface{}, key string) []string {
	raw, ok := p[key]
	if !ok || raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func buildTTSTask(p map[string]interface{}) sdk.AgentTask {
	return sdk.AgentTask{
		Prompt: stringParam(p, "text"),
		TTS: &sdk.TTSOptions{
			Voice:        stringParam(p, "voice"),
			LanguageType: stringParam(p, "language"),
			Instructions: stringParam(p, "instructions"),
		},
	}
}

func buildMusicTask(p map[string]interface{}) sdk.AgentTask {
	prompt := stringParam(p, "prompt")
	if prompt == "" {
		prompt = stringParam(p, "text")
	}
	return sdk.AgentTask{
		Prompt: prompt,
	}
}

func buildVoiceDesignTask(p map[string]interface{}) sdk.AgentTask {
	return sdk.AgentTask{
		Prompt: stringParam(p, "voice_prompt"),
		VoiceDesign: &sdk.VoiceDesignOptions{
			VoicePrompt:   stringParam(p, "voice_prompt"),
			PreviewText:   stringParam(p, "preview_text"),
			TargetModel:   stringParam(p, "target_model"),
			PreferredName: stringParam(p, "preferred_name"),
			Language:      stringParam(p, "language"),
		},
	}
}

func buildEditImageTask(p map[string]interface{}) sdk.AgentTask {
	task := sdk.AgentTask{
		Prompt: stringParam(p, "prompt"),
		Size:   stringParam(p, "size"),
	}
	if url := stringParam(p, "image_url"); url != "" {
		task.References = []sdk.ReferenceAsset{{Type: sdk.ReferenceTypeImage, URL: resolveLocalRef(url)}}
	}
	return task
}

func buildEditVideoTask(p map[string]interface{}) sdk.AgentTask {
	task := sdk.AgentTask{
		Prompt:   stringParam(p, "prompt"),
		Duration: intParam(p, "duration"),
		Size:     stringParam(p, "size"),
	}
	if url := stringParam(p, "video_url"); url != "" {
		task.References = append(task.References, sdk.ReferenceAsset{Type: sdk.ReferenceTypeVideo, URL: resolveLocalRef(url)})
	}
	if url := stringParam(p, "reference_image"); url != "" {
		task.References = append(task.References, sdk.ReferenceAsset{Type: sdk.ReferenceTypeImage, URL: resolveLocalRef(url)})
	}
	return task
}

// resolveLocalRef converts a local /api/files/ path to a base64 data URI
// so that external APIs (e.g. aliyun) can consume it. Public URLs and
// data URIs are returned as-is.
func resolveLocalRef(rawURL string) string {
	if !strings.HasPrefix(rawURL, "/api/files/") {
		return rawURL
	}

	// /api/files/home/vipas/.../foo.png → /home/vipas/.../foo.png
	trimmed := strings.TrimPrefix(rawURL, "/api/files/")
	decoded, err := url.PathUnescape(trimmed)
	if err != nil {
		decoded = trimmed
	}
	diskPath := "/" + decoded

	data, err := os.ReadFile(diskPath)
	if err != nil {
		slog.Warn("[aigo] resolveLocalRef: cannot read file", "path", diskPath, "error", err)
		return rawURL // fall back to original URL
	}

	mimeType := mime.TypeByExtension(filepath.Ext(diskPath))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
	slog.Info("[aigo] resolveLocalRef: converted file to data URI", "path", diskPath, "mime", mimeType, "size", len(data))
	return dataURI
}

func buildTranscribeTask(p map[string]interface{}) sdk.AgentTask {
	prompt := stringParam(p, "audio_url")
	if lang := stringParam(p, "language"); lang != "" {
		prompt += " language=" + lang
	}
	if f := stringParam(p, "response_format"); f != "" {
		prompt += " format=" + f
	}
	return sdk.AgentTask{Prompt: prompt}
}

func stringParam(p map[string]interface{}, key string) string {
	v, _ := p[key].(string)
	return v
}

func intParam(p map[string]interface{}, key string) int {
	switch v := p[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}