// service.go: Service struct + constructor + dependency wiring.
//
// The Store struct itself lives in store.go; this file holds the package's
// option types, sentinel errors, and small shared helpers (slugify, newID)
// that the per-slice service_*.go files all build on.
package project

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors callers can match with errors.Is.
var (
	ErrUserNotFound      = errors.New("project: user not found")
	ErrAlreadyMember     = errors.New("project: already a member")
	ErrNotMember         = errors.New("project: not a member of this project")
	ErrInsufficientRole  = errors.New("project: insufficient role")
	ErrSoleOwner         = errors.New("project: cannot remove or demote sole owner")
	ErrProjectNotFound   = errors.New("project: project not found")
	ErrInviteNotFound    = errors.New("project: invite not found")
	ErrInviteWrongUser   = errors.New("project: invite is for a different user")
	ErrInviteNotPending  = errors.New("project: invite is not pending")
	ErrSelfInvite        = errors.New("project: cannot invite yourself")
	ErrInvalidRole       = errors.New("project: invalid role")
	ErrPersonalImmutable = errors.New("project: personal project cannot be modified this way")
)

// CreateProjectOptions describes a new team project. Personal projects are
// created via EnsurePersonalProject and bypass these options.
type CreateProjectOptions struct {
	Name        string
	Slug        string // optional; derived from Name if empty
	OwnerUserID string
	TeamID      string // optional
	Kind        ProjectKind
}

// InviteOptions describes a username-targeted invitation.
type InviteOptions struct {
	ProjectID  string
	InviterID  string
	Username   string
	UserSource UserSource // optional; if empty, the first matching user wins
	Role       Role
	ExpiresIn  time.Duration // 0 = no expiry
}

// UpdateRoleOptions describes a role change.
type UpdateRoleOptions struct {
	ProjectID    string
	ActorUserID  string
	TargetUserID string
	NewRole      Role
}

// ProjectSummary is the lightweight projection returned by ListProjects.
type ProjectSummary struct {
	Project Project
	Role    Role
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify produces a lowercase, hyphen-separated slug suitable for URLs and
// filesystem paths. Empty input produces empty output (caller must handle).
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// newID returns a fresh UUIDv4 string.
func newID() string { return uuid.NewString() }
