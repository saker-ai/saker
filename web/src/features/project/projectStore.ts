import { create } from "zustand";

import { httpRequest } from "../rpc/httpRpc";

// ProjectSummary mirrors the shape returned by `project/list` —
// (id, name, kind, role) plus optional createdAt for display ordering.
export interface ProjectSummary {
  id: string;
  name: string;
  kind: "personal" | "team";
  role: "owner" | "admin" | "member" | "viewer";
  createdAt?: string;
}

interface ProjectStoreState {
  projects: ProjectSummary[];
  currentProjectId: string | null;
  loading: boolean;
  error: string | null;
  /** Replace project list and pick a current id (preserve existing if still valid). */
  setProjects: (projects: ProjectSummary[]) => void;
  /** Switch active project (no-op if same id). Persists to localStorage. */
  setCurrent: (id: string | null) => void;
  /**
   * Fetch project/list over HTTP (so the page doesn't open a WebSocket just to
   * keep the project list fresh) and pick a default if nothing's chosen.
   */
  refresh: () => Promise<void>;
  setError: (msg: string | null) => void;
}

const STORAGE_KEY = "saker.currentProjectId";

function loadPersisted(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

function persist(id: string | null) {
  if (typeof window === "undefined") return;
  try {
    if (id) window.localStorage.setItem(STORAGE_KEY, id);
    else window.localStorage.removeItem(STORAGE_KEY);
  } catch {
    // ignore quota / privacy-mode errors
  }
}

// pickDefault chooses the personal project first, then the first project,
// falling back to null. Used after refresh when nothing is selected yet.
function pickDefault(
  projects: ProjectSummary[],
  preferred: string | null,
): string | null {
  if (preferred && projects.some((p) => p.id === preferred)) return preferred;
  const personal = projects.find((p) => p.kind === "personal");
  if (personal) return personal.id;
  return projects[0]?.id ?? null;
}

export const useProjectStore = create<ProjectStoreState>((set, get) => ({
  projects: [],
  currentProjectId: loadPersisted(),
  loading: false,
  error: null,
  setProjects: (projects) => {
    const next = pickDefault(projects, get().currentProjectId);
    if (next !== get().currentProjectId) persist(next);
    set({ projects, currentProjectId: next });
  },
  setCurrent: (id) => {
    if (id === get().currentProjectId) return;
    persist(id);
    set({ currentProjectId: id });
  },
  refresh: async () => {
    set({ loading: true, error: null });
    try {
      const result = await httpRequest<{ projects: ProjectSummary[] }>(
        "project/list",
      );
      const projects = Array.isArray(result?.projects) ? result.projects : [];
      const next = pickDefault(projects, get().currentProjectId);
      if (next !== get().currentProjectId) persist(next);
      set({ projects, currentProjectId: next, loading: false });
    } catch (err) {
      set({
        loading: false,
        error: err instanceof Error ? err.message : String(err),
      });
    }
  },
  setError: (msg) => set({ error: msg }),
}));

/**
 * projectIdProvider returns the current project id from the zustand store.
 * Wired into RPCClient.setProjectIdProvider() at app boot so every request
 * automatically carries projectId without forcing every call site to thread it.
 */
export function projectIdProvider(): string | null {
  return useProjectStore.getState().currentProjectId;
}
