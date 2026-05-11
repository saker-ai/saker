// service_apps.go: project (app) CRUD, listing, soft-delete, and metadata.
//
// In this codebase the "app" unit is a Project: the top-level workspace a
// user owns or is a member of. This file holds project create/get/list/
// update/delete operations. Membership and role machinery live in
// service_users.go.
package project

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

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
