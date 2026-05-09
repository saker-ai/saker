// Package skillhub is a thin client for the SkillHub registry used to
// distribute, sync, and publish skills between Saker instances.
//
// See godeps/skillhub/.saker/specs/skillhub-integration/plan.md for the
// overall design.
package skillhub

import "time"

// DefaultRegistry is the canonical public skillhub URL.
// May be overridden via settings.json `skillhub.registry` or the
// SKILLHUB_REGISTRY environment variable.
const DefaultRegistry = "https://skillhub.saker.run"

// User is the identity returned from /api/v1/whoami.
type User struct {
	ID     string `json:"id"`
	Handle string `json:"handle"`
	Role   string `json:"role"`
	Email  string `json:"email,omitempty"`
}

// Skill is the API shape returned by /api/v1/skills.
// The struct is intentionally loose — we decode only fields we use.
type Skill struct {
	ID               string    `json:"id"`
	Slug             string    `json:"slug"`
	DisplayName      string    `json:"displayName,omitempty"`
	Summary          string    `json:"summary,omitempty"`
	Category         string    `json:"category"`
	Kind             string    `json:"kind,omitempty"`
	Visibility       string    `json:"visibility"`
	ModerationStatus string    `json:"moderationStatus"`
	Tags             []string  `json:"tags"`
	Downloads        int64     `json:"downloads"`
	StarsCount       int       `json:"starsCount"`
	LatestVersionID  string    `json:"latestVersionId,omitempty"`
	OwnerHandle      string    `json:"ownerHandle,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// Version is a single published version of a skill.
type Version struct {
	ID          string    `json:"id"`
	SkillID     string    `json:"skillId"`
	Version     string    `json:"version"`
	Fingerprint string    `json:"fingerprint"`
	Changelog   string    `json:"changelog,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// SearchHit is an entry in /api/v1/search response.
type SearchHit struct {
	Slug        string  `json:"slug"`
	DisplayName string  `json:"displayName,omitempty"`
	Summary     string  `json:"summary,omitempty"`
	Category    string  `json:"category,omitempty"`
	OwnerHandle string  `json:"ownerHandle,omitempty"`
	Visibility  string  `json:"visibility,omitempty"` // "public" | "private" — empty when registry doesn't index it
	Kind        string  `json:"kind,omitempty"`
	Downloads   int64   `json:"downloads,omitempty"`
	StarsCount  int     `json:"starsCount,omitempty"`
	Score       float64 `json:"score,omitempty"`
}

// SearchResult wraps hits with total counter.
type SearchResult struct {
	Hits               []SearchHit `json:"hits"`
	EstimatedTotalHits int         `json:"estimatedTotalHits"`
}

// ListResult carries a cursor-paginated skill page.
type ListResult struct {
	Data       []Skill `json:"data"`
	NextCursor string  `json:"nextCursor"`
}

// PublishRequest describes a multipart publish to /api/v1/skills.
type PublishRequest struct {
	Slug        string
	Version     string
	Category    string
	Kind        string
	DisplayName string
	Summary     string
	Changelog   string
	Tags        []string
	Files       map[string][]byte // path → content
}

// PublishResponse mirrors the JSON body returned on success.
type PublishResponse struct {
	Skill   Skill   `json:"skill"`
	Version Version `json:"version"`
}

// DeviceCode is the response from POST /auth/device/code.
type DeviceCode struct {
	DeviceCode      string `json:"deviceCode"`
	UserCode        string `json:"userCode"`
	VerificationURL string `json:"verificationUrl"`
	ExpiresIn       int    `json:"expiresIn"`
	Interval        int    `json:"interval"`
}
