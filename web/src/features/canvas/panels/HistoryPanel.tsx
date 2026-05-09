"use client";

import { useState } from "react";
import { X, Trash2, Image, Video, Music } from "lucide-react";
import { useHistoryStore, type HistoryEntry } from "./historyStore";
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
const TAB_I18N: Record<FilterTab, TKey> = {
  all: "canvas.all",
  image: "canvas.images",
  video: "canvas.videos",
  audio: "canvas.audioTab",
};

interface HistoryPanelProps {
  open: boolean;
  onClose: () => void;
}

export function HistoryPanel({ open, onClose }: HistoryPanelProps) {
  const { t } = useT();
  const [tab, setTab] = useState<FilterTab>("all");
  const entries = useHistoryStore((s) => s.entries);
  const removeEntry = useHistoryStore((s) => s.removeEntry);

  if (!open) return null;

  const filtered = tab === "all" ? entries : entries.filter((e) => e.type === tab);

  const addToCanvas = (entry: HistoryEntry) => {
    const store = useCanvasStore.getState();
    const nodeType = entry.type as CanvasNodeType;
    store.addNode({
      type: nodeType,
      position: { x: 100 + Math.random() * 200, y: 100 + Math.random() * 200 },
      data: {
        nodeType,
        label: entry.prompt.slice(0, 30) || entry.type,
        status: "done",
        mediaType: entry.type as MediaType,
        mediaUrl: entry.mediaUrl,
        startTime: entry.createdAt,
        endTime: entry.createdAt,
      },
    });
  };

  const formatTime = (ts: number) => {
    const d = new Date(ts);
    return `${d.getMonth() + 1}/${d.getDate()} ${d.getHours()}:${d.getMinutes().toString().padStart(2, "0")}`;
  };

  return (
    <div className="canvas-panel canvas-history-panel">
      <div className="canvas-panel-header">
        <h3>{t("canvas.history")} ({entries.length})</h3>
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
            <p>{t("canvas.noHistory")}</p>
            <p className="canvas-panel-hint">{t("canvas.historyHint")}</p>
          </div>
        ) : (
          <div className="canvas-asset-grid">
            {filtered.map((entry) => (
              <div
                key={entry.id}
                className="canvas-asset-card"
                onClick={() => addToCanvas(entry)}
                title={entry.prompt}
              >
                {entry.type === "image" && (
                  <img src={entry.mediaUrl} alt={entry.prompt} loading="lazy" />
                )}
                {entry.type === "video" && (
                  <div className="canvas-asset-video-thumb">
                    <Video size={24} />
                  </div>
                )}
                {entry.type === "audio" && (
                  <div className="canvas-asset-audio-thumb">
                    <Music size={24} />
                  </div>
                )}
                <div className="canvas-asset-card-footer">
                  <span className="canvas-asset-card-label">{entry.prompt.slice(0, 20) || entry.type}</span>
                  <span className="canvas-history-time">{formatTime(entry.createdAt)}</span>
                  <button
                    className="canvas-asset-card-delete"
                    onClick={(e) => {
                      e.stopPropagation();
                      removeEntry(entry.id);
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
