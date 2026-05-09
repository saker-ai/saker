import { create } from "zustand";

export interface Asset {
  id: string;
  type: "image" | "video" | "audio";
  url: string;
  label: string;
  createdAt: number;
}

interface AssetState {
  assets: Asset[];
  addAsset: (asset: Omit<Asset, "id" | "createdAt">) => void;
  removeAsset: (id: string) => void;
}

function loadAssets(): Asset[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = localStorage.getItem("canvas-assets");
    return raw ? JSON.parse(raw) : [];
  } catch {
    return [];
  }
}

function saveAssets(assets: Asset[]) {
  try {
    localStorage.setItem("canvas-assets", JSON.stringify(assets));
  } catch { /* quota exceeded */ }
}

export const useAssetStore = create<AssetState>((set, get) => ({
  assets: loadAssets(),

  addAsset: (spec) => {
    // Deduplicate by URL
    if (get().assets.some((a) => a.url === spec.url)) return;
    const asset: Asset = {
      ...spec,
      id: `asset_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`,
      createdAt: Date.now(),
    };
    const MAX_ASSETS = 200;
    const next = [asset, ...get().assets].slice(0, MAX_ASSETS);
    saveAssets(next);
    set({ assets: next });
  },

  removeAsset: (id) => {
    const next = get().assets.filter((a) => a.id !== id);
    saveAssets(next);
    set({ assets: next });
  },
}));
