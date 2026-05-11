//go:build integration

// Package otlp asserts the OTel HTTP middleware actually exports spans to a
// real collector. The test is gated behind the integration build tag and
// requires the collector spun up via docker-compose.otel.yml in this
// directory.
//
// Run:
//
//	docker compose -f test/integration/otlp/docker-compose.otel.yml up -d --wait
//	OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 \
//	  go test -tags integration -count=1 ./test/integration/otlp/...
//	docker compose -f test/integration/otlp/docker-compose.otel.yml down
package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cinience/saker/pkg/middleware"
)

// TestOTLPSpansLandInCollector asserts that:
//  1. OTLPConfigFromEnv parses the env vars set by the test runner
//  2. SetupOTLP installs an exporter pointing at the spun-up collector
//  3. A request through OTELHTTPMiddleware produces a span the
//     collector writes to its file exporter
func TestOTLPSpansLandInCollector(t *testing.T) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		t.Skip("OTEL_EXPORTER_OTLP_ENDPOINT not set; skipping OTLP integration test")
	}

	// Override service.name to a unique value so we can grep for it in the
	// collector's NDJSON output without colliding with other test runs.
	uniqueService := "saker-otlp-itest-" + time.Now().Format("20060102150405")
	t.Setenv("OTEL_SERVICE_NAME", uniqueService)

	cfg, ok := middleware.OTLPConfigFromEnv()
	if !ok {
		t.Fatal("OTLPConfigFromEnv returned ok=false despite endpoint set")
	}
	if cfg.ServiceName != uniqueService {
		t.Fatalf("service name %q not picked up", cfg.ServiceName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdown, err := middleware.SetupOTLP(ctx, cfg)
	if err != nil {
		t.Fatalf("SetupOTLP: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.OTELHTTPMiddleware())
	r.GET("/probe/:id", func(c *gin.Context) { c.String(http.StatusOK, c.Param("id")) })

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/probe/abc", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("probe got status %d", w.Code)
		}
	}

	// Force span export before reading the collector file.
	flushCtx, flushCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer flushCancel()
	if err := shutdown(flushCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	spansFile := spansOutputPath(t)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if hasServiceSpan(t, spansFile, uniqueService) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("collector file %s never received span for service %q", spansFile, uniqueService)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// spansOutputPath resolves where the docker-compose.otel.yml file exporter
// writes its NDJSON. Defaults to ./otel-output/spans.json relative to this
// test file, override with OTEL_SPANS_FILE.
func spansOutputPath(t *testing.T) string {
	t.Helper()
	if v := strings.TrimSpace(os.Getenv("OTEL_SPANS_FILE")); v != "" {
		return v
	}
	pwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(pwd, "otel-output", "spans.json")
}

// hasServiceSpan scans the NDJSON span dump for any record whose
// resource.service.name matches the supplied value. Returns false on any
// I/O or parse error so the caller's retry loop keeps trying until the
// deadline expires.
func hasServiceSpan(t *testing.T, path, service string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var doc struct {
			ResourceSpans []struct {
				Resource struct {
					Attributes []struct {
						Key   string `json:"key"`
						Value struct {
							StringValue string `json:"stringValue"`
						} `json:"value"`
					} `json:"attributes"`
				} `json:"resource"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal(line, &doc); err != nil {
			continue
		}
		for _, rs := range doc.ResourceSpans {
			for _, a := range rs.Resource.Attributes {
				if a.Key == "service.name" && a.Value.StringValue == service {
					return true
				}
			}
		}
	}
	return false
}

// TestComposeFileIsValid is a fast sanity check that the docker-compose
// file in this directory parses cleanly. It runs even when the integration
// build tag is set, but only triggers `docker compose config` if the
// docker CLI is on PATH.
func TestComposeFileIsValid(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed; skipping compose validation")
	}
	pwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	out, err := exec.Command("docker", "compose",
		"-f", filepath.Join(pwd, "docker-compose.otel.yml"),
		"config",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config failed: %v\n%s", err, out)
	}
}
