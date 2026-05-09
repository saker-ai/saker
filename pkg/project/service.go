package project

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
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

// EnsureUserFromAuth upserts a (Source, Username) user record. Used by the
// Local/LDAP/OIDC login flows so the users table stays aligned with whichever
// identity provider authenticated the request. ExternalID, DisplayName, and
// Email are refreshed on every call so renames upstream propagate.
func (s *Store) EnsureUserFromAuth(ctx context.Context, source UserSource, username, externalID, displayName, email string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("project: username required")
	}
	// Serialize concurrent first-request bursts for the same identity so we
	// don't double-create user rows; the second caller will see the row the
	// first caller wrote.
	defer s.provisioningLock("user:" + string(source) + ":" + username)()
	var u User
	err := s.db.WithContext(ctx).
		Where("source = ? AND username = ?", source, username).
		First(&u).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		u = User{
			ID:          newID(),
			Username:    username,
			DisplayName: displayName,
			Email:       email,
			Source:      source,
			ExternalID:  externalID,
		}
		if err := s.db.WithContext(ctx).Create(&u).Error; err != nil {
			return nil, fmt.Errorf("project: create user: %w", err)
		}
		return &u, nil
	case err != nil:
		return nil, fmt.Errorf("project: lookup user: %w", err)
	}
	// Refresh mutable fields if they've changed.
	updates := map[string]any{}
	if displayName != "" && displayName != u.DisplayName {
		updates["display_name"] = displayName
	}
	if email != "" && email != u.Email {
		updates["email"] = email
	}
	if externalID != "" && externalID != u.ExternalID {
		updates["external_id"] = externalID
	}
	if len(updates) > 0 {
		if err := s.db.WithContext(ctx).Model(&u).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("project: update user: %w", err)
		}
	}
	return &u, nil
}

// EnsureLocalhostUser returns the localhost-mode user for the given OS uid,
// creating it on first call. Username is `local-<uid>` so multiple OS users on
// the same machine don't collide. Always granted GlobalRole=admin.
func (s *Store) EnsureLocalhostUser(ctx context.Context, osUID string) (*User, error) {
	osUID = strings.TrimSpace(osUID)
	if osUID == "" {
		osUID = "default"
	}
	username := "local-" + osUID
	u, err := s.EnsureUserFromAuth(ctx, UserSourceLocalhost, username, osUID, "Localhost ("+osUID+")", "")
	if err != nil {
		return nil, err
	}
	if u.GlobalRole != "admin" {
		if err := s.db.WithContext(ctx).Model(u).Update("global_role", "admin").Error; err != nil {
			return nil, fmt.Errorf("project: promote localhost user: %w", err)
		}
		u.GlobalRole = "admin"
	}
	return u, nil
}

// EnsurePersonalProject returns the user's personal project, creating it on
// first call. Slug is `personal-<username>`; collisions are extremely unlikely
// because (Source, Username) is already unique.
func (s *Store) EnsurePersonalProject(ctx context.Context, userID string) (*Project, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, errors.New("project: userID required")
	}
	// Serialize per-user so concurrent first-request bursts don't both pass
	// the SELECT then both attempt CREATE on the unique slug.
	defer s.provisioningLock("personal:" + userID)()
	var u User
	if err := s.db.WithContext(ctx).First(&u, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("project: lookup user: %w", err)
	}

	var p Project
	err := s.db.WithContext(ctx).
		Where("owner_user_id = ? AND kind = ? AND deleted_at IS NULL", userID, ProjectKindPersonal).
		First(&p).Error
	if err == nil {
		return &p, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("project: lookup personal project: %w", err)
	}

	slug := "personal-" + slugify(u.Username)
	if slug == "personal-" {
		slug = "personal-" + userID[:8]
	}
	p = Project{
		ID:          newID(),
		Name:        u.DisplayName,
		Slug:        slug,
		OwnerUserID: userID,
		Kind:        ProjectKindPersonal,
	}
	if p.Name == "" {
		p.Name = u.Username
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&p).Error; err != nil {
			return fmt.Errorf("create personal project: %w", err)
		}
		pm := ProjectMember{
			ProjectID: p.ID,
			UserID:    userID,
			Role:      RoleOwner,
			InvitedBy: userID,
			JoinedAt:  time.Now(),
		}
		if err := tx.Create(&pm).Error; err != nil {
			return fmt.Errorf("create personal owner membership: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// CreateProject creates a new team project and inserts the owner as a
// RoleOwner ProjectMember in the same transaction.
func (s *Store) CreateProject(ctx context.Context, opts CreateProjectOptions) (*Project, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return nil, errors.New("project: name required")
	}
	if strings.TrimSpace(opts.OwnerUserID) == "" {
		return nil, errors.New("project: owner required")
	}
	kind := opts.Kind
	if kind == "" {
		kind = ProjectKindTeam
	}
	slug := strings.TrimSpace(opts.Slug)
	if slug == "" {
		slug = slugify(name)
	}
	if slug == "" {
		slug = "project"
	}

	// Verify owner exists.
	if err := s.db.WithContext(ctx).First(&User{}, "id = ?", opts.OwnerUserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("project: lookup owner: %w", err)
	}

	p := Project{
		ID:          newID(),
		Name:        name,
		Slug:        s.uniqueSlug(ctx, slug),
		OwnerUserID: opts.OwnerUserID,
		TeamID:      opts.TeamID,
		Kind:        kind,
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&p).Error; err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		pm := ProjectMember{
			ProjectID: p.ID,
			UserID:    opts.OwnerUserID,
			Role:      RoleOwner,
			InvitedBy: opts.OwnerUserID,
			JoinedAt:  time.Now(),
		}
		if err := tx.Create(&pm).Error; err != nil {
			return fmt.Errorf("create owner membership: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// uniqueSlug returns the base slug if available, otherwise appends a short
// suffix until the DB no longer reports a collision. This is a friendlier
// first attempt than relying solely on the DB unique constraint.
func (s *Store) uniqueSlug(ctx context.Context, base string) string {
	candidate := base
	for i := 0; i < 8; i++ {
		var count int64
		err := s.db.WithContext(ctx).
			Model(&Project{}).
			Where("slug = ?", candidate).
			Count(&count).Error
		if err != nil {
			return candidate + "-" + uuid.NewString()[:8]
		}
		if count == 0 {
			return candidate
		}
		candidate = base + "-" + uuid.NewString()[:8]
	}
	return candidate
}

// ListProjects returns every non-deleted project the user is a member of,
// the user's role in each.
func (s *Store) ListProjects(ctx context.Context, userID string) ([]ProjectSummary, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, errors.New("project: userID required")
	}
	rows, err := s.db.WithContext(ctx).
		Table("project_members AS pm").
		Select("projects.*, pm.role AS member_role").
		Joins("JOIN projects ON projects.id = pm.project_id").
		Where("pm.user_id = ? AND projects.deleted_at IS NULL", userID).
		Order("projects.created_at ASC").
		Rows()
	if err != nil {
		return nil, fmt.Errorf("project: list: %w", err)
	}
	defer rows.Close()

	var out []ProjectSummary
	for rows.Next() {
		var (
			p          Project
			memberRole string
		)
		if err := s.db.ScanRows(rows, &p); err != nil {
			return nil, fmt.Errorf("project: scan: %w", err)
		}
		// ScanRows can't fill the alias; pull role directly.
		// (Cheap second-pass but avoids a custom struct.)
		if err := s.db.WithContext(ctx).
			Model(&ProjectMember{}).
			Select("role").
			Where("project_id = ? AND user_id = ?", p.ID, userID).
			Scan(&memberRole).Error; err != nil {
			return nil, fmt.Errorf("project: scan role: %w", err)
		}
		out = append(out, ProjectSummary{Project: p, Role: Role(memberRole)})
	}
	return out, nil
}

// ListAllProjects returns every non-deleted project across all users. Intended
// for server-wide background tasks (cleanup loops, audits) that need to walk
// every tenant. Use ListProjects when you need a single user's view.
func (s *Store) ListAllProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	err := s.db.WithContext(ctx).
		Where("deleted_at IS NULL").
		Order("created_at ASC").
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("project: list all: %w", err)
	}
	return out, nil
}

// GetProject loads a project by ID. Returns ErrProjectNotFound if missing or
// soft-deleted.
func (s *Store) GetProject(ctx context.Context, projectID string) (*Project, error) {
	var p Project
	err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", projectID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("project: get: %w", err)
	}
	return &p, nil
}

// GetMember returns the membership row for (project, user). ErrNotMember if
// no row exists.
func (s *Store) GetMember(ctx context.Context, projectID, userID string) (*ProjectMember, error) {
	var pm ProjectMember
	err := s.db.WithContext(ctx).
		Where("project_id = ? AND user_id = ?", projectID, userID).
		First(&pm).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotMember
	}
	if err != nil {
		return nil, fmt.Errorf("project: get member: %w", err)
	}
	return &pm, nil
}

// ListMembers returns every membership row for a project, joined with users.
func (s *Store) ListMembers(ctx context.Context, projectID string) ([]ProjectMember, error) {
	var out []ProjectMember
	err := s.db.WithContext(ctx).
		Where("project_id = ?", projectID).
		Order("joined_at ASC").
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("project: list members: %w", err)
	}
	return out, nil
}

// LookupUserByUsername finds a user by username, optionally filtered by
// source. When source is empty, the first match (in source priority order:
// local, oidc, ldap, localhost) wins.
func (s *Store) LookupUserByUsername(ctx context.Context, username string, source UserSource) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, ErrUserNotFound
	}
	q := s.db.WithContext(ctx).Where("username = ?", username)
	if source != "" {
		q = q.Where("source = ?", source)
	}
	var u User
	err := q.First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("project: lookup username: %w", err)
	}
	return &u, nil
}

// GetUser loads a user by ID.
func (s *Store) GetUser(ctx context.Context, userID string) (*User, error) {
	var u User
	err := s.db.WithContext(ctx).First(&u, "id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("project: get user: %w", err)
	}
	return &u, nil
}

// InviteByUsername creates a pending Invite. The invitee must already exist
// in the users table. The inviter must be admin or owner of the project.
// Returns ErrAlreadyMember if the target is already a member.
func (s *Store) InviteByUsername(ctx context.Context, opts InviteOptions) (*Invite, error) {
	if !opts.Role.Valid() {
		return nil, ErrInvalidRole
	}
	// Inviter capability check.
	inviter, err := s.GetMember(ctx, opts.ProjectID, opts.InviterID)
	if err != nil {
		return nil, err
	}
	if !inviter.Role.AtLeast(RoleAdmin) {
		return nil, ErrInsufficientRole
	}
	// Cannot grant a role >= your own.
	if !inviter.Role.AtLeast(opts.Role) {
		return nil, ErrInsufficientRole
	}

	target, err := s.LookupUserByUsername(ctx, opts.Username, opts.UserSource)
	if err != nil {
		return nil, err
	}
	if target.ID == opts.InviterID {
		return nil, ErrSelfInvite
	}
	// Already a member?
	if _, err := s.GetMember(ctx, opts.ProjectID, target.ID); err == nil {
		return nil, ErrAlreadyMember
	} else if !errors.Is(err, ErrNotMember) {
		return nil, err
	}

	now := time.Now()
	inv := Invite{
		ID:        newID(),
		ProjectID: opts.ProjectID,
		Username:  target.Username,
		UserID:    target.ID,
		Role:      opts.Role,
		InvitedBy: opts.InviterID,
		Status:    InviteStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if opts.ExpiresIn > 0 {
		t := now.Add(opts.ExpiresIn)
		inv.ExpiresAt = &t
	}
	if err := s.db.WithContext(ctx).Create(&inv).Error; err != nil {
		return nil, fmt.Errorf("project: create invite: %w", err)
	}
	return &inv, nil
}

// AcceptInvite resolves an invite and creates the corresponding ProjectMember.
// The accepting user must match the invite's UserID.
func (s *Store) AcceptInvite(ctx context.Context, inviteID, userID string) (*ProjectMember, error) {
	var inv Invite
	if err := s.db.WithContext(ctx).First(&inv, "id = ?", inviteID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("project: lookup invite: %w", err)
	}
	if inv.UserID != userID {
		return nil, ErrInviteWrongUser
	}
	if inv.Status != InviteStatusPending {
		return nil, ErrInviteNotPending
	}
	if inv.ExpiresAt != nil && time.Now().After(*inv.ExpiresAt) {
		_ = s.db.WithContext(ctx).Model(&inv).Update("status", InviteStatusExpired).Error
		return nil, ErrInviteNotPending
	}

	pm := ProjectMember{
		ProjectID: inv.ProjectID,
		UserID:    userID,
		Role:      inv.Role,
		InvitedBy: inv.InvitedBy,
		JoinedAt:  time.Now(),
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&pm).Error; err != nil {
			return fmt.Errorf("create membership: %w", err)
		}
		now := time.Now()
		if err := tx.Model(&inv).Updates(map[string]any{
			"status":      InviteStatusAccepted,
			"accepted_at": &now,
			"updated_at":  now,
		}).Error; err != nil {
			return fmt.Errorf("mark invite accepted: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &pm, nil
}

// DeclineInvite lets the invitee themselves refuse a pending invite. The
// invitee must match the invite's UserID — distinct from CancelInvite, which
// is the admin-side path. Idempotent: declining an already-non-pending invite
// returns ErrInviteNotPending so the UI can surface "this invite is no
// longer available" rather than silently accept the click.
func (s *Store) DeclineInvite(ctx context.Context, inviteID, userID string) error {
	var inv Invite
	if err := s.db.WithContext(ctx).First(&inv, "id = ?", inviteID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInviteNotFound
		}
		return fmt.Errorf("project: lookup invite: %w", err)
	}
	if inv.UserID != userID {
		return ErrInviteWrongUser
	}
	if inv.Status != InviteStatusPending {
		return ErrInviteNotPending
	}
	return s.db.WithContext(ctx).Model(&inv).Updates(map[string]any{
		"status":     InviteStatusDeclined,
		"updated_at": time.Now(),
	}).Error
}

// CancelInvite revokes a pending invite. The actor must be admin or owner.
func (s *Store) CancelInvite(ctx context.Context, inviteID, actorUserID string) error {
	var inv Invite
	if err := s.db.WithContext(ctx).First(&inv, "id = ?", inviteID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInviteNotFound
		}
		return fmt.Errorf("project: lookup invite: %w", err)
	}
	actor, err := s.GetMember(ctx, inv.ProjectID, actorUserID)
	if err != nil {
		return err
	}
	if !actor.Role.AtLeast(RoleAdmin) {
		return ErrInsufficientRole
	}
	return s.db.WithContext(ctx).Model(&inv).Updates(map[string]any{
		"status":     InviteStatusRevoked,
		"updated_at": time.Now(),
	}).Error
}

// ListInvites returns all invites for a project, optionally filtered by
// status. Pass empty status to get all.
func (s *Store) ListInvites(ctx context.Context, projectID string, status InviteStatus) ([]Invite, error) {
	q := s.db.WithContext(ctx).Where("project_id = ?", projectID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var out []Invite
	if err := q.Order("created_at DESC").Find(&out).Error; err != nil {
		return nil, fmt.Errorf("project: list invites: %w", err)
	}
	return out, nil
}

// ListInvitesForUser returns pending invites addressed to the given user.
func (s *Store) ListInvitesForUser(ctx context.Context, userID string) ([]Invite, error) {
	var out []Invite
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, InviteStatusPending).
		Order("created_at DESC").
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("project: list user invites: %w", err)
	}
	return out, nil
}

// UpdateRole changes a member's role. The actor must outrank both the current
// and new role of the target. The sole owner cannot be demoted (use
// TransferOwnership for that).
func (s *Store) UpdateRole(ctx context.Context, opts UpdateRoleOptions) error {
	if !opts.NewRole.Valid() {
		return ErrInvalidRole
	}
	actor, err := s.GetMember(ctx, opts.ProjectID, opts.ActorUserID)
	if err != nil {
		return err
	}
	if !actor.Role.AtLeast(RoleAdmin) {
		return ErrInsufficientRole
	}
	target, err := s.GetMember(ctx, opts.ProjectID, opts.TargetUserID)
	if err != nil {
		return err
	}
	// Actor cannot grant a role higher than their own, nor modify someone
	// who outranks them.
	if !actor.Role.AtLeast(opts.NewRole) || target.Role.rank() > actor.Role.rank() {
		return ErrInsufficientRole
	}
	if target.Role == RoleOwner && opts.NewRole != RoleOwner {
		count, err := s.countOwners(ctx, opts.ProjectID)
		if err != nil {
			return err
		}
		if count <= 1 {
			return ErrSoleOwner
		}
	}
	return s.db.WithContext(ctx).
		Model(&ProjectMember{}).
		Where("project_id = ? AND user_id = ?", opts.ProjectID, opts.TargetUserID).
		Update("role", opts.NewRole).Error
}

// RemoveMember removes a member. Owners can remove anyone (except the sole
// owner); admins can remove members and viewers; members may remove
// themselves only.
func (s *Store) RemoveMember(ctx context.Context, projectID, actorUserID, targetUserID string) error {
	actor, err := s.GetMember(ctx, projectID, actorUserID)
	if err != nil {
		return err
	}
	target, err := s.GetMember(ctx, projectID, targetUserID)
	if err != nil {
		return err
	}
	selfRemoval := actorUserID == targetUserID
	if !selfRemoval {
		if !actor.Role.AtLeast(RoleAdmin) {
			return ErrInsufficientRole
		}
		if target.Role.rank() >= actor.Role.rank() {
			return ErrInsufficientRole
		}
	}
	if target.Role == RoleOwner {
		count, err := s.countOwners(ctx, projectID)
		if err != nil {
			return err
		}
		if count <= 1 {
			return ErrSoleOwner
		}
	}
	return s.db.WithContext(ctx).
		Where("project_id = ? AND user_id = ?", projectID, targetUserID).
		Delete(&ProjectMember{}).Error
}

func (s *Store) countOwners(ctx context.Context, projectID string) (int64, error) {
	var c int64
	err := s.db.WithContext(ctx).
		Model(&ProjectMember{}).
		Where("project_id = ? AND role = ?", projectID, RoleOwner).
		Count(&c).Error
	return c, err
}

// SoftDeleteProject marks a project deleted. Only owners may delete; personal
// projects cannot be deleted via this API (they live as long as the user).
func (s *Store) SoftDeleteProject(ctx context.Context, projectID, actorUserID string) error {
	p, err := s.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	if p.Kind == ProjectKindPersonal {
		return ErrPersonalImmutable
	}
	actor, err := s.GetMember(ctx, projectID, actorUserID)
	if err != nil {
		return err
	}
	if actor.Role != RoleOwner {
		return ErrInsufficientRole
	}
	now := time.Now()
	return s.db.WithContext(ctx).
		Model(&Project{}).
		Where("id = ?", projectID).
		Update("deleted_at", &now).Error
}

// TransferOwnership hands the owner role from the current owner to a target
// member. The previous owner is downgraded to admin in the same transaction
// so the project never has zero owners and never has two (callers can use
// UpdateRole afterwards if a different demotion is desired).
//
// Constraints:
//   - actor must currently hold RoleOwner on the project
//   - target must already be a member (use Invite first)
//   - personal projects cannot have their owner transferred
//   - target == actor is a no-op (returns nil)
func (s *Store) TransferOwnership(ctx context.Context, projectID, actorUserID, targetUserID string) error {
	actorUserID = strings.TrimSpace(actorUserID)
	targetUserID = strings.TrimSpace(targetUserID)
	if actorUserID == "" || targetUserID == "" {
		return errors.New("project: actorUserID and targetUserID required")
	}
	if actorUserID == targetUserID {
		return nil
	}
	p, err := s.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	if p.Kind == ProjectKindPersonal {
		return ErrPersonalImmutable
	}
	actor, err := s.GetMember(ctx, projectID, actorUserID)
	if err != nil {
		return err
	}
	if actor.Role != RoleOwner {
		return ErrInsufficientRole
	}
	target, err := s.GetMember(ctx, projectID, targetUserID)
	if err != nil {
		// Bubble up ErrNotMember unchanged so the handler maps it to a
		// helpful "user is not a project member" message instead of generic
		// store error.
		return err
	}
	if target.Role == RoleOwner {
		// Already the owner — caller likely raced with another transfer.
		// Treating as no-op keeps the API idempotent.
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ProjectMember{}).
			Where("project_id = ? AND user_id = ?", projectID, targetUserID).
			Update("role", RoleOwner).Error; err != nil {
			return err
		}
		return tx.Model(&ProjectMember{}).
			Where("project_id = ? AND user_id = ?", projectID, actorUserID).
			Update("role", RoleAdmin).Error
	})
}

// UpdateProjectMeta updates name/slug. Admin or owner only.
func (s *Store) UpdateProjectMeta(ctx context.Context, projectID, actorUserID, name, slug string) error {
	actor, err := s.GetMember(ctx, projectID, actorUserID)
	if err != nil {
		return err
	}
	if !actor.Role.AtLeast(RoleAdmin) {
		return ErrInsufficientRole
	}
	updates := map[string]any{}
	if strings.TrimSpace(name) != "" {
		updates["name"] = strings.TrimSpace(name)
	}
	if strings.TrimSpace(slug) != "" {
		updates["slug"] = s.uniqueSlug(ctx, slugify(slug))
	}
	if len(updates) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).
		Model(&Project{}).
		Where("id = ?", projectID).
		Updates(updates).Error
}
