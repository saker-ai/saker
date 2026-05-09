import { memo, useCallback } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Wrench, CheckCircle2, AlertCircle, Loader2 } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar, getMediaActions, getDetailActions } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";

export const ToolNode = memo(function ToolNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const isRunning = d.status === "running";
  const isError = d.status === "error";
  const isDone = d.status === "done";
  const isHighlighted = useCanvasStore((s) => s.highlightedTurnId != null && s.highlightedTurnId === d.turnId);
  const elapsed =
    d.startTime && d.endTime ? ((d.endTime - d.startTime) / 1000).toFixed(1) : null;

  const mediaActions = getMediaActions(d.mediaUrl, d.label);
  const detailActions = getDetailActions(d.content);
  const actions = d.mediaUrl ? mediaActions : detailActions;

  const handleDoubleClick = useCallback(() => {
    if (d.mediaUrl) {
      const type = d.mediaType === "video" ? "video" : "image";
      window.dispatchEvent(
        new CustomEvent("canvas-preview", { detail: { url: d.mediaUrl, type, label: d.label } })
      );
    } else if (d.content) {
      window.dispatchEvent(
        new CustomEvent("canvas-preview", { detail: { text: d.content, type: "text" } })
      );
    }
  }, [d.mediaUrl, d.mediaType, d.label, d.content]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, mediaUrl: d.mediaUrl, content: d.content, label: d.label },
      })
    );
  }, [id, d.mediaUrl, d.content, d.label]);

  return (
    <div
      className={`canvas-node canvas-node-tool ${isRunning ? "running" : ""} ${isError ? "error" : ""} ${isDone ? "done" : ""} ${selected ? "selected" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""}`}
      role="article"
      aria-label={`${d.toolName || d.label || "Tool"} — ${d.status || "pending"}`}
      onDoubleClick={handleDoubleClick}
      onContextMenu={handleContextMenu}
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          {isRunning ? (
            <Loader2 className="animate-spin text-accent" size={16} />
          ) : isError ? (
            <AlertCircle className="text-danger" size={16} />
          ) : isDone ? (
            <CheckCircle2 className="text-success" size={16} />
          ) : (
            <Wrench size={16} />
          )}
        </div>
        <span className="canvas-node-label">{d.toolName || d.label || "Tool"}</span>
        {d.status && d.status !== "pending" && (
          <span className={`canvas-node-badge ${d.status === "done" ? "done" : d.status === "error" ? "error" : ""}`}>
            {d.status === "done" ? t("canvas.statusDone") : d.status === "running" ? t("canvas.statusRunning") : d.status === "error" ? t("canvas.statusError") : d.status}
          </span>
        )}
        <LockToggle nodeId={id} locked={d.locked} />
      </div>
      {/* Key input params preview */}
      {d.toolParams && !d.mediaUrl && !d.content && (() => {
        const p = d.toolParams;
        const prompt = typeof p.prompt === "string" ? p.prompt : typeof p.text === "string" ? p.text : "";
        const engine = typeof p.engine === "string" ? p.engine : "";
        if (!prompt && !engine) return null;
        return (
          <div className="canvas-node-body">
            {prompt && <pre className="canvas-node-pre">{prompt.length > 80 ? prompt.slice(0, 80) + "..." : prompt}</pre>}
            {engine && <span className="gen-engine-badge" style={{ marginTop: 2 }}>{engine}</span>}
          </div>
        );
      })()}
      {d.mediaUrl ? (
        <div className="canvas-node-body media">
          {d.mediaType === "video" ? (
            <video src={d.mediaUrl} controls preload="metadata" />
          ) : d.mediaType === "audio" ? (
            <audio src={d.mediaUrl} controls preload="metadata" />
          ) : (
            <img src={d.mediaUrl} alt={d.label || "Output"} loading="lazy" />
          )}
        </div>
      ) : d.content ? (
        <div className="canvas-node-body">
          <pre className="canvas-node-pre">
            {(() => {
              if (!isError) {
                const s = d.content.slice(0, 200);
                return s + (d.content.length > 200 ? "..." : "");
              }
              const msg = d.content.match(/(?:"message"\s*:\s*"([^"]+)")|(?:Invalid\s+\w[^,}\n]{0,100})|(?:\d{3}\s+\w[^,}\n]{0,60})/)?.[1]
                ?? d.content.match(/Invalid\s+\w[^,}\n]{0,100}|Bad Request[^,}\n]{0,60}|error[^,}\n]{0,80}/i)?.[0]
                ?? d.content.slice(0, 100);
              return msg.length > 100 ? msg.slice(0, 100) + "..." : msg;
            })()}
          </pre>
        </div>
      ) : null}
      {elapsed && (
        <div className="canvas-node-footer">
          <span className="time-badge">{elapsed}s</span>
        </div>
      )}
      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
