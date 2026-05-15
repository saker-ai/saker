//go:build desktop

package main

import (
	"context"
	"embed"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/provider"
	"github.com/saker-ai/saker/pkg/server"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	var (
		projectRoot = flag.String("project", ".", "project root directory")
		providerF   = flag.String("provider", "", "model provider")
		modelF      = flag.String("model", "", "model name")
		dataDir     = flag.String("data-dir", "", "data directory")
	)
	flag.Parse()

	if *dataDir == "" {
		home, _ := os.UserHomeDir()
		*dataDir = filepath.Join(home, ".saker", "server")
	}

	absProject, err := filepath.Abs(*projectRoot)
	if err != nil {
		log.Fatalf("resolve project root: %v", err)
	}

	modelProvider, modelName := provider.Detect(*providerF, *modelF, "")
	log.Printf("desktop: provider=%T model=%s project=%s", modelProvider, modelName, absProject)

	ctx := context.Background()

	runtime, err := api.New(ctx, api.Options{
		ProjectRoot:  absProject,
		ModelFactory: modelProvider,
		EntryPoint:   api.EntryPointPlatform,
	})
	if err != nil {
		log.Fatalf("create runtime: %v", err)
	}
	defer runtime.Close()

	// Start the WebSocket server on a background port.
	srv, err := server.New(runtime, server.Options{
		Addr:    "127.0.0.1:10112",
		DataDir: *dataDir,
	})
	if err != nil {
		log.Fatalf("create server: %v", err)
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Printf("ws server stopped: %v", err)
		}
	}()

	app := &App{}

	err = wails.Run(&options.App{
		Title:  "Saker",
		Width:  1200,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: func(ctx context.Context) {
			app.ctx = ctx
		},
		OnShutdown: func(ctx context.Context) {
			_ = srv.Shutdown(ctx)
		},
		Bind: []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// App is the Wails binding struct (minimal — real logic goes through WebSocket).
type App struct {
	ctx context.Context
}
