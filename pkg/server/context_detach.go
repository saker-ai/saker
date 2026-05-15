package server

import (
	"context"

	"github.com/saker-ai/saker/pkg/project"
)

// detachWithScope copies the project Scope (and other request-bound values)
// from src onto dst. Use it when starting a background goroutine whose ctx
// must outlive the request — context.Background() drops every value, which
// silently routes per-project work (sessions, canvas, paths) back to the
// legacy single-tenant store. Callers typically pass the original request
// ctx as src and a freshly-built background ctx (with the desired
// cancellation / timeout) as dst.
//
// Auth identity (UserFromContext / RoleFromContext) is intentionally not
// carried here because the executeTurn path threads username/role as
// explicit arguments. If a future caller needs them too, extend this helper
// rather than re-deriving the copy logic.
func detachWithScope(src, dst context.Context) context.Context {
	if src == nil {
		return dst
	}
	if scope, ok := project.FromContext(src); ok {
		dst = project.WithScope(dst, scope)
	}
	return dst
}
