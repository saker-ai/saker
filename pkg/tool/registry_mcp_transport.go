package tool

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/cinience/saker/pkg/mcp"
)

func applyMCPTransportOptions(transport mcp.Transport, opts MCPServerOptions) error {
	if transport == nil {
		return errors.New("mcp transport is nil")
	}
	if len(opts.Headers) == 0 && len(opts.Env) == 0 {
		return nil
	}

	switch impl := transport.(type) {
	case *mcp.CommandTransport:
		if len(opts.Env) == 0 {
			return nil
		}
		if impl == nil || impl.Command == nil {
			return errors.New("mcp stdio transport missing command")
		}
		impl.Command.Env = mergeEnv(impl.Command.Env, opts.Env)
	case *mcp.SSEClientTransport:
		if len(opts.Headers) == 0 {
			return nil
		}
		impl.HTTPClient = withInjectedHeaders(impl.HTTPClient, opts.Headers)
	case *mcp.StreamableClientTransport:
		if len(opts.Headers) == 0 {
			return nil
		}
		impl.HTTPClient = withInjectedHeaders(impl.HTTPClient, opts.Headers)
	}
	return nil
}

func withInjectedHeaders(client *http.Client, headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return client
	}
	if client == nil {
		client = &http.Client{}
	}

	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &headerRoundTripper{
		base:    base,
		headers: normalizeHeaders(headers),
	}
	return client
}

func normalizeHeaders(headers map[string]string) http.Header {
	if len(headers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(http.Header, len(keys))
	for _, raw := range keys {
		key := http.CanonicalHeaderKey(strings.TrimSpace(raw))
		if key == "" {
			continue
		}
		out.Set(key, strings.TrimSpace(headers[raw]))
	}
	return out
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers http.Header
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if len(h.headers) == 0 {
		return base.RoundTrip(req)
	}

	clone := req.Clone(req.Context())
	clone.Header = clone.Header.Clone()
	for k, vals := range h.headers {
		clone.Header.Del(k)
		for _, v := range vals {
			if strings.TrimSpace(v) == "" {
				continue
			}
			clone.Header.Add(k, v)
		}
	}
	return base.RoundTrip(clone)
}

func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	if base == nil {
		base = os.Environ()
	}

	keys := make([]string, 0, len(extra))
	trimmed := make(map[string]string, len(extra))
	for k, v := range extra {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		trimmed[key] = v
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(base)+len(keys))
	seen := map[string]struct{}{}
	for _, entry := range base {
		k, _, ok := strings.Cut(entry, "=")
		if !ok || k == "" {
			continue
		}
		if _, ok := trimmed[k]; ok {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, entry)
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s=%s", key, trimmed[key]))
	}
	return out
}
