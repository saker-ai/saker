import { memo, useCallback } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Zap, CheckCircle2, Loader2 } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar, getDetailActions } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useCanvasStore } from "../store";

export const SkillNode = memo(function SkillNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const isRunning = d.status === "running";
  const isDone = d.status === "done";
  const isHighlighted = useCanvasStore((s) => s.highlightedTurnId != null && s.highlightedTurnId === d.turnId);
  const elapsed =
    d.startTime && d.endTime ? ((d.endTime - d.startTime) / 1000).toFixed(1) : null;
  const actions = getDetailActions(d.content);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, content: d.content, label: d.label },
      })
    );
  }, [id, d.content, d.label]);

  return (
    <div
      className={`canvas-node canvas-node-skill ${isRunning ? "running" : ""} ${isDone ? "done" : ""} ${selected ? "selected" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
      aria-label={d.label || "Skill"}
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          {isRunning ? (
            <Loader2 className="animate-spin text-accent" size={16} />
          ) : isDone ? (
            <CheckCircle2 className="text-success" size={16} />
          ) : (
            <Zap size={16} />
          )}
        </div>
        <span className="canvas-node-label">{d.label || "Skill"}</span>
        {d.status && d.status !== "pending" && (
          <span className={`canvas-node-badge ${d.status === "done" ? "done" : d.status === "error" ? "error" : ""}`}>
            {d.status}
          </span>
        )}
        <LockToggle nodeId={id} locked={d.locked} />
      </div>
      {d.content && (
        <div className="canvas-node-body">
          <p className="canvas-node-content">
            {d.content.slice(0, 150)}
            {d.content.length > 150 ? "..." : ""}
          </p>
        </div>
      )}
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
