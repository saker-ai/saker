import { useProjectStore } from "./projectStore";

export type ProjectRole = "owner" | "admin" | "member" | "viewer";

export interface ProjectPermissions {
  role: ProjectRole | null;
  canRead: boolean;
  canEdit: boolean;
  canDelete: boolean;
  canInvite: boolean;
  canManageProject: boolean;
  canTransferOrDelete: boolean;
}

const NONE: ProjectPermissions = {
  role: null,
  canRead: false,
  canEdit: false,
  canDelete: false,
  canInvite: false,
  canManageProject: false,
  canTransferOrDelete: false,
};

// Derive permissions from the *current* project's role. The dispatch table
// here mirrors pkg/server/handler.go's methodMinRole so the UI can disable the
// same actions the backend will reject. If the matrix drifts, prefer adjusting
// this file rather than letting users see actions that always 403.
export function rolePermissions(role: ProjectRole | null): ProjectPermissions {
  if (!role) return NONE;
  const isOwner = role === "owner";
  const isAdminOrOwner = role === "admin" || isOwner;
  const isMemberOrAbove =
    role === "member" || role === "admin" || role === "owner";
  return {
    role,
    canRead: true,
    canEdit: isMemberOrAbove,
    canDelete: isAdminOrOwner,
    canInvite: isAdminOrOwner,
    canManageProject: isAdminOrOwner,
    canTransferOrDelete: isOwner,
  };
}

export function usePermissions(): ProjectPermissions {
  const projects = useProjectStore((s) => s.projects);
  const currentId = useProjectStore((s) => s.currentProjectId);
  const current = projects.find((p) => p.id === currentId) ?? null;
  return rolePermissions(current?.role ?? null);
}
