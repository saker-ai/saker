package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/mojatter/s2"
	"github.com/mojatter/s2/server"
)

// Embedded represents an in-process mojatter/s2 server. Two flavors share
// the same handle:
//
//   - ModeExternal — no goroutine, no listener. Handler() returns the http
//     handler that the parent application is expected to mount on its own
//     mux (saker mounts at /_s3/, see pkg/server.openObjectStore).
//   - ModeStandalone — owns a private goroutine running an independent
//     http.Server bound to Addr. Stop terminates that goroutine.
//
// In either mode the application reads/writes through the s2.Storage
// returned alongside from Open — the embedded server is only here so
// external tooling (CLI, browser, sidecar) can speak standard S3 to the
// same data.
type Embedded struct {
	mode    string
	addr    string
	bucket  string
	handler http.Handler // ModeExternal only

	cancel context.CancelFunc // ModeStandalone only
	done   chan error         // ModeStandalone only
	once   sync.Once
}

// Mode reports which startup style was used; one of ModeExternal /
// ModeStandalone.
func (e *Embedded) Mode() string {
	if e == nil {
		return ""
	}
	return e.mode
}

// Addr returns the configured S3 listen address. Empty for ModeExternal —
// the application's main listener carries the S3 traffic in that case.
func (e *Embedded) Addr() string {
	if e == nil {
		return ""
	}
	return e.addr
}

// Bucket returns the bucket name created on startup.
func (e *Embedded) Bucket() string {
	if e == nil {
		return ""
	}
	return e.bucket
}

// Handler returns the embedded S3 API handler for ModeExternal callers to
// mount onto their own mux. Returns nil for ModeStandalone (the standalone
// goroutine already serves it on its own listener).
func (e *Embedded) Handler() http.Handler {
	if e == nil {
		return nil
	}
	return e.handler
}

// Stop signals the standalone goroutine (if any) to shut down and waits
// for it to exit. Safe to call multiple times. No-op for ModeExternal.
func (e *Embedded) Stop() error {
	if e == nil || e.cancel == nil {
		return nil
	}
	var err error
	e.once.Do(func() {
		e.cancel()
		err = <-e.done
		// (*server.Server).Start returns ctx.Err() on graceful shutdown
		// via context cancellation; treat that as success.
		if errors.Is(err, context.Canceled) {
			err = nil
		}
	})
	return err
}

// openEmbedded dispatches to the right startup variant based on cfg.Mode.
func openEmbedded(parent context.Context, cfg EmbeddedConfig) (*Embedded, error) {
	switch cfg.Mode {
	case "", ModeExternal:
		return prepareEmbeddedHandler(parent, cfg)
	case ModeStandalone:
		return startEmbedded(parent, cfg)
	default:
		return nil, fmt.Errorf("storage: unknown embedded mode %q", cfg.Mode)
	}
}

// buildEmbeddedServer constructs an *server.Server with bucket pre-created
// but does NOT call Start. Shared between external and standalone paths so
// the validation / bucket setup is identical.
func buildEmbeddedServer(ctx context.Context, cfg EmbeddedConfig) (*server.Server, error) {
	if cfg.Bucket == "" {
		cfg.Bucket = "media"
	}
	srvCfg := server.DefaultConfig()
	// Listen is irrelevant for external mode (no http.Server bound) and
	// passed through verbatim for standalone. Start() is the only consumer.
	srvCfg.Listen = cfg.Addr
	srvCfg.ConsoleListen = "" // we don't expose the web console
	srvCfg.Type = s2.TypeOSFS
	srvCfg.Root = cfg.Root
	srvCfg.User = cfg.AccessKey
	srvCfg.Password = cfg.SecretKey
	srvCfg.Buckets = []string{cfg.Bucket}

	srv, err := server.NewServer(ctx, srvCfg)
	if err != nil {
		return nil, fmt.Errorf("new server: %w", err)
	}
	if exists, _ := srv.Buckets.Exists(ctx, cfg.Bucket); !exists {
		if err := srv.Buckets.Create(ctx, cfg.Bucket); err != nil {
			return nil, fmt.Errorf("create bucket %q: %w", cfg.Bucket, err)
		}
	}
	return srv, nil
}

// prepareEmbeddedHandler builds the s2 server in handler-only mode. No
// goroutine, no listener — just the http.Handler the caller will mount.
func prepareEmbeddedHandler(parent context.Context, cfg EmbeddedConfig) (*Embedded, error) {
	srv, err := buildEmbeddedServer(parent, cfg)
	if err != nil {
		return nil, err
	}
	return &Embedded{
		mode:    ModeExternal,
		bucket:  cfg.Bucket,
		handler: srv.S3Handler(),
	}, nil
}

// startEmbedded launches a private goroutine that runs the s2 server on
// cfg.Addr. The returned Embedded owns that goroutine; Stop terminates it.
func startEmbedded(parent context.Context, cfg EmbeddedConfig) (*Embedded, error) {
	if cfg.Addr == "" {
		return nil, errors.New("storage: embedded.addr is required for standalone mode")
	}
	ctx, cancel := context.WithCancel(parent)
	srv, err := buildEmbeddedServer(ctx, cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	emb := &Embedded{
		mode:   ModeStandalone,
		addr:   cfg.Addr,
		bucket: cfg.Bucket,
		cancel: cancel,
		done:   make(chan error, 1),
	}
	go func() {
		// Start blocks until ctx is cancelled or any listener dies.
		emb.done <- srv.Start(ctx)
		close(emb.done)
	}()
	return emb, nil
}
