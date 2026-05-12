// anthropic_headers.go: Anthropic CLI 伪装 header 集合 + 合并工具。
// 历史上由 anthropic_request.go 持有；Bifrost 迁移后旧 SDK 文件被删除，
// 这里独立保留以便 provider.go / Bifrost ExtraHeaders 仍可读到这套 header。
package model

import (
	"os"
	"strings"
)

var anthropicPredefinedHeaders = map[string]string{
	"accept":         "application/json",
	"anthropic-beta": "interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14",
	"anthropic-dangerous-direct-browser-access": "true",
	"anthropic-version":                         "2023-06-01",
	"content-type":                              "application/json",
	"user-agent":                                "claude-cli/2.0.34 (external, cli)",
	"x-app":                                     "cli",
	"x-stainless-arch":                          "arm64",
	"x-stainless-helper-method":                 "stream",
	"x-stainless-lang":                          "js",
	"x-stainless-os":                            "MacOS",
	"x-stainless-package-version":               "0.68.0",
	"x-stainless-retry-count":                   "0",
	"x-stainless-runtime":                       "node",
	"x-stainless-runtime-version":               "v22.20.0",
	"x-stainless-timeout":                       "600",
}

func anthropicCustomHeadersEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ANTHROPIC_CUSTOM_HEADERS_ENABLED")), "true")
}

// newAnthropicHeaders merges predefined Anthropic CLI headers (when the env
// switch is on) with caller-supplied defaults and overrides. The x-api-key
// header is intentionally dropped — Bifrost's Account injects the key
// separately, and we don't want to leak it into ExtraHeaders maps.
func newAnthropicHeaders(defaults, overrides map[string]string) map[string]string {
	merge := func(dst map[string]string, src map[string]string) {
		for k, v := range src {
			norm := strings.ToLower(strings.TrimSpace(k))
			if norm == "" || norm == "x-api-key" {
				continue
			}
			dst[norm] = v
		}
	}

	merged := make(map[string]string)
	if anthropicCustomHeadersEnabled() {
		merge(merged, anthropicPredefinedHeaders)
	}
	merge(merged, defaults)
	merge(merged, overrides)

	if len(merged) == 0 {
		return nil
	}
	return merged
}
