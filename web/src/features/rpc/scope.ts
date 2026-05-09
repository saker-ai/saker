// Shared between the WebSocket RPC client (`client.ts`) and the HTTP RPC
// client (`httpRpc.ts`). Both transports need the same projectId-injection
// rules, otherwise the same call would behave differently depending on
// transport — which would be a bug magnet.

export type ProjectIdProvider = () => string | null;

// METHOD_SKIP_PROJECT mirrors pkg/server/middleware_scope.go's
// methodSkipProject map. Methods listed here must NOT receive a synthetic
// projectId — they either run before any project is selected, or operate
// on global state. Keep in sync with the backend list.
export const METHOD_SKIP_PROJECT = new Set<string>([
  // Boot / connection.
  "initialize",

  // Auth lifecycle: caller may not even be logged in yet.
  "auth/update",
  "auth/delete",

  // User self-service & site-admin user CRUD.
  "user/me",
  "user/list",
  "user/create",
  "user/delete",
  "user/update-password",

  // Project discovery & creation.
  "project/list",
  "project/create",
  "project/get",
  "project/me",

  // Invite handling is keyed by inviteId, not projectId.
  "project/invite/accept",
  "project/invite/decline",
  "project/invite/list-for-me",

  // Teams are top-level entities — they don't belong to a project.
  "team/list",
  "team/create",
  "team/delete",
  "team/member/list",

  // Cross-project search & global metadata.
  "sessions/search",
  "aigo/models",
  "aigo/providers",
  "aigo/status",
]);

/**
 * Inject the active projectId into params for non-skip methods. Caller-provided
 * projectId always wins. Returns the params object unchanged when the method
 * is in the skip list, no provider is set, or the provider returns null.
 */
export function injectProjectId(
  method: string,
  params: Record<string, unknown> | undefined,
  provider: ProjectIdProvider | null,
): Record<string, unknown> | undefined {
  if (METHOD_SKIP_PROJECT.has(method)) return params;
  if (!provider) return params;
  if (params && params.projectId) return params;
  const pid = provider();
  if (!pid) return params;
  return { ...(params ?? {}), projectId: pid };
}
