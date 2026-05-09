// Package storage provides a thin wrapper around mojatter/s2 that lets
// saker persist provider-returned media (aigo, canvas, uploads) to a
// pluggable backend: local disk (osfs), an in-process embedded S3 server,
// or a remote S3-compatible service (AWS, Aliyun OSS, Cloudflare R2,
// self-hosted MinIO, etc.).
//
// The package intentionally exposes s2.Storage directly. We do NOT wrap
// it in another interface — s2 already unifies osfs/memfs/s3/gcs/azblob
// and adding another layer is a KISS violation. Callers depend only on
// s2.Storage; the Backend type below is the runtime selector that builds
// the right s2.Config for them.
package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mojatter/s2"

	// Register backend factories. Importing for side effect populates the
	// type registry that s2.NewStorage looks up.
	_ "github.com/mojatter/s2/fs"
	_ "github.com/mojatter/s2/s3"
)

// Backend names accepted in settings.
const (
	BackendOSFS     = "osfs"
	BackendMemFS    = "memfs"
	BackendEmbedded = "embedded"
	BackendS3       = "s3"
)

// Embedded server modes.
//
// ModeExternal (default) hands the application a ready-to-mount http.Handler
// so the S3 API rides the main saker listener at /_s3/. No new TCP port is
// opened.
//
// ModeStandalone keeps the legacy behavior: spawn a goroutine that runs an
// independent http.Server on EmbeddedConfig.Addr. Use this when you want the
// S3 API on a separate network interface or port (different firewall rules,
// dedicated TLS, etc.).
const (
	ModeExternal   = "external"
	ModeStandalone = "standalone"
)

// DefaultBackend is used when settings.storage is omitted or backend is empty.
const DefaultBackend = BackendOSFS

// DefaultPublicBaseURL is the URL prefix mounted by server.handleMediaServe.
// Changing this requires updating the route registration in pkg/server/server.go.
const DefaultPublicBaseURL = "/media"

// DefaultS3MountPath is the URL prefix the embedded S3 API is mounted at when
// running in ModeExternal. Changing this requires updating the route
// registration in pkg/server/server.go and the auth-bypass list.
const DefaultS3MountPath = "/_s3"

// Config is the runtime form of settings.storage. It is populated from
// pkg/config.StorageConfig (which speaks JSON) and passed to Open.
type Config struct {
	Backend       string
	PublicBaseURL string // URL prefix for object reads; defaults to "/media"
	TenantPrefix  string // optional prefix prepended to every key (multi-instance shared bucket)

	OSFS     OSFSConfig
	Embedded EmbeddedConfig
	S3       S3Config
}

// OSFSConfig configures the local-disk backend.
type OSFSConfig struct {
	Root string // absolute path; empty → <dataDir>/media
}

// EmbeddedConfig configures the in-process S3 server.
type EmbeddedConfig struct {
	// Mode selects how the S3 API is exposed. Empty defaults to "external"
	// (mounted on the main saker HTTP listener at /_s3/). Set to
	// "standalone" to run a dedicated http.Server on Addr.
	Mode      string
	Addr      string // S3 listen address (standalone only); empty → 127.0.0.1:9100
	Root      string // backing directory; empty → <dataDir>/media
	Bucket    string // bucket name; empty → "media"
	AccessKey string // S3 client AK
	SecretKey string // S3 client SK
}

// S3Config configures a remote S3-compatible backend.
type S3Config struct {
	Endpoint        string // empty → AWS default
	Region          string
	Bucket          string // bucket name (Root in s2 terms)
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool   // MinIO/R2 typically need this
	PublicBaseURL   string // optional bucket public domain; falls back to SignedURL when empty
}

// Open constructs the storage stack described by cfg. dataDir is the
// server-wide data directory used to fill in defaults for backends that
// need a backing path.
//
// The returned Embedded is non-nil only when cfg.Backend == "embedded";
// the caller owns its lifecycle (call Stop on shutdown).
func Open(ctx context.Context, cfg Config, dataDir string) (s2.Storage, *Embedded, error) {
	cfg = cfg.withDefaults(dataDir)

	switch cfg.Backend {
	case BackendOSFS:
		st, err := s2.NewStorage(ctx, s2.Config{
			Type: s2.TypeOSFS,
			Root: cfg.OSFS.Root,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("storage: open osfs %q: %w", cfg.OSFS.Root, err)
		}
		return st, nil, nil

	case BackendMemFS:
		st, err := s2.NewStorage(ctx, s2.Config{Type: s2.TypeMemFS})
		if err != nil {
			return nil, nil, fmt.Errorf("storage: open memfs: %w", err)
		}
		return st, nil, nil

	case BackendEmbedded:
		// Embedded mode: app talks to s2.Storage directly via osfs at
		// the same Root the embedded S3 server exposes. The S3 endpoint
		// is for external tooling (CLI, browser, sidecar) — application
		// code never round-trips through HTTP.
		st, err := s2.NewStorage(ctx, s2.Config{
			Type: s2.TypeOSFS,
			Root: filepath.Join(cfg.Embedded.Root, cfg.Embedded.Bucket),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("storage: open embedded osfs %q: %w", cfg.Embedded.Root, err)
		}
		emb, err := openEmbedded(ctx, cfg.Embedded)
		if err != nil {
			return nil, nil, fmt.Errorf("storage: open embedded server: %w", err)
		}
		return st, emb, nil

	case BackendS3:
		if cfg.S3.Bucket == "" {
			return nil, nil, errors.New("storage: s3 backend requires bucket")
		}
		st, err := s2.NewStorage(ctx, s2.Config{
			Type: s2.TypeS3,
			Root: cfg.S3.Bucket,
			S3: &s2.S3Config{
				EndpointURL:     cfg.S3.Endpoint,
				Region:          cfg.S3.Region,
				AccessKeyID:     cfg.S3.AccessKeyID,
				SecretAccessKey: cfg.S3.SecretAccessKey,
			},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("storage: open s3 bucket %q: %w", cfg.S3.Bucket, err)
		}
		return st, nil, nil

	default:
		return nil, nil, fmt.Errorf("storage: unknown backend %q", cfg.Backend)
	}
}

// PublicURL returns the public URL for a given object key. When publicBaseURL
// is a path-only prefix (default "/media"), this is just concatenation; when
// it's an absolute URL (CDN/bucket public domain), the same join applies.
func (c Config) PublicURL(key string) string {
	prefix := strings.TrimRight(c.PublicBaseURL, "/")
	key = strings.TrimLeft(key, "/")
	if prefix == "" {
		return "/" + key
	}
	return prefix + "/" + key
}

// Key builds the canonical object key from its parts. The shape is:
//
//	[<tenantPrefix>/]<projectID>/<mediaType>/<sha[:2]>/<sha><ext>
//
// Each segment is URL-safe; the sha[:2] level keeps any single directory
// from accumulating an unbounded number of objects.
func (c Config) Key(projectID, mediaType, sha, ext string) string {
	parts := []string{}
	if p := strings.Trim(c.TenantPrefix, "/"); p != "" {
		parts = append(parts, p)
	}
	if projectID == "" {
		projectID = "_default"
	}
	parts = append(parts, projectID)
	if mediaType == "" {
		mediaType = "blob"
	}
	parts = append(parts, mediaType)
	if len(sha) >= 2 {
		parts = append(parts, sha[:2])
	}
	parts = append(parts, sha+ext)
	return strings.Join(parts, "/")
}

// withDefaults fills in zero-value fields. dataDir is the server's data
// directory and is used as the root for osfs/embedded when not explicitly set.
func (c Config) withDefaults(dataDir string) Config {
	if c.Backend == "" {
		c.Backend = DefaultBackend
	}
	if c.PublicBaseURL == "" {
		c.PublicBaseURL = DefaultPublicBaseURL
	}
	if c.OSFS.Root == "" {
		c.OSFS.Root = filepath.Join(dataDir, "media")
	}
	if c.Embedded.Root == "" {
		c.Embedded.Root = filepath.Join(dataDir, "media")
	}
	if c.Embedded.Bucket == "" {
		c.Embedded.Bucket = "media"
	}
	if c.Embedded.Mode == "" {
		c.Embedded.Mode = ModeExternal
	}
	// Addr is only meaningful in standalone mode. We still default it so
	// users who flip Mode → standalone without setting Addr get a working
	// listener; in external mode the field is ignored entirely.
	if c.Embedded.Mode == ModeStandalone && c.Embedded.Addr == "" {
		c.Embedded.Addr = "127.0.0.1:9100"
	}
	return c
}

// IsNotExist reports whether err indicates the requested object is absent.
// Wrappers should use this rather than depending on s2 directly.
func IsNotExist(err error) bool { return errors.Is(err, s2.ErrNotExist) }
