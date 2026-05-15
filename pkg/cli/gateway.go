package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/im"
	"github.com/godeps/goim"
)

func (a *App) runGatewayMode(stdout, stderr io.Writer, opts api.Options, platform, configPath, token, allowFrom, channelsPath string) error {
	var cfg goim.Config
	var err error
	switch {
	case configPath != "":
		cfg, err = goim.LoadConfig(configPath)
		if err != nil {
			return fmt.Errorf("gateway config: %w", err)
		}
	case token != "":
		cfg = goim.ConfigFromFlags(platform, token, allowFrom)
	default:
		if envToken := os.Getenv("GATEWAY_TOKEN"); envToken != "" {
			cfg = goim.ConfigFromFlags(platform, envToken, allowFrom)
		} else if channelsPath != "" {
			chCfg, loadErr := goim.LoadChannelsJSON(channelsPath)
			if loadErr != nil {
				return fmt.Errorf("load channels.json: %w", loadErr)
			}
			if platform != "" {
				savedOpts := chCfg.LookupChannel(platform)
				if savedOpts == nil {
					return fmt.Errorf("no saved config for platform %q in %s; provide --gateway-token", platform, channelsPath)
				}
				cfg = chCfg.ToConfig()
				pOpts := make(map[string]any, len(savedOpts))
				for k, v := range savedOpts {
					if k != "enabled" {
						pOpts[k] = v
					}
				}
				cfg.Project.Platforms = []goim.PlatformConfig{{Type: platform, Options: pOpts}}
			} else {
				cfg = chCfg.ToConfig()
				if len(cfg.Project.Platforms) == 0 {
					return fmt.Errorf("no enabled channels in %s; provide --gateway-token or configure channels", channelsPath)
				}
			}
		} else {
			return fmt.Errorf("--gateway-token, GATEWAY_TOKEN, or channels.json is required when using --gateway without --gateway-config")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt, err := a.runtimeFactory(ctx, opts)
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}
	defer rt.Close()

	rtAdapter := im.NewRuntimeAdapter(rt.(*api.Runtime))
	agent := goim.NewAgent(rtAdapter, "saker")

	platforms, err := goim.CreatePlatforms(cfg)
	if err != nil {
		return fmt.Errorf("create platforms: %w", err)
	}

	logCleanup, logErr := goim.SetupIMLogger()
	if logErr != nil {
		fmt.Fprintf(stderr, "warning: im log setup: %v\n", logErr)
	}
	if logCleanup != nil {
		defer logCleanup()
	}

	engine := goim.NewEngine(agent, platforms, cfg)

	displayPlatform := platform
	if displayPlatform == "" && len(cfg.Project.Platforms) > 0 {
		names := make([]string, len(cfg.Project.Platforms))
		for i, p := range cfg.Project.Platforms {
			names[i] = p.Type
		}
		displayPlatform = strings.Join(names, ", ")
	}
	fmt.Fprintf(stdout, "saker gateway: starting IM bridge (%s)\n", displayPlatform)
	if err := engine.Start(); err != nil {
		return fmt.Errorf("start engine: %w", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	fmt.Fprintln(stdout, "\nsaker gateway: shutting down...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	done := make(chan error, 1)
	go func() { done <- engine.Stop() }()
	select {
	case err := <-done:
		return err
	case <-shutCtx.Done():
		return fmt.Errorf("gateway shutdown timed out")
	}
}
