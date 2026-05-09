import { memo, useMemo, useCallback, useState } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Bot, Zap, CheckCircle2, Loader2, GitBranch, ChevronDown, ChevronUp, BrainCircuit } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar, getDetailActions } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { renderMarkdown } from "@/features/chat/markdown";
import { useT } from "@/features/i18n";
import { useCanvasStore } from "../store";

export const AgentNode = memo(function AgentNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const [thoughtExpanded, setThoughtExpanded] = useState(false);
  const isRunning = d.status === "running";
  const isDone = d.status === "done";
  const isHighlighted = useCanvasStore((s) => s.highlightedTurnId != null && s.highlightedTurnId === d.turnId);
  const elapsed =
    d.startTime && d.endTime ? ((d.endTime - d.startTime) / 1000).toFixed(1) : null;
  const actions = getDetailActions(d.content);

  // Parse reasoning/thinking part and the actual answer
  const parsed = useMemo(() => {
    const raw = d.content || "";
    const thoughtMatch = raw.match(/<thought>([\s\S]*?)<\/thought>/);
    const thought = thoughtMatch ? thoughtMatch[1].trim() : null;
    const answer = raw.replace(/<thought>[\s\S]*?<\/thought>/g, "").trim();
    return { thought, answer };
  }, [d.content]);

  const thoughtHtml = useMemo(() => 
    parsed.thought ? renderMarkdown(parsed.thought) : "", 
  [parsed.thought]);

  const answerHtml = useMemo(() => 
    parsed.answer ? renderMarkdown(parsed.answer) : "", 
  [parsed.answer]);

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
      className={`canvas-node canvas-node-agent ${isRunning ? "running" : ""} ${isDone ? "done" : ""} ${selected ? "selected" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""}`}
      role="article"
      aria-label={`${d.label || "Agent"} — ${d.status || "pending"}`}
      onContextMenu={handleContextMenu}
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div className="canvas-node-header" style={{ cursor: "default" }}>
        <div className="canvas-node-icon-wrapper">
          {isRunning ? (
            <Loader2 className="animate-spin text-accent" size={16} />
          ) : isDone ? (
            <CheckCircle2 className="text-success" size={16} />
          ) : (
            <Bot size={16} />
          )}
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.agent")}</span>
        {isRunning && <Zap size={12} className="text-accent animate-pulse" />}
        <LockToggle nodeId={id} locked={d.locked} />
      </div>

      <div className="canvas-node-body nowheel">
        {/* Thinking / Reasoning Section */}
        {parsed.thought && (
          <div className={`canvas-node-thought-section ${thoughtExpanded ? "expanded" : ""}`}>
            <button 
              className="canvas-node-thought-toggle nodrag" 
              onClick={(e) => { e.stopPropagation(); setThoughtExpanded(!thoughtExpanded); }}
            >
              <BrainCircuit size={12} />
              <span>{t("canvas.thinking")}</span>
              <div style={{ flex: 1 }} />
              {thoughtExpanded ? <ChevronUp size={12} /> : <ChevronDown size={12} />}
            </button>
            {thoughtExpanded && (
              <div 
                className="canvas-node-thought-content nodrag"
                dangerouslySetInnerHTML={{ __html: thoughtHtml }}
              />
            )}
          </div>
        )}

        {/* Actual Answer Section */}
        {parsed.answer && (
          <div
            className="canvas-node-content nodrag"
            dangerouslySetInnerHTML={{ __html: answerHtml }}
          />
        )}

        {isRunning && !parsed.thought && !parsed.answer && (
          <div className="canvas-node-placeholder animate-pulse">
            {t("canvas.generating")}
          </div>
        )}
      </div>

      {(elapsed || isDone) && (
        <div className="canvas-node-footer">
          {elapsed && <span className="time-badge">{elapsed}s</span>}
          {isDone && (
            <button
              className="canvas-branch-btn"
              title={t("canvas.branchFromHere")}
              onClick={(e) => {
                e.stopPropagation();
                useCanvasStore.getState().setPendingBranchNodeId(id);
              }}
            >
              <GitBranch size={12} />
            </button>
          )}
        </div>
      )}
      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
