"use client";

import { useState } from "react";
import { X, Trash2, Image, Video, Music } from "lucide-react";
import { useAssetStore, type Asset } from "./assetStore";
import { useCanvasStore } from "../store";
import type { CanvasNodeType, MediaType } from "../types";
import { useT, type TKey } from "@/features/i18n";

type FilterTab = "all" | "image" | "video" | "audio";

const TAB_KEYS: FilterTab[] = ["all", "image", "video", "audio"];
const TAB_ICONS: Record<FilterTab, React.ReactNode> = {
  all: null,
  image: <Image size={12} />,
  video: <Video size={12} />,
  audio: <Music size={12} />,
};

interface AssetLibraryProps {
  open: boolean;
  onClose: () => void;
}

const TAB_I18N: Record<FilterTab, TKey> = {
  all: "canvas.all",
  image: "canvas.images",
  video: "canvas.videos",
  audio: "canvas.audioTab",
};

export function AssetLibrary({ open, onClose }: AssetLibraryProps) {
  const { t } = useT();
  const [tab, setTab] = useState<FilterTab>("all");
  const assets = useAssetStore((s) => s.assets);
  const removeAsset = useAssetStore((s) => s.removeAsset);

  if (!open) return null;

  const filtered = tab === "all" ? assets : assets.filter((a) => a.type === tab);

  const addToCanvas = (asset: Asset) => {
    const store = useCanvasStore.getState();
    const nodeType = asset.type as CanvasNodeType;
    store.addNode({
      type: nodeType,
      position: { x: 100 + Math.random() * 200, y: 100 + Math.random() * 200 },
      data: {
        nodeType,
        label: asset.label,
        status: "done",
        mediaType: asset.type as MediaType,
        mediaUrl: asset.url,
        startTime: Date.now(),
        endTime: Date.now(),
      },
    });
  };

  return (
    <div className="canvas-panel canvas-asset-panel">
      <div className="canvas-panel-header">
        <h3>{t("canvas.assets")} ({assets.length})</h3>
        <button className="canvas-panel-close" onClick={onClose}>
          <X size={16} />
        </button>
      </div>

      <div className="canvas-panel-tabs">
        {TAB_KEYS.map((key) => (
          <button
            key={key}
            className={`canvas-panel-tab ${tab === key ? "active" : ""}`}
            onClick={() => setTab(key)}
          >
            {TAB_ICONS[key]}
            {t(TAB_I18N[key])}
          </button>
        ))}
      </div>

      <div className="canvas-panel-body">
        {filtered.length === 0 ? (
          <div className="canvas-panel-empty">
            <p>{t("canvas.noAssets")}</p>
            <p className="canvas-panel-hint">{t("canvas.assetHint")}</p>
          </div>
        ) : (
          <div className="canvas-asset-grid">
            {filtered.map((asset) => (
              <div
                key={asset.id}
                className="canvas-asset-card"
                onClick={() => addToCanvas(asset)}
                title={t("canvas.addToCanvas")}
              >
                {asset.type === "image" && (
                  <img src={asset.url} alt={asset.label} loading="lazy" />
                )}
                {asset.type === "video" && (
                  <div className="canvas-asset-video-thumb">
                    <Video size={24} />
                  </div>
                )}
                {asset.type === "audio" && (
                  <div className="canvas-asset-audio-thumb">
                    <Music size={24} />
                  </div>
                )}
                <div className="canvas-asset-card-footer">
                  <span className="canvas-asset-card-label">{asset.label}</span>
                  <button
                    className="canvas-asset-card-delete"
                    onClick={(e) => {
                      e.stopPropagation();
                      removeAsset(asset.id);
                    }}
                    title={t("canvas.remove")}
                  >
                    <Trash2 size={12} />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
