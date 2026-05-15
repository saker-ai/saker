package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/project"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// teamJSON is the wire shape returned by team/* RPCs.
func teamJSON(t *project.Team) map[string]any {
	return map[string]any{
		"id":        t.ID,
		"name":      t.Name,
		"slug":      t.Slug,
		"ownerId":   t.OwnerUserID,
		"createdAt": t.CreatedAt,
	}
}

// handleTeamList returns every team the caller belongs to. The plumbing here
// is intentionally light because Team is a P1 placeholder model — full team
// administration ships in a later phase.
func (h *Handler) handleTeamList(ctx context.Context, req Request) Response {
	u, deny := h.resolveCurrentUser(ctx, req.ID)
	if deny != nil {
		return *deny
	}
	db := h.projects.DB()
	if db == nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, "project store not initialised")
	}
	var rows []project.TeamMember
	if err := db.WithContext(ctx).Where("user_id = ?", u.ID).Find(&rows).Error; err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	teams := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		var t project.Team
		if err := db.WithContext(ctx).First(&t, "id = ?", m.TeamID).Error; err != nil {
			continue
		}
		entry := teamJSON(&t)
		entry["role"] = string(m.Role)
		teams = append(teams, entry)
	}
	return h.success(req.ID, map[string]any{"teams": teams})
}

// handleTeamCreate creates a Team and inserts the caller as RoleOwner.
func (h *Handler) handleTeamCreate(ctx context.Context, req Request) Response {
	u, deny := h.resolveCurrentUser(ctx, req.ID)
	if deny != nil {
		return *deny
	}
	var params struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	raw, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(raw, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return h.invalidParams(req.ID, "name is required")
	}
	slug := strings.TrimSpace(params.Slug)
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	}
	t := project.Team{
		ID:          uuid.NewString(),
		Name:        name,
		Slug:        slug,
		OwnerUserID: u.ID,
	}
	db := h.projects.DB()
	if db == nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, "project store not initialised")
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&t).Error; err != nil {
			return fmt.Errorf("create team: %w", err)
		}
		tm := project.TeamMember{
			TeamID:    t.ID,
			UserID:    u.ID,
			Role:      project.RoleOwner,
			InvitedBy: u.ID,
			JoinedAt:  time.Now(),
		}
		if err := tx.Create(&tm).Error; err != nil {
			return fmt.Errorf("create team owner: %w", err)
		}
		return nil
	})
	if err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	return h.success(req.ID, teamJSON(&t))
}

// handleTeamDelete removes a team and its memberships. Owner only.
func (h *Handler) handleTeamDelete(ctx context.Context, req Request) Response {
	u, deny := h.resolveCurrentUser(ctx, req.ID)
	if deny != nil {
		return *deny
	}
	teamID, _ := req.Params["teamId"].(string)
	if strings.TrimSpace(teamID) == "" {
		return h.invalidParams(req.ID, "teamId is required")
	}
	db := h.projects.DB()
	if db == nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, "project store not initialised")
	}
	var t project.Team
	if err := db.WithContext(ctx).First(&t, "id = ?", teamID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return h.invalidParams(req.ID, "team not found")
		}
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	if t.OwnerUserID != u.ID {
		return h.errorResp(req.ID, ErrCodeProjectAccess, "only the owner can delete a team")
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("team_id = ?", teamID).Delete(&project.TeamMember{}).Error; err != nil {
			return err
		}
		return tx.Delete(&project.Team{}, "id = ?", teamID).Error
	})
	if err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// handleTeamMemberList returns every member of the named team. Caller must
// be a member of the team.
func (h *Handler) handleTeamMemberList(ctx context.Context, req Request) Response {
	u, deny := h.resolveCurrentUser(ctx, req.ID)
	if deny != nil {
		return *deny
	}
	teamID, _ := req.Params["teamId"].(string)
	if strings.TrimSpace(teamID) == "" {
		return h.invalidParams(req.ID, "teamId is required")
	}
	db := h.projects.DB()
	if db == nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, "project store not initialised")
	}
	var caller project.TeamMember
	if err := db.WithContext(ctx).Where("team_id = ? AND user_id = ?", teamID, u.ID).First(&caller).Error; err != nil {
		return h.errorResp(req.ID, ErrCodeProjectAccess, "not a member of team")
	}
	var rows []project.TeamMember
	if err := db.WithContext(ctx).Where("team_id = ?", teamID).Find(&rows).Error; err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		usr, _ := h.projects.GetUser(ctx, rows[i].UserID)
		entry := map[string]any{
			"teamId":   rows[i].TeamID,
			"userId":   rows[i].UserID,
			"role":     string(rows[i].Role),
			"joinedAt": rows[i].JoinedAt,
		}
		if usr != nil {
			entry["username"] = usr.Username
			entry["displayName"] = usr.DisplayName
		}
		out = append(out, entry)
	}
	return h.success(req.ID, map[string]any{"members": out})
}
