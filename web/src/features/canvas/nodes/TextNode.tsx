import { useState, memo, useCallback } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { FileText, ChevronDown, ChevronUp } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar, getTextActions } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useT } from "@/features/i18n";
import { useCanvasStore } from "../store";

const COLLAPSE_THRESHOLD = 200;

/** Minimal inline markdown: **bold** and *italic* */
function renderSimpleMarkdown(text: string) {
  const parts: React.ReactNode[] = [];
  const regex = /(\*\*(.+?)\*\*)|(\*(.+?)\*)/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  let key = 0;

  while ((match = regex.exec(text)) !== null) {
    if (match.index > lastIndex) {
      parts.push(text.slice(lastIndex, match.index));
    }
    if (match[1]) {
      parts.push(<strong key={key++}>{match[2]}</strong>);
    } else if (match[3]) {
      parts.push(<em key={key++}>{match[4]}</em>);
    }
    lastIndex = regex.lastIndex;
  }
  if (lastIndex < text.length) {
    parts.push(text.slice(lastIndex));
  }
  return parts;
}

export const TextNode = memo(function TextNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const [expanded, setExpanded] = useState(false);
  const isHighlighted = useCanvasStore((s) => s.highlightedTurnId != null && s.highlightedTurnId === d.turnId);
  const actions = getTextActions(d.content);

  const isLong = (d.content?.length || 0) > COLLAPSE_THRESHOLD;

  const displayContent = expanded || !isLong ? d.content || "" : d.content!.slice(0, COLLAPSE_THRESHOLD) + "...";

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
      className={`canvas-node canvas-node-text ${selected ? "selected" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
      aria-label={d.label || "Text"}
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          <FileText size={14} />
        </div>
        <span className="canvas-node-label">{d.label || "Text"}</span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>
      <div className="canvas-node-body">
        <p className="canvas-node-content canvas-node-md">
          {renderSimpleMarkdown(displayContent)}
        </p>
        {isLong && (
          <button
            className="canvas-node-expand-btn"
            onClick={(e) => {
              e.stopPropagation();
              setExpanded(!expanded);
            }}
          >
            {expanded ? <ChevronUp size={12} /> : <ChevronDown size={12} />}
            {expanded ? t("canvas.collapse") : t("canvas.expand")}
          </button>
        )}
      </div>
      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
