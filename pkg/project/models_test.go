package project

import (
	"fmt"
	"testing"
	"time"
)

// --- Role tests ---------------------------------------------------------

func TestModelsRoleRank(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role Role
		want int
	}{
		{RoleOwner, 4},
		{RoleAdmin, 3},
		{RoleMember, 2},
		{RoleViewer, 1},
		{Role("unknown"), 0},
		{Role(""), 0},
	}
	for _, c := range cases {
		if got := c.role.rank(); got != c.want {
			t.Errorf("Role(%q).rank() = %d, want %d", c.role, got, c.want)
		}
	}
}

func TestModelsRoleAtLeast(t *testing.T) {
	t.Parallel()
	cases := []struct {
		r    Role
		min  Role
		want bool
	}{
		// Exact matches.
		{RoleOwner, RoleOwner, true},
		{RoleAdmin, RoleAdmin, true},
		{RoleMember, RoleMember, true},
		{RoleViewer, RoleViewer, true},
		// Higher role meets lower minimum.
		{RoleOwner, RoleAdmin, true},
		{RoleOwner, RoleMember, true},
		{RoleOwner, RoleViewer, true},
		{RoleAdmin, RoleMember, true},
		{RoleAdmin, RoleViewer, true},
		{RoleMember, RoleViewer, true},
		// Lower role does not meet higher minimum.
		{RoleAdmin, RoleOwner, false},
		{RoleMember, RoleOwner, false},
		{RoleMember, RoleAdmin, false},
		{RoleViewer, RoleOwner, false},
		{RoleViewer, RoleAdmin, false},
		{RoleViewer, RoleMember, false},
		// Invalid/empty roles: both rank 0.
		{Role(""), Role(""), true},
		{Role(""), RoleViewer, false},
		{RoleOwner, Role(""), true},
		{Role("unknown"), Role("other"), true},
		{Role("unknown"), RoleViewer, false},
	}
	for _, c := range cases {
		if got := c.r.AtLeast(c.min); got != c.want {
			t.Errorf("Role(%q).AtLeast(%q) = %v, want %v", c.r, c.min, got, c.want)
		}
	}
}

func TestModelsRoleValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role Role
		want bool
	}{
		{RoleOwner, true},
		{RoleAdmin, true},
		{RoleMember, true},
		{RoleViewer, true},
		{Role(""), false},
		{Role("superadmin"), false},
		{Role("Owner"), false}, // case-sensitive
	}
	for _, c := range cases {
		if got := c.role.Valid(); got != c.want {
			t.Errorf("Role(%q).Valid() = %v, want %v", c.role, got, c.want)
		}
	}
}

// --- UserSource tests ---------------------------------------------------

func TestModelsUserSourceConstants(t *testing.T) {
	t.Parallel()
	if UserSourceLocal != "local" {
		t.Errorf("UserSourceLocal = %q, want %q", UserSourceLocal, "local")
	}
	if UserSourceLDAP != "ldap" {
		t.Errorf("UserSourceLDAP = %q, want %q", UserSourceLDAP, "ldap")
	}
	if UserSourceOIDC != "oidc" {
		t.Errorf("UserSourceOIDC = %q, want %q", UserSourceOIDC, "oidc")
	}
	if UserSourceLocalhost != "localhost" {
		t.Errorf("UserSourceLocalhost = %q, want %q", UserSourceLocalhost, "localhost")
	}
}

// --- ProjectKind tests -------------------------------------------------

func TestModelsProjectKindConstants(t *testing.T) {
	t.Parallel()
	if ProjectKindPersonal != "personal" {
		t.Errorf("ProjectKindPersonal = %q, want %q", ProjectKindPersonal, "personal")
	}
	if ProjectKindTeam != "team" {
		t.Errorf("ProjectKindTeam = %q, want %q", ProjectKindTeam, "team")
	}
}

// --- InviteStatus tests ------------------------------------------------

func TestModelsInviteStatusConstants(t *testing.T) {
	t.Parallel()
	if InviteStatusPending != "pending" {
		t.Errorf("InviteStatusPending = %q, want %q", InviteStatusPending, "pending")
	}
	if InviteStatusAccepted != "accepted" {
		t.Errorf("InviteStatusAccepted = %q, want %q", InviteStatusAccepted, "accepted")
	}
	if InviteStatusRevoked != "revoked" {
		t.Errorf("InviteStatusRevoked = %q, want %q", InviteStatusRevoked, "revoked")
	}
	if InviteStatusExpired != "expired" {
		t.Errorf("InviteStatusExpired = %q, want %q", InviteStatusExpired, "expired")
	}
	if InviteStatusDeclined != "declined" {
		t.Errorf("InviteStatusDeclined = %q, want %q", InviteStatusDeclined, "declined")
	}
}

// --- Struct field tests ------------------------------------------------

func TestModelsUserStruct(t *testing.T) {
	t.Parallel()
	now := time.Now()
	u := User{
		ID:          "id-1",
		Username:    "alice",
		DisplayName: "Alice Smith",
		Email:       "alice@example.com",
		AvatarURL:   "https://img.example.com/alice",
		Source:      UserSourceLocal,
		ExternalID:  "ext-123",
		GlobalRole:  "admin",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if u.ID != "id-1" {
		t.Errorf("User.ID = %q, want %q", u.ID, "id-1")
	}
	if u.Username != "alice" {
		t.Errorf("User.Username = %q, want %q", u.Username, "alice")
	}
	if u.Source != UserSourceLocal {
		t.Errorf("User.Source = %q, want %q", u.Source, UserSourceLocal)
	}
	if u.GlobalRole != "admin" {
		t.Errorf("User.GlobalRole = %q, want %q", u.GlobalRole, "admin")
	}
	// Verify zero-value defaults.
	z := User{}
	if z.ID != "" {
		t.Errorf("zero User.ID = %q, want empty", z.ID)
	}
	if z.Source != "" {
		t.Errorf("zero User.Source = %q, want empty", z.Source)
	}
	if z.GlobalRole != "" {
		t.Errorf("zero User.GlobalRole = %q, want empty", z.GlobalRole)
	}
}

func TestModelsTeamStruct(t *testing.T) {
	t.Parallel()
	now := time.Now()
	team := Team{
		ID:          "team-1",
		Name:        "Engineering",
		Slug:        "engineering",
		OwnerUserID: "user-1",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if team.ID != "team-1" {
		t.Errorf("Team.ID = %q, want %q", team.ID, "team-1")
	}
	if team.Slug != "engineering" {
		t.Errorf("Team.Slug = %q, want %q", team.Slug, "engineering")
	}
	if team.OwnerUserID != "user-1" {
		t.Errorf("Team.OwnerUserID = %q, want %q", team.OwnerUserID, "user-1")
	}
	z := Team{}
	if z.ID != "" {
		t.Errorf("zero Team.ID = %q, want empty", z.ID)
	}
}

func TestModelsTeamMemberStruct(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tm := TeamMember{
		TeamID:    "team-1",
		UserID:    "user-1",
		Role:      RoleAdmin,
		InvitedBy: "user-0",
		JoinedAt:  now,
	}
	if tm.TeamID != "team-1" {
		t.Errorf("TeamMember.TeamID = %q, want %q", tm.TeamID, "team-1")
	}
	if tm.UserID != "user-1" {
		t.Errorf("TeamMember.UserID = %q, want %q", tm.UserID, "user-1")
	}
	if tm.Role != RoleAdmin {
		t.Errorf("TeamMember.Role = %q, want %q", tm.Role, RoleAdmin)
	}
	if tm.InvitedBy != "user-0" {
		t.Errorf("TeamMember.InvitedBy = %q, want %q", tm.InvitedBy, "user-0")
	}
}

func TestModelsProjectStruct(t *testing.T) {
	t.Parallel()
	now := time.Now()
	p := Project{
		ID:          "proj-1",
		Name:        "Acme",
		Slug:        "acme",
		OwnerUserID: "user-1",
		TeamID:      "team-1",
		Kind:        ProjectKindTeam,
		CreatedAt:   now,
		UpdatedAt:   now,
		DeletedAt:   nil,
	}
	if p.ID != "proj-1" {
		t.Errorf("Project.ID = %q, want %q", p.ID, "proj-1")
	}
	if p.Kind != ProjectKindTeam {
		t.Errorf("Project.Kind = %q, want %q", p.Kind, ProjectKindTeam)
	}
	if p.DeletedAt != nil {
		t.Errorf("Project.DeletedAt = %v, want nil", p.DeletedAt)
	}
	// Personal project: TeamID empty.
	pp := Project{Kind: ProjectKindPersonal}
	if pp.TeamID != "" {
		t.Errorf("personal project TeamID = %q, want empty", pp.TeamID)
	}
	// Soft-deleted project: DeletedAt non-nil.
	delTime := now
	pd := Project{DeletedAt: &delTime}
	if pd.DeletedAt == nil {
		t.Error("soft-deleted project DeletedAt should be non-nil")
	}
}

func TestModelsProjectMemberStruct(t *testing.T) {
	t.Parallel()
	now := time.Now()
	pm := ProjectMember{
		ProjectID: "proj-1",
		UserID:    "user-1",
		Role:      RoleOwner,
		InvitedBy: "user-0",
		JoinedAt:  now,
	}
	if pm.ProjectID != "proj-1" {
		t.Errorf("ProjectMember.ProjectID = %q, want %q", pm.ProjectID, "proj-1")
	}
	if pm.UserID != "user-1" {
		t.Errorf("ProjectMember.UserID = %q, want %q", pm.UserID, "user-1")
	}
	if pm.Role != RoleOwner {
		t.Errorf("ProjectMember.Role = %q, want %q", pm.Role, RoleOwner)
	}
	if pm.InvitedBy != "user-0" {
		t.Errorf("ProjectMember.InvitedBy = %q, want %q", pm.InvitedBy, "user-0")
	}
}

func TestModelsInviteStruct(t *testing.T) {
	t.Parallel()
	now := time.Now()
	inv := Invite{
		ID:         "inv-1",
		ProjectID:  "proj-1",
		Username:   "bob",
		UserID:     "user-2",
		Role:       RoleMember,
		InvitedBy:  "user-1",
		Status:     InviteStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  nil,
		AcceptedAt: nil,
	}
	if inv.ID != "inv-1" {
		t.Errorf("Invite.ID = %q, want %q", inv.ID, "inv-1")
	}
	if inv.ProjectID != "proj-1" {
		t.Errorf("Invite.ProjectID = %q, want %q", inv.ProjectID, "proj-1")
	}
	if inv.Username != "bob" {
		t.Errorf("Invite.Username = %q, want %q", inv.Username, "bob")
	}
	if inv.UserID != "user-2" {
		t.Errorf("Invite.UserID = %q, want %q", inv.UserID, "user-2")
	}
	if inv.Role != RoleMember {
		t.Errorf("Invite.Role = %q, want %q", inv.Role, RoleMember)
	}
	if inv.Status != InviteStatusPending {
		t.Errorf("Invite.Status = %q, want %q", inv.Status, InviteStatusPending)
	}
	if inv.ExpiresAt != nil {
		t.Errorf("Invite.ExpiresAt = %v, want nil", inv.ExpiresAt)
	}
	if inv.AcceptedAt != nil {
		t.Errorf("Invite.AcceptedAt = %v, want nil", inv.AcceptedAt)
	}
	// Accepted invite: AcceptedAt non-nil.
	acceptTime := now
	ia := Invite{Status: InviteStatusAccepted, AcceptedAt: &acceptTime}
	if ia.AcceptedAt == nil {
		t.Error("accepted invite AcceptedAt should be non-nil")
	}
	// Expired invite: ExpiresAt non-nil.
	expTime := now.Add(24 * time.Hour)
	ie := Invite{Status: InviteStatusExpired, ExpiresAt: &expTime}
	if ie.ExpiresAt == nil {
		t.Error("expired invite ExpiresAt should be non-nil")
	}
}

// --- AllModels test ----------------------------------------------------

func TestModelsAllModels(t *testing.T) {
	t.Parallel()
	models := AllModels()
	if len(models) != 6 {
		t.Fatalf("AllModels() returned %d items, want 6", len(models))
	}
	// Verify each model type appears exactly once.
	wantTypes := map[string]int{
		"*project.User":          1,
		"*project.Team":          1,
		"*project.TeamMember":    1,
		"*project.Project":       1,
		"*project.ProjectMember": 1,
		"*project.Invite":        1,
	}
	gotTypes := map[string]int{}
	for _, m := range models {
		gotTypes[typeName(m)]++
	}
	for typ, want := range wantTypes {
		got := gotTypes[typ]
		if got != want {
			t.Errorf("AllModels() type %s: count %d, want %d", typ, got, want)
		}
	}
	// Verify no unexpected types.
	for typ := range gotTypes {
		if _, ok := wantTypes[typ]; !ok {
			t.Errorf("AllModels() contains unexpected type %s", typ)
		}
	}
}

// typeName returns a readable type name for any value. Uses fmt.Sprintf
// with %T which prints "*pkg.TypeName".
func typeName(v any) string {
	return fmt.Sprintf("%T", v)
}