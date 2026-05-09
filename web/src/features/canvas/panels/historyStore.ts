import { create } from "zustand";

export type HistoryEntryType = "image" | "video" | "audio" | "text";

export interface HistoryEntry {
  id: string;
  type: HistoryEntryType;
  prompt: string;
  /** Optional for text entries (no media URL). */
  mediaUrl: string;
  params: Record<string, unknown>;
  createdAt: number;
}

interface HistoryState {
  entries: HistoryEntry[];
  addEntry: (entry: Omit<HistoryEntry, "id" | "createdAt">) => void;
  removeEntry: (id: string) => void;
  clearAll: () => void;
}

function loadHistory(): HistoryEntry[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = localStorage.getItem("canvas-gen-history");
    return raw ? JSON.parse(raw) : [];
  } catch {
    return [];
  }
}

function saveHistory(entries: HistoryEntry[]) {
  try {
    localStorage.setItem("canvas-gen-history", JSON.stringify(entries));
  } catch { /* quota exceeded */ }
}

const MAX_HISTORY = 100;

export const useHistoryStore = create<HistoryState>((set, get) => ({
  entries: loadHistory(),

  addEntry: (spec) => {
    const existing = get().entries;
    if (spec.mediaUrl && existing.some((e) => e.mediaUrl === spec.mediaUrl)) return;
    if (!spec.mediaUrl && existing.some((e) => e.type === spec.type && e.prompt === spec.prompt)) return;
    const entry: HistoryEntry = {
      ...spec,
      id: `hist_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`,
      createdAt: Date.now(),
    };
    const next = [entry, ...get().entries].slice(0, MAX_HISTORY);
    saveHistory(next);
    set({ entries: next });
  },

  removeEntry: (id) => {
    const next = get().entries.filter((e) => e.id !== id);
    saveHistory(next);
    set({ entries: next });
  },

  clearAll: () => {
    saveHistory([]);
    set({ entries: [] });
  },
}));
