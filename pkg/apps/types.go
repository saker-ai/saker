// Package apps publishes a canvas DAG as a runnable "App" with stable inputs
// and outputs. It snapshots a canvas Document at publish time (Dify-style)
// so that future edits to the source canvas do not affect the live app.
//
// On-disk layout (parallel to <dataDir>/canvas/):
//
//	<root>/apps/{appId}/
//	  meta.json                         # mutable AppMeta
//	  keys.json                         # mutable KeysFile (api keys + share tokens)
//	  versions/
//	    2026-05-02-145332.json          # immutable AppVersion snapshot
//	    2026-05-02-153041.json
//
// The package intentionally does NOT depend on pkg/server. The runner
// composes pkg/canvas primitives so callers can drive runs from REST,
// RPC, CLI, or test code without booting an HTTP layer.
package apps

import (
	"errors"
	"time"

	"github.com/cinience/saker/pkg/canvas"
)

// Sentinel errors that callers (REST handlers, RPC) translate to status
// codes. Tests assert against these via errors.Is.
var (
	ErrAppNotFound  = errors.New("apps: app not found")
	ErrNotPublished = errors.New("apps: app has no published version")
	ErrInvalidAppID = errors.New("apps: invalid appID")
)

// Visibility values for AppMeta.Visibility.
const (
	VisibilityPrivate = "private"
	VisibilityPublic  = "public"
)

// AppMeta is the mutable record describing one app. Persisted as
// <root>/apps/{id}/meta.json.
type AppMeta struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description,omitempty"`
	Icon             string    `json:"icon,omitempty"`   // emoji or URL
	SourceThreadID   string    `json:"sourceThreadId"`   // editor entry point
	PublishedVersion string    `json:"publishedVersion"` // empty = never published
	Visibility       string    `json:"visibility"`       // VisibilityPrivate | VisibilityPublic
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// AppVersion is an immutable snapshot of the source canvas at publish time
// plus the extracted Inputs/Outputs schema. Persisted as
// <root>/apps/{id}/versions/{version}.json.
type AppVersion struct {
	Version     string           `json:"version"` // "2006-01-02-150405"
	PublishedAt time.Time        `json:"publishedAt"`
	PublishedBy string           `json:"publishedBy,omitempty"`
	Inputs      []AppInputField  `json:"inputs"`
	Outputs     []AppOutputField `json:"outputs"`
	Document    *canvas.Document `json:"document"`
}

// AppInputField describes one user-facing form field that maps onto an
// appInput node in the snapshot Document.
type AppInputField struct {
	NodeID   string   `json:"nodeId"`
	Variable string   `json:"variable"` // key in the inputs map at run time
	Label    string   `json:"label"`
	Type     string   `json:"type"` // text | paragraph | number | select | file
	Required bool     `json:"required,omitempty"`
	Default  any      `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"` // for type=select
	Min      *float64 `json:"min,omitempty"`     // for type=number
	Max      *float64 `json:"max,omitempty"`     // for type=number
}

// AppOutputField describes a single rendered output. SourceRef is the ID
// of the upstream node whose mediaUrl/content should be displayed.
type AppOutputField struct {
	NodeID    string `json:"nodeId"`
	Label     string `json:"label"`
	SourceRef string `json:"sourceRef"`
	Kind      string `json:"kind"` // image | video | audio | text
}

// ApiKey is the persisted record. Hash is bcrypt(secret); Prefix is the
// first 8 plaintext characters shown in the UI. ExpiresAt is optional —
// nil means "never expires"; ValidateAPIKey rejects credentials whose
// ExpiresAt is in the past so a leaked key can be put on a finite leash.
type ApiKey struct {
	ID         string     `json:"id"`
	Hash       string     `json:"hash"`
	Prefix     string     `json:"prefix"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// ShareToken is a public, anonymous-access credential.
type ShareToken struct {
	Token     string     `json:"token"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	RateLimit int        `json:"rateLimit,omitempty"` // requests / minute
}

// KeysFile is the on-disk shape of <root>/apps/{id}/keys.json.
type KeysFile struct {
	ApiKeys     []ApiKey     `json:"apiKeys"`
	ShareTokens []ShareToken `json:"shareTokens"`
}
