import { create } from "zustand";
import type { CanvasNodeData, CanvasNodeType } from "../types";

export interface NodeTemplate {
  id: string;
  name: string;
  nodeType: CanvasNodeType;
  /** Snapshot of the generation-relevant config fields. */
  data: Partial<CanvasNodeData>;
  createdAt: number;
}

interface TemplateState {
  templates: NodeTemplate[];
  addTemplate: (spec: Omit<NodeTemplate, "id" | "createdAt">) => void;
  removeTemplate: (id: string) => void;
  renameTemplate: (id: string, name: string) => void;
}

const STORAGE_KEY = "canvas-node-templates";

function load(): NodeTemplate[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? JSON.parse(raw) : [];
  } catch {
    return [];
  }
}

function save(templates: NodeTemplate[]) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(templates));
  } catch { /* quota exceeded */ }
}

/** Subset of CanvasNodeData fields that describe how a gen node is configured. */
export const TEMPLATE_FIELDS = [
  "engine",
  "size",
  "resolution",
  "aspectRatio",
  "cameraAngle",
  "duration",
  "negativePrompt",
  "voice",
  "language",
  "instructions",
  "genCount",
  "prompt",
] as const;

export function pickTemplateData(data: CanvasNodeData): Partial<CanvasNodeData> {
  const out: Partial<CanvasNodeData> = {};
  for (const k of TEMPLATE_FIELDS) {
    const v = (data as Record<string, unknown>)[k];
    if (v !== undefined && v !== "" && v !== null) {
      (out as Record<string, unknown>)[k] = v;
    }
  }
  return out;
}

export const useTemplateStore = create<TemplateState>((set, get) => ({
  templates: load(),

  addTemplate: (spec) => {
    const entry: NodeTemplate = {
      ...spec,
      id: `tpl_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`,
      createdAt: Date.now(),
    };
    const next = [entry, ...get().templates].slice(0, 50);
    save(next);
    set({ templates: next });
  },

  removeTemplate: (id) => {
    const next = get().templates.filter((t) => t.id !== id);
    save(next);
    set({ templates: next });
  },

  renameTemplate: (id, name) => {
    const next = get().templates.map((t) => (t.id === id ? { ...t, name } : t));
    save(next);
    set({ templates: next });
  },
}));
