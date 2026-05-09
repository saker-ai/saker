import { create } from "zustand";
import { listApps, deleteApp, type AppMeta } from "./appsApi";

interface AppsStoreState {
  apps: AppMeta[];
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  remove: (appId: string) => Promise<void>;
}

export const useAppsStore = create<AppsStoreState>((set) => ({
  apps: [],
  loading: false,
  error: null,

  refresh: async () => {
    set({ loading: true, error: null });
    try {
      const apps = await listApps();
      set({ apps, loading: false });
    } catch (err) {
      set({
        loading: false,
        error: err instanceof Error ? err.message : String(err),
      });
    }
  },

  remove: async (appId: string) => {
    await deleteApp(appId);
    set((s) => ({ apps: s.apps.filter((a) => a.id !== appId) }));
  },
}));
