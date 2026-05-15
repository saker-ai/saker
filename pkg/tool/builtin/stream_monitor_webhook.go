package toolbuiltin

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/saker-ai/saker/pkg/pipeline"
	"github.com/saker-ai/saker/pkg/security"
)

// stream_monitor_webhook.go isolates outbound HTTP delivery for detected
// events. The SSRF-safe HTTP client used by the underlying stream source is
// also defined here so all SSRF policy lives in one place.

// ssrfSafeClient is a package-level SSRF-safe HTTP client used for stream
// sources where the destination URL is not known up-front (go2rtc upstreams,
// etc.). It validates IPs at connect time and blocks redirects to private
// networks. Webhook delivery does NOT use this client — it builds a per-event
// pinned client in sendWebhook to defeat DNS rebinding.
var ssrfSafeClient = NewSSRFSafeClient()

func (s *StreamMonitorTool) sendWebhook(webhookURL, taskID, streamURL string, ev pipeline.Event) {
	payload, _ := json.Marshal(map[string]any{
		"event":      ev.Type,
		"detail":     ev.Detail,
		"confidence": ev.Confidence,
		"frame":      ev.Frame,
		"timestamp":  time.Now().Format(time.RFC3339),
		"stream_url": streamURL,
		"task_id":    taskID,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Per-event SSRF re-validation + IP pinning. The user-supplied webhook URL
	// could resolve to a private IP between when the monitor was started and
	// when this event fires (DNS rebinding, TTL changes), so re-check and pin.
	parsed, err := url.Parse(webhookURL)
	if err != nil {
		slog.Error("stream_monitor: webhook url parse failed", "url", webhookURL, "error", err)
		return
	}
	ssrfResult, err := security.CheckSSRF(ctx, parsed.Hostname())
	if err != nil {
		slog.Error("stream_monitor: webhook SSRF check failed", "url", webhookURL, "error", err)
		return
	}
	port := parsed.Port()
	if port == "" {
		port = defaultPortForScheme(parsed.Scheme)
	}
	client := newSSRFPinnedClient(ssrfResult, port)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		slog.Error("stream_monitor: webhook request create error", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("stream_monitor: webhook POST failed", "url", webhookURL, "error", err)
		return
	}
	resp.Body.Close()
}
