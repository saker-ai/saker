"use client";

import { Bookmark, Image, Video, Music, Trash2, X } from "lucide-react";
import { useTemplateStore, type NodeTemplate } from "./templateStore";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";
import type { CanvasNodeData, CanvasNodeType } from "../types";

interface TemplatePanelProps {
  open: boolean;
  onClose: () => void;
}

function iconFor(nodeType: CanvasNodeType) {
  if (nodeType === "imageGen") return <Image size={14} />;
  if (nodeType === "videoGen") return <Video size={14} />;
  if (nodeType === "voiceGen") return <Music size={14} />;
  return <Bookmark size={14} />;
}

function summarize(tpl: NodeTemplate, nodeLabel: (t: CanvasNodeType) => string): string[] {
  const d = tpl.data;
  const chips: string[] = [nodeLabel(tpl.nodeType)];
  if (d.aspectRatio) chips.push(String(d.aspectRatio));
  if (d.resolution) chips.push(String(d.resolution));
  if (d.duration) chips.push(`${d.duration}s`);
  if (d.engine) chips.push(String(d.engine));
  if (d.genCount && d.genCount > 1) chips.push(`×${d.genCount}`);
  return chips;
}

export function TemplatePanel({ open, onClose }: TemplatePanelProps) {
  const { t } = useT();
  const templates = useTemplateStore((s) => s.templates);
  const removeTemplate = useTemplateStore((s) => s.removeTemplate);
  const addNode = useCanvasStore((s) => s.addNode);

  if (!open) return null;

  const nodeLabel = (nt: CanvasNodeType): string => {
    if (nt === "imageGen") return t("canvas.imageGen");
    if (nt === "videoGen") return t("canvas.videoGen");
    if (nt === "voiceGen") return t("canvas.audioGen");
    return nt;
  };

  const createFromTemplate = (tpl: NodeTemplate) => {
    const nodeType = tpl.nodeType;
    const label = tpl.name || nodeLabel(nodeType);
    addNode({
      type: nodeType,
      position: { x: window.innerWidth / 2 - 140, y: window.innerHeight / 2 - 100 },
      data: {
        nodeType,
        label,
        status: "pending",
        startTime: Date.now(),
        ...tpl.data,
      } as CanvasNodeData,
    });
    onClose();
  };

  return (
    <div className="canvas-template-panel">
      <div className="canvas-template-panel-header">
        <Bookmark size={14} />
        <span>{t("canvas.templates" as any)}</span>
        <div style={{ flex: 1 }} />
        <button className="canvas-template-panel-close" onClick={onClose} title={t("canvas.close" as any)}>
          <X size={14} />
        </button>
      </div>
      <div className="canvas-template-panel-body">
        {templates.length === 0 ? (
          <div className="canvas-template-empty">
            {t("canvas.templatesEmpty" as any)}
          </div>
        ) : (
          templates.map((tpl) => {
            const chips = summarize(tpl, nodeLabel);
            return (
              <div key={tpl.id} className="canvas-template-item">
                <button
                  className="canvas-template-apply"
                  onClick={() => createFromTemplate(tpl)}
                  title={t("canvas.templateApply" as any)}
                >
                  <div className="canvas-template-icon">{iconFor(tpl.nodeType)}</div>
                  <div className="canvas-template-info">
                    <div className="canvas-template-name">{tpl.name}</div>
                    <div className="canvas-template-chips">
                      {chips.map((chip, i) => (
                        <span key={i} className="canvas-template-chip">{chip}</span>
                      ))}
                    </div>
                  </div>
                </button>
                <button
                  className="canvas-template-remove"
                  onClick={() => removeTemplate(tpl.id)}
                  title={t("canvas.delete")}
                >
                  <Trash2 size={12} />
                </button>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
