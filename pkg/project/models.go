// Package project provides multi-tenant primitives: users, teams, projects,
// memberships, and invitations. It owns a GORM-backed metadata store and
// resolves per-request scope (current project, role, on-disk paths) so the
// rest of the server can stay project-agnostic.
package project

import (
	"time"
)

// Role enumerates a user's permission level inside a single project.
//
// owner  : full control, including deletion and ownership transfer.
// admin  : manage members and settings; cannot delete the project.
// member : create/edit threads, canvas, settings reads.
// viewer : read-only.
//
// Roles are ordered: a higher role implies all lower-role permissions.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

// rank returns a comparable integer for role precedence (higher = stronger).
func (r Role) rank() int {
	switch r {
	case RoleOwner:
		return 4
	case RoleAdmin:
		return 3
	case RoleMember:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// AtLeast reports whether r meets or exceeds min.
func (r Role) AtLeast(min Role) bool { return r.rank() >= min.rank() }

// Valid reports whether r is one of the four defined roles.
func (r Role) Valid() bool { return r.rank() > 0 }

// UserSource records where a user record originated. Used to keep external
// auth state aligned with the local users table without colliding usernames.
type UserSource string

const (
	UserSourceLocal     UserSource = "local"
	UserSourceLDAP      UserSource = "ldap"
	UserSourceOIDC      UserSource = "oidc"
	UserSourceLocalhost UserSource = "localhost"
)

// ProjectKind separates personal (one per user, auto-created) projects from
// team projects (collaborative, explicitly created).
type ProjectKind string

const (
	ProjectKindPersonal ProjectKind = "personal"
	ProjectKindTeam     ProjectKind = "team"
)

// User is a registered identity. The username is stable within a Source —
// (Source, Username) is unique. ID is a UUID assigned at insert time.
type User struct {
	ID          string     `gorm:"primaryKey;size:36"`
	Username    string     `gorm:"size:128;not null;uniqueIndex:idx_user_source_username,priority:2"`
	DisplayName string     `gorm:"size:255"`
	Email       string     `gorm:"size:255;index"`
	AvatarURL   string     `gorm:"size:512"`
	Source      UserSource `gorm:"size:32;not null;uniqueIndex:idx_user_source_username,priority:1;index"`
	ExternalID  string     `gorm:"size:255;index"` // upstream subject for OIDC/LDAP
	GlobalRole  string     `gorm:"size:32;default:user"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Team groups projects under a shared owner. Optional in P1; present so the
// schema doesn't need a breaking migration once team-level features land.
type Team struct {
	ID          string `gorm:"primaryKey;size:36"`
	Name        string `gorm:"size:255;not null"`
	Slug        string `gorm:"size:128;not null;uniqueIndex"`
	OwnerUserID string `gorm:"size:36;not null;index"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TeamMember binds a user to a team with a role.
type TeamMember struct {
	TeamID    string    `gorm:"primaryKey;size:36"`
	UserID    string    `gorm:"primaryKey;size:36;index"`
	Role      Role      `gorm:"size:16;not null"`
	InvitedBy string    `gorm:"size:36"`
	JoinedAt  time.Time `gorm:"not null"`
}

// Project is the unit of data isolation. Every thread, canvas document,
// memory file, and per-project setting lives under
// `<projectRoot>/.saker/projects/<ID>/`.
type Project struct {
	ID          string      `gorm:"primaryKey;size:36"`
	Name        string      `gorm:"size:255;not null"`
	Slug        string      `gorm:"size:128;not null;uniqueIndex"`
	OwnerUserID string      `gorm:"size:36;not null;index"`
	TeamID      string      `gorm:"size:36;index"` // empty for personal
	Kind        ProjectKind `gorm:"size:16;not null;index"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time `gorm:"index"` // soft delete
}

// ProjectMember binds a user to a project with a role. (ProjectID, UserID)
// is the natural key — uniqueness is enforced via composite primary key.
type ProjectMember struct {
	ProjectID string    `gorm:"primaryKey;size:36"`
	UserID    string    `gorm:"primaryKey;size:36;index"`
	Role      Role      `gorm:"size:16;not null"`
	InvitedBy string    `gorm:"size:36"`
	JoinedAt  time.Time `gorm:"not null"`
}

// InviteStatus is the lifecycle of a project invitation.
type InviteStatus string

const (
	InviteStatusPending  InviteStatus = "pending"
	InviteStatusAccepted InviteStatus = "accepted"
	InviteStatusRevoked  InviteStatus = "revoked"
	InviteStatusExpired  InviteStatus = "expired"
	// Declined is set by the invitee themselves via project/invite/decline.
	// Distinct from Revoked (admin-side cancel) so audit logs preserve who
	// closed the invite.
	InviteStatusDeclined InviteStatus = "declined"
)

// Invite is a username-targeted invitation. The invitee must already exist in
// the users table (P1 decision; email-based invites would extend this later).
type Invite struct {
	ID         string       `gorm:"primaryKey;size:36"`
	ProjectID  string       `gorm:"size:36;not null;index"`
	Username   string       `gorm:"size:128;not null;index"` // denormalized for quick lookup
	UserID     string       `gorm:"size:36;index"`           // resolved at invite time
	Role       Role         `gorm:"size:16;not null"`
	InvitedBy  string       `gorm:"size:36;not null"`
	Status     InviteStatus `gorm:"size:16;not null;index"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ExpiresAt  *time.Time
	AcceptedAt *time.Time
}

// AllModels returns every GORM model managed by the project store. Used by
// AutoMigrate at startup and by tests that reset the schema.
func AllModels() []any {
	return []any{
		&User{},
		&Team{},
		&TeamMember{},
		&Project{},
		&ProjectMember{},
		&Invite{},
	}
}
