package server

import (
	"context"
	"testing"

	"github.com/saker-ai/saker/pkg/config"
	"golang.org/x/crypto/bcrypt"
)

// newUserTestHandler creates a Handler with a real Runtime and writes an initial
// WebAuth config to settings.local.json so user CRUD handlers can operate.
// The admin credentials are username="admin", password="testpass".
func newUserTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	h, root := newTestHandler(t)

	hash := hashPasswordBcrypt(t, "testpass")
	writeSettingsLocal(t, root, &config.Settings{
		WebAuth: &config.WebAuthConfig{
			Username: "admin",
			Password: hash,
		},
	})
	reloadSettings(t, h)

	// Wire up an AuthManager so UpdateConfig / GetUserInfo paths are exercised.
	h.auth = NewAuthManager(h.runtime.Settings().WebAuth, h.logger)

	return h, root
}

func hashPasswordBcrypt(t *testing.T, plain string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

// --- requireAdmin --- //

func TestRequireAdmin_AdminRole(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp, ok := h.requireAdmin(ctx, 1)
	if !ok {
		t.Fatalf("requireAdmin should return ok=true for admin role, got response: %+v", resp)
	}
	if resp.Error != nil {
		t.Fatalf("expected no error for admin, got: %+v", resp.Error)
	}
}

func TestRequireAdmin_UserRole(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := userCtx("alice")

	resp, ok := h.requireAdmin(ctx, 1)
	if ok {
		t.Fatal("requireAdmin should return ok=false for non-admin role")
	}
	if resp.Error == nil {
		t.Fatal("expected error response for non-admin")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
	if resp.Error.Message != "admin access required" {
		t.Fatalf("expected 'admin access required', got '%s'", resp.Error.Message)
	}
}

// --- handleUserMe --- //

func TestHandleUserMe_BasicFields(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserMe(ctx, rpcRequest("user/me", 1, nil))
	if resp.Error != nil {
		t.Fatalf("user/me: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T", resp.Result)
	}
	if result["username"] != "admin" {
		t.Fatalf("want username=admin, got %v", result["username"])
	}
	if result["role"] != "admin" {
		t.Fatalf("want role=admin, got %v", result["role"])
	}
}

func TestHandleUserMe_WithUserInfo(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)

	// Cache external user info so GetUserInfo returns enriched data.
	h.auth.cacheUserInfo(&AuthResult{
		Username:    "admin",
		DisplayName: "Admin User",
		Email:       "admin@example.com",
		AvatarURL:   "https://example.com/avatar.png",
		Provider:    "ldap",
	})

	ctx := adminCtx("admin")
	resp := h.handleUserMe(ctx, rpcRequest("user/me", 1, nil))
	if resp.Error != nil {
		t.Fatalf("user/me: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]any)
	if result["displayName"] != "Admin User" {
		t.Fatalf("want displayName=Admin User, got %v", result["displayName"])
	}
	if result["email"] != "admin@example.com" {
		t.Fatalf("want email=admin@example.com, got %v", result["email"])
	}
	if result["avatarUrl"] != "https://example.com/avatar.png" {
		t.Fatalf("want avatarUrl, got %v", result["avatarUrl"])
	}
	if result["provider"] != "ldap" {
		t.Fatalf("want provider=ldap, got %v", result["provider"])
	}
}

func TestHandleUserMe_UserRole(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := userCtx("alice")

	resp := h.handleUserMe(ctx, rpcRequest("user/me", 1, nil))
	if resp.Error != nil {
		t.Fatalf("user/me: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]any)
	if result["username"] != "alice" {
		t.Fatalf("want username=alice, got %v", result["username"])
	}
	if result["role"] != "user" {
		t.Fatalf("want role=user, got %v", result["role"])
	}
}

// --- handleUserList --- //

func TestHandleUserList_AdminOnly(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := userCtx("alice")

	resp := h.handleUserList(ctx, rpcRequest("user/list", 1, nil))
	if resp.Error == nil {
		t.Fatal("expected error for non-admin")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserList_AdminOnlyUser(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserList(ctx, rpcRequest("user/list", 1, nil))
	if resp.Error != nil {
		t.Fatalf("user/list: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]any)
	users, ok := result["users"].([]map[string]any)
	if !ok {
		t.Fatalf("users not a slice of maps: %T", result["users"])
	}
	// Should have at least admin user.
	if len(users) < 1 {
		t.Fatalf("want at least 1 user (admin), got %d", len(users))
	}
	if users[0]["username"] != "admin" {
		t.Fatalf("want first user username=admin, got %v", users[0]["username"])
	}
	if users[0]["role"] != "admin" {
		t.Fatalf("want first user role=admin, got %v", users[0]["role"])
	}
}

func TestHandleUserList_WithRegularUsers(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)

	// Add a regular user to settings.
	settings, _ := config.LoadSettingsLocal(root)
	settings.WebAuth.Users = append(settings.WebAuth.Users, config.UserAuth{
		Username: "alice",
		Password: hashPasswordBcrypt(t, "alicepass"),
	})
	settings.WebAuth.Users = append(settings.WebAuth.Users, config.UserAuth{
		Username: "bob",
		Password: hashPasswordBcrypt(t, "bobpass"),
		Disabled: true,
	})
	writeSettingsLocal(t, root, settings)
	reloadSettings(t, h)

	ctx := adminCtx("admin")
	resp := h.handleUserList(ctx, rpcRequest("user/list", 1, nil))
	if resp.Error != nil {
		t.Fatalf("user/list: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]any)
	users, _ := result["users"].([]map[string]any)
	if len(users) != 3 {
		t.Fatalf("want 3 users (admin + 2 regular), got %d", len(users))
	}
	// Check regular users appear.
	aliceFound := false
	bobFound := false
	for _, u := range users[1:] {
		if u["username"] == "alice" && u["role"] == "user" && u["disabled"] == false {
			aliceFound = true
		}
		if u["username"] == "bob" && u["role"] == "user" && u["disabled"] == true {
			bobFound = true
		}
	}
	if !aliceFound {
		t.Error("alice not found in user list")
	}
	if !bobFound {
		t.Error("bob not found in user list")
	}
}

// --- handleUserCreate --- //

func TestHandleUserCreate_AdminOnly(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := userCtx("alice")

	resp := h.handleUserCreate(ctx, rpcRequest("user/create", 1, map[string]any{
		"username": "newuser",
		"password": "newpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for non-admin")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserCreate_MissingUsername(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserCreate(ctx, rpcRequest("user/create", 1, map[string]any{
		"password": "newpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for missing username")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserCreate_MissingPassword(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserCreate(ctx, rpcRequest("user/create", 1, map[string]any{
		"username": "newuser",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for missing password")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserCreate_AdminUsernameCollision(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserCreate(ctx, rpcRequest("user/create", 1, map[string]any{
		"username": "admin",
		"password": "newpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error: cannot create user with admin username")
	}
	if resp.Error.Message != "cannot create user with admin username" {
		t.Fatalf("want 'cannot create user with admin username', got '%s'", resp.Error.Message)
	}
}

func TestHandleUserCreate_DuplicateUser(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)

	// Add alice as a regular user first.
	settings, _ := config.LoadSettingsLocal(root)
	settings.WebAuth.Users = append(settings.WebAuth.Users, config.UserAuth{
		Username: "alice",
		Password: hashPasswordBcrypt(t, "alicepass"),
	})
	writeSettingsLocal(t, root, settings)
	reloadSettings(t, h)

	ctx := adminCtx("admin")
	resp := h.handleUserCreate(ctx, rpcRequest("user/create", 1, map[string]any{
		"username": "alice",
		"password": "anotherpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for duplicate user")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserCreate_InvalidUsername(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	// "default" is a reserved profile name — profile.Validate rejects it
	// only when name == "" or name == "default" returns nil, so try a name
	// with invalid characters instead.
	resp := h.handleUserCreate(ctx, rpcRequest("user/create", 1, map[string]any{
		"username": "has spaces",
		"password": "validpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for invalid username")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserCreate_NoWebAuth(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t)
	// Do NOT write WebAuth config — user/create should reject since
	// web auth not configured.
	ctx := adminCtx("admin")

	resp := h.handleUserCreate(ctx, rpcRequest("user/create", 1, map[string]any{
		"username": "alice",
		"password": "alicepass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error when web auth not configured")
	}
	if resp.Error.Message != "web auth not configured — set admin password first" {
		t.Fatalf("want 'web auth not configured', got '%s'", resp.Error.Message)
	}
}

func TestHandleUserCreate_Success(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserCreate(ctx, rpcRequest("user/create", 1, map[string]any{
		"username": "alice",
		"password": "alicepass",
	}))
	if resp.Error != nil {
		t.Fatalf("user/create: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]any)
	if result["ok"] != true {
		t.Fatalf("want ok=true, got %v", result["ok"])
	}
	if result["username"] != "alice" {
		t.Fatalf("want username=alice, got %v", result["username"])
	}

	// Verify persisted in settings.local.json.
	saved, err := config.LoadSettingsLocal(root)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if len(saved.WebAuth.Users) != 1 {
		t.Fatalf("want 1 regular user, got %d", len(saved.WebAuth.Users))
	}
	if saved.WebAuth.Users[0].Username != "alice" {
		t.Fatalf("want username=alice, got %s", saved.WebAuth.Users[0].Username)
	}
	// Password should be hashed, not plaintext.
	if saved.WebAuth.Users[0].Password == "alicepass" {
		t.Fatal("password should be hashed, not stored as plaintext")
	}
}

func TestHandleUserCreate_SuccessWithAuthManager(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserCreate(ctx, rpcRequest("user/create", 1, map[string]any{
		"username": "bob",
		"password": "bobpass",
	}))
	if resp.Error != nil {
		t.Fatalf("user/create: %+v", resp.Error)
	}

	// Verify AuthManager was updated (new user should be in the config).
	h.auth.mu.RLock()
	cfg := h.auth.cfg
	h.auth.mu.RUnlock()
	found := false
	for _, u := range cfg.Users {
		if u.Username == "bob" {
			found = true
		}
	}
	if !found {
		t.Error("AuthManager config not updated with new user 'bob'")
	}
}

// --- handleUserDelete --- //

func TestHandleUserDelete_AdminOnly(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := userCtx("alice")

	resp := h.handleUserDelete(ctx, rpcRequest("user/delete", 1, map[string]any{
		"username": "alice",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for non-admin")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserDelete_MissingUsername(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserDelete(ctx, rpcRequest("user/delete", 1, map[string]any{}))
	if resp.Error == nil {
		t.Fatal("expected error for missing username")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserDelete_CannotDeleteAdmin(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserDelete(ctx, rpcRequest("user/delete", 1, map[string]any{
		"username": "admin",
	}))
	if resp.Error == nil {
		t.Fatal("expected error: cannot delete admin user")
	}
	if resp.Error.Message != "cannot delete admin user" {
		t.Fatalf("want 'cannot delete admin user', got '%s'", resp.Error.Message)
	}
}

func TestHandleUserDelete_UserNotFound(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserDelete(ctx, rpcRequest("user/delete", 1, map[string]any{
		"username": "nonexistent",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for non-existent user")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserDelete_NoWebAuth(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserDelete(ctx, rpcRequest("user/delete", 1, map[string]any{
		"username": "alice",
	}))
	if resp.Error == nil {
		t.Fatal("expected error when web auth not configured")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserDelete_Success(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)

	// Add alice as a regular user.
	settings, _ := config.LoadSettingsLocal(root)
	settings.WebAuth.Users = append(settings.WebAuth.Users, config.UserAuth{
		Username: "alice",
		Password: hashPasswordBcrypt(t, "alicepass"),
	})
	settings.WebAuth.Users = append(settings.WebAuth.Users, config.UserAuth{
		Username: "bob",
		Password: hashPasswordBcrypt(t, "bobpass"),
	})
	writeSettingsLocal(t, root, settings)
	reloadSettings(t, h)

	ctx := adminCtx("admin")
	resp := h.handleUserDelete(ctx, rpcRequest("user/delete", 1, map[string]any{
		"username": "alice",
	}))
	if resp.Error != nil {
		t.Fatalf("user/delete: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]any)
	if result["ok"] != true {
		t.Fatalf("want ok=true, got %v", result["ok"])
	}

	// Verify alice removed from settings.
	saved, _ := config.LoadSettingsLocal(root)
	if len(saved.WebAuth.Users) != 1 {
		t.Fatalf("want 1 remaining user, got %d", len(saved.WebAuth.Users))
	}
	if saved.WebAuth.Users[0].Username != "bob" {
		t.Fatalf("want remaining user=bob, got %s", saved.WebAuth.Users[0].Username)
	}
}

func TestHandleUserDelete_UpdatesAuthManager(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)

	// Add alice as a regular user.
	settings, _ := config.LoadSettingsLocal(root)
	settings.WebAuth.Users = append(settings.WebAuth.Users, config.UserAuth{
		Username: "alice",
		Password: hashPasswordBcrypt(t, "alicepass"),
	})
	writeSettingsLocal(t, root, settings)
	reloadSettings(t, h)

	ctx := adminCtx("admin")
	resp := h.handleUserDelete(ctx, rpcRequest("user/delete", 1, map[string]any{
		"username": "alice",
	}))
	if resp.Error != nil {
		t.Fatalf("user/delete: %+v", resp.Error)
	}

	// AuthManager should no longer have alice in its config.
	h.auth.mu.RLock()
	cfg := h.auth.cfg
	h.auth.mu.RUnlock()
	for _, u := range cfg.Users {
		if u.Username == "alice" {
			t.Error("alice should have been removed from AuthManager config")
		}
	}
}

// --- handleUserUpdatePassword --- //

func TestHandleUserUpdatePassword_NotAuthenticated(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := context.Background() // no user in context

	resp := h.handleUserUpdatePassword(ctx, rpcRequest("user/updatePassword", 1, map[string]any{
		"oldPassword": "testpass",
		"newPassword": "newpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for unauthenticated user")
	}
	if resp.Error.Message != "not authenticated" {
		t.Fatalf("want 'not authenticated', got '%s'", resp.Error.Message)
	}
}

func TestHandleUserUpdatePassword_MissingOldPassword(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserUpdatePassword(ctx, rpcRequest("user/updatePassword", 1, map[string]any{
		"newPassword": "newpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for missing oldPassword")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserUpdatePassword_MissingNewPassword(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserUpdatePassword(ctx, rpcRequest("user/updatePassword", 1, map[string]any{
		"oldPassword": "testpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for missing newPassword")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleUserUpdatePassword_AdminWrongOldPassword(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserUpdatePassword(ctx, rpcRequest("user/updatePassword", 1, map[string]any{
		"oldPassword": "wrongpass",
		"newPassword": "newpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for wrong old password")
	}
	if resp.Error.Message != "incorrect old password" {
		t.Fatalf("want 'incorrect old password', got '%s'", resp.Error.Message)
	}
}

func TestHandleUserUpdatePassword_AdminSuccess(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleUserUpdatePassword(ctx, rpcRequest("user/updatePassword", 1, map[string]any{
		"oldPassword": "testpass",
		"newPassword": "newadminpass",
	}))
	if resp.Error != nil {
		t.Fatalf("update password: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]any)
	if result["ok"] != true {
		t.Fatalf("want ok=true, got %v", result["ok"])
	}

	// Verify new password works (bcrypt comparison).
	saved, _ := config.LoadSettingsLocal(root)
	if err := bcrypt.CompareHashAndPassword([]byte(saved.WebAuth.Password), []byte("newadminpass")); err != nil {
		t.Fatalf("new admin password does not match: %v", err)
	}
}

func TestHandleUserUpdatePassword_RegularUserWrongOldPassword(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)

	// Add alice as a regular user.
	settings, _ := config.LoadSettingsLocal(root)
	settings.WebAuth.Users = append(settings.WebAuth.Users, config.UserAuth{
		Username: "alice",
		Password: hashPasswordBcrypt(t, "alicepass"),
	})
	writeSettingsLocal(t, root, settings)
	reloadSettings(t, h)

	ctx := userCtx("alice")
	resp := h.handleUserUpdatePassword(ctx, rpcRequest("user/updatePassword", 1, map[string]any{
		"oldPassword": "wrongpass",
		"newPassword": "newpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for wrong old password")
	}
	if resp.Error.Message != "incorrect old password" {
		t.Fatalf("want 'incorrect old password', got '%s'", resp.Error.Message)
	}
}

func TestHandleUserUpdatePassword_RegularUserSuccess(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)

	// Add alice as a regular user.
	settings, _ := config.LoadSettingsLocal(root)
	settings.WebAuth.Users = append(settings.WebAuth.Users, config.UserAuth{
		Username: "alice",
		Password: hashPasswordBcrypt(t, "alicepass"),
	})
	writeSettingsLocal(t, root, settings)
	reloadSettings(t, h)

	ctx := userCtx("alice")
	resp := h.handleUserUpdatePassword(ctx, rpcRequest("user/updatePassword", 1, map[string]any{
		"oldPassword": "alicepass",
		"newPassword": "newalicepass",
	}))
	if resp.Error != nil {
		t.Fatalf("update password: %+v", resp.Error)
	}

	// Verify new password works.
	saved, _ := config.LoadSettingsLocal(root)
	for _, u := range saved.WebAuth.Users {
		if u.Username == "alice" {
			if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte("newalicepass")); err != nil {
				t.Fatalf("new alice password does not match: %v", err)
			}
			return
		}
	}
	t.Fatal("alice not found in saved settings")
}

func TestHandleUserUpdatePassword_RegularUserNotFound(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)

	// Regular user context, but the user does not exist in the Users slice.
	ctx := userCtx("ghost")
	resp := h.handleUserUpdatePassword(ctx, rpcRequest("user/updatePassword", 1, map[string]any{
		"oldPassword": "whatever",
		"newPassword": "newpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for user not found")
	}
	if resp.Error.Message != "user not found" {
		t.Fatalf("want 'user not found', got '%s'", resp.Error.Message)
	}
}

func TestHandleUserUpdatePassword_NoAuthConfigured(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t)
	// No WebAuth config written.
	ctx := adminCtx("admin")

	resp := h.handleUserUpdatePassword(ctx, rpcRequest("user/updatePassword", 1, map[string]any{
		"oldPassword": "test",
		"newPassword": "new",
	}))
	if resp.Error == nil {
		t.Fatal("expected error when auth not configured")
	}
	if resp.Error.Message != "auth not configured" {
		t.Fatalf("want 'auth not configured', got '%s'", resp.Error.Message)
	}
}

// --- handleAuthUpdate --- //

func TestHandleAuthUpdate_AdminOnly(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := userCtx("alice")

	resp := h.handleAuthUpdate(ctx, rpcRequest("auth/update", 1, map[string]any{
		"password": "newpass",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for non-admin")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleAuthUpdate_MissingPassword(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleAuthUpdate(ctx, rpcRequest("auth/update", 1, map[string]any{
		"username": "admin",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for missing password")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleAuthUpdate_Success(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleAuthUpdate(ctx, rpcRequest("auth/update", 1, map[string]any{
		"username": "superadmin",
		"password": "superpass",
	}))
	if resp.Error != nil {
		t.Fatalf("auth/update: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]any)
	if result["ok"] != true {
		t.Fatalf("want ok=true, got %v", result["ok"])
	}

	// Verify admin username and password changed.
	saved, _ := config.LoadSettingsLocal(root)
	if saved.WebAuth.Username != "superadmin" {
		t.Fatalf("want username=superadmin, got %s", saved.WebAuth.Username)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(saved.WebAuth.Password), []byte("superpass")); err != nil {
		t.Fatalf("new admin password does not match: %v", err)
	}

	// Verify AuthManager was updated.
	h.auth.mu.RLock()
	cfg := h.auth.cfg
	h.auth.mu.RUnlock()
	if cfg.Username != "superadmin" {
		t.Fatalf("AuthManager username not updated: %s", cfg.Username)
	}
}

func TestHandleAuthUpdate_DefaultUsername(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleAuthUpdate(ctx, rpcRequest("auth/update", 1, map[string]any{
		"password": "newpass",
	}))
	if resp.Error != nil {
		t.Fatalf("auth/update: %+v", resp.Error)
	}

	// When username is empty, it defaults to "admin".
	saved, _ := config.LoadSettingsLocal(root)
	if saved.WebAuth.Username != "admin" {
		t.Fatalf("want username=admin (default), got %s", saved.WebAuth.Username)
	}
}

func TestHandleAuthUpdate_PreservesExistingUsers(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)

	// Add a regular user first.
	settings, _ := config.LoadSettingsLocal(root)
	settings.WebAuth.Users = append(settings.WebAuth.Users, config.UserAuth{
		Username: "alice",
		Password: hashPasswordBcrypt(t, "alicepass"),
	})
	writeSettingsLocal(t, root, settings)
	reloadSettings(t, h)

	ctx := adminCtx("admin")
	resp := h.handleAuthUpdate(ctx, rpcRequest("auth/update", 1, map[string]any{
		"password": "newadminpass",
	}))
	if resp.Error != nil {
		t.Fatalf("auth/update: %+v", resp.Error)
	}

	// Verify regular users are preserved.
	saved, _ := config.LoadSettingsLocal(root)
	if len(saved.WebAuth.Users) != 1 || saved.WebAuth.Users[0].Username != "alice" {
		t.Fatalf("regular users should be preserved, got %d users", len(saved.WebAuth.Users))
	}
}

// --- handleAuthDelete --- //

func TestHandleAuthDelete_AdminOnly(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := userCtx("alice")

	resp := h.handleAuthDelete(ctx, rpcRequest("auth/delete", 1, nil))
	if resp.Error == nil {
		t.Fatal("expected error for non-admin")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleAuthDelete_Success(t *testing.T) {
	t.Parallel()
	h, root := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleAuthDelete(ctx, rpcRequest("auth/delete", 1, nil))
	if resp.Error != nil {
		t.Fatalf("auth/delete: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]any)
	if result["ok"] != true {
		t.Fatalf("want ok=true, got %v", result["ok"])
	}

	// Verify WebAuth cleared.
	saved, _ := config.LoadSettingsLocal(root)
	if saved != nil && saved.WebAuth != nil {
		t.Fatal("WebAuth should be nil after auth/delete")
	}

	// Verify AuthManager was cleared.
	h.auth.mu.RLock()
	cfg := h.auth.cfg
	h.auth.mu.RUnlock()
	if cfg != nil {
		t.Fatal("AuthManager config should be nil after auth/delete")
	}
}

func TestHandleAuthDelete_NoSettings(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t)
	// No settings.local.json written — handleAuthDelete should still succeed
	// (it just does nothing since there's nothing to clear).
	ctx := adminCtx("admin")

	resp := h.handleAuthDelete(ctx, rpcRequest("auth/delete", 1, nil))
	if resp.Error != nil {
		t.Fatalf("auth/delete on empty settings: %+v", resp.Error)
	}
}

// --- handleProfileList --- //

func TestHandleProfileList_AdminOnly(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := userCtx("alice")

	resp := h.handleProfileList(ctx, rpcRequest("profile/list", 1, nil))
	if resp.Error == nil {
		t.Fatal("expected admin required error")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got code=%d", resp.Error.Code)
	}
}

func TestHandleProfileList_Success(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handleProfileList(ctx, rpcRequest("profile/list", 1, nil))
	if resp.Error != nil {
		t.Fatalf("profile/list: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]any)
	profiles, ok := result["profiles"]
	if !ok {
		t.Fatal("result missing 'profiles' key")
	}
	// Verify profiles is a non-nil slice (the default profile always exists).
	if profiles == nil {
		t.Fatal("profiles should not be nil")
	}
}

// --- safeWebAuthForResponse --- //

func TestSafeWebAuthForResponse_Nil(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)

	result := h.safeWebAuthForResponse(nil)
	if result != nil {
		t.Fatalf("expected nil for nil input, got %v", result)
	}
}

func TestSafeWebAuthForResponse_AdminOnly(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)

	auth := &config.WebAuthConfig{
		Username: "admin",
		Password: "hashed_secret",
	}
	result := h.safeWebAuthForResponse(auth)
	if result["username"] != "admin" {
		t.Fatalf("want username=admin, got %v", result["username"])
	}
	// Password hash should NOT appear in the response.
	if _, hasPassword := result["password"]; hasPassword {
		t.Error("password hash should not appear in safe response")
	}
}

func TestSafeWebAuthForResponse_WithUsers(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)

	auth := &config.WebAuthConfig{
		Username: "admin",
		Password: "hashed_secret",
		Users: []config.UserAuth{
			{Username: "alice", Password: "alice_hash", Disabled: false},
			{Username: "bob", Password: "bob_hash", Disabled: true},
		},
	}
	result := h.safeWebAuthForResponse(auth)
	if result["username"] != "admin" {
		t.Fatalf("want username=admin, got %v", result["username"])
	}

	users, ok := result["users"].([]map[string]any)
	if !ok {
		t.Fatalf("users not a slice of maps: %T", result["users"])
	}
	if len(users) != 2 {
		t.Fatalf("want 2 users, got %d", len(users))
	}
	if users[0]["username"] != "alice" {
		t.Fatalf("want alice, got %v", users[0]["username"])
	}
	if users[0]["disabled"] != false {
		t.Fatalf("want alice disabled=false, got %v", users[0]["disabled"])
	}
	if users[1]["username"] != "bob" {
		t.Fatalf("want bob, got %v", users[1]["username"])
	}
	if users[1]["disabled"] != true {
		t.Fatalf("want bob disabled=true, got %v", users[1]["disabled"])
	}
	// Password hashes should NOT appear in the response.
	if _, hasPw := users[0]["password"]; hasPw {
		t.Error("user password hash should not appear in safe response")
	}
	if _, hasPw := users[1]["password"]; hasPw {
		t.Error("user password hash should not appear in safe response")
	}
}

func TestSafeWebAuthForResponse_NoUsers(t *testing.T) {
	t.Parallel()
	h, _ := newUserTestHandler(t)

	auth := &config.WebAuthConfig{
		Username: "admin",
		Password: "hashed_secret",
	}
	result := h.safeWebAuthForResponse(auth)
	// When Users slice is empty, "users" key should not be present.
	if _, hasUsers := result["users"]; hasUsers {
		t.Error("empty Users slice should not produce 'users' key in response")
	}
}
