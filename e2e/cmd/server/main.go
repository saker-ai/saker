// e2e test server — supports both Anthropic and OpenAI-compatible providers.
// Usage:
//
//	# DashScope (OpenAI-compatible)
//	DASHSCOPE_API_KEY=sk-xxx E2E_PROVIDER=openai go run ./e2e/cmd/server
//
//	# Anthropic
//	ANTHROPIC_API_KEY=sk-ant-xxx go run ./e2e/cmd/server
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/tool"
	toolbuiltin "github.com/saker-ai/saker/pkg/tool/builtin"
)

func main() {
	addr := envOr("E2E_ADDR", ":18080")
	provider := buildProvider()

	projectRoot, err := api.ResolveProjectRoot()
	if err != nil {
		log.Fatalf("resolve project root: %v", err)
	}

	// Configure webhook tool to allow internal e2e hosts
	wh := toolbuiltin.NewWebhookTool()
	wh.AllowedHosts = buildAllowedHosts()

	runtime, err := api.New(context.Background(), api.Options{
		EntryPoint:   api.EntryPointPlatform,
		ProjectRoot:  projectRoot,
		ModelFactory: provider,
		CustomTools:  []tool.Tool{wh},
		Timeout:      30 * time.Minute,
	})
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer runtime.Close()

	mux := http.NewServeMux()
	srv := &e2eServer{runtime: runtime}
	mux.HandleFunc("/health", srv.handleHealth)
	mux.HandleFunc("/v1/run", srv.handleRun)
	mux.HandleFunc("/v1/run/stream", srv.handleStream)

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("E2E server listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	log.Println("server exited")
}

func buildProvider() model.Provider {
	providerType := strings.ToLower(envOr("E2E_PROVIDER", "auto"))

	// Auto-detect: if DASHSCOPE_API_KEY is set, use OpenAI-compatible mode
	if providerType == "auto" {
		if os.Getenv("DASHSCOPE_API_KEY") != "" {
			providerType = "openai"
		} else {
			providerType = "anthropic"
		}
	}

	switch providerType {
	case "openai":
		apiKey := envOr("DASHSCOPE_API_KEY", os.Getenv("OPENAI_API_KEY"))
		baseURL := envOr("E2E_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1")
		modelName := envOr("E2E_MODEL", "qwen3.6-plus")

		log.Printf("Provider: OpenAI-compatible | model=%s | base_url=%s", modelName, baseURL)
		return &model.OpenAIProvider{
			APIKey:    apiKey,
			BaseURL:   baseURL,
			ModelName: modelName,
		}

	default:
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
		}
		if apiKey == "" {
			log.Fatal("ANTHROPIC_API_KEY required for anthropic provider")
		}
		modelName := envOr("E2E_MODEL", "claude-sonnet-4-5-20250514")
		baseURL := os.Getenv("ANTHROPIC_BASE_URL")

		log.Printf("Provider: Anthropic | model=%s", modelName)
		return &model.AnthropicProvider{
			APIKey:    apiKey,
			BaseURL:   baseURL,
			ModelName: modelName,
		}
	}
}

// buildAllowedHosts returns hosts that the webhook tool may call in e2e.
// Reads E2E_WEBHOOK_ALLOWED_HOSTS (comma-separated) and always includes
// common Docker Compose service names used in the e2e environment.
func buildAllowedHosts() map[string]bool {
	hosts := map[string]bool{
		"webhook-echo:8080": true,
		"webhook-echo:80":   true,
	}
	if extra := os.Getenv("E2E_WEBHOOK_ALLOWED_HOSTS"); extra != "" {
		for _, h := range strings.Split(extra, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hosts[h] = true
			}
		}
	}
	return hosts
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
