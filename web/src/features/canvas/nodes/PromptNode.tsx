import { memo, useCallback } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { MessageSquare } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar, getTextActions } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";

export const PromptNode = memo(function PromptNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const actions = getTextActions(d.content);
  const isHighlighted = useCanvasStore((s) => s.highlightedTurnId != null && s.highlightedTurnId === d.turnId);

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
      className={`canvas-node canvas-node-prompt ${selected ? "selected" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""}`}
      role="article"
      aria-label={`${d.label || "Prompt"}: ${(d.content || "").slice(0, 50)}`}
      onContextMenu={handleContextMenu}
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          <MessageSquare size={14} />
        </div>
        <span className="canvas-node-label">{d.label === "Prompt" ? t("canvas.promptLabel") : d.label || t("canvas.promptLabel")}</span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>
      <div className="canvas-node-body nowheel">
        <p className="canvas-node-content">{d.content}</p>
      </div>
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
