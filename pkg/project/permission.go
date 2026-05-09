package project

import (
	"context"
	"errors"
)

// ErrScopeMissing is returned by RequireProjectRole when the request context
// has no Scope bound (typically because the request bypassed middleware).
var ErrScopeMissing = errors.New("project: no scope in context")

// RequireProjectRole checks that the Scope in ctx meets min. Handlers call
// this at the top of any RPC that mutates project state. Returns a typed
// error so the dispatcher can map it to JSON-RPC error codes.
//
// Note: the dispatcher also enforces a method→role table, but handlers should
// re-check inside any code path that branches on user privileges (e.g.,
// "members can delete only their own threads").
func RequireProjectRole(ctx context.Context, min Role) error {
	scope, ok := FromContext(ctx)
	if !ok {
		return ErrScopeMissing
	}
	if !scope.Role.AtLeast(min) {
		return ErrInsufficientRole
	}
	return nil
}

// CanEdit is sugar for the most common check.
func CanEdit(ctx context.Context) bool {
	return RequireProjectRole(ctx, RoleMember) == nil
}

// CanManage is sugar for "admin or owner".
func CanManage(ctx context.Context) bool {
	return RequireProjectRole(ctx, RoleAdmin) == nil
}

// CanOwn is sugar for "owner only".
func CanOwn(ctx context.Context) bool {
	return RequireProjectRole(ctx, RoleOwner) == nil
}
