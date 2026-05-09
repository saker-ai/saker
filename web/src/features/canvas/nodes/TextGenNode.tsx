import { memo, useState, useCallback, useEffect } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Sparkles, Loader2, Send, ChevronUp, ChevronDown } from "lucide-react";
import type { CanvasNodeData, CanvasNodeType } from "../types";
import { useRpcStore } from "@/features/rpc/rpcStore";
import { useCanvasStore } from "../store";
import { useHistoryStore } from "../panels/historyStore";
import { useT } from "@/features/i18n";
import { NodeToolbar, getTextActions } from "./NodeToolbar";
import { GenTimer } from "./GenTimer";
import { createDefaultManuscript } from "../manuscript";
import { PromptHistoryButton } from "./PromptHistoryButton";
import { CollapsedSummary } from "./CollapsedSummary";
import { LockToggle } from "./LockToggle";

export const TextGenNode = memo(function TextGenNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const [prompt, setPrompt] = useState(d.prompt || "");
  const [loading, setLoading] = useState(false);
  const rpc = useRpcStore((s) => s.rpc);
  const isHighlighted = useCanvasStore((s) => s.highlightedTurnId != null && s.highlightedTurnId === d.turnId);
  const actions = getTextActions(prompt);

  useEffect(() => {
    updateNode(id, { prompt } as Partial<CanvasNodeData>);
  }, [prompt, id, updateNode]);

  const handleGenerate = useCallback(async () => {
    if (!prompt.trim() || !rpc) return;
    setLoading(true);
    updateNode(id, { generating: true, startTime: Date.now(), endTime: undefined } as Partial<CanvasNodeData>);

    try {
      const result = await rpc.request<{ text: string }>("canvas/text-gen", { prompt });
      const store = useCanvasStore.getState();
      const nodes = store.nodes;
      const currentNode = nodes.find(n => n.id === id);
      
      if (currentNode) {
        const manuscript = createDefaultManuscript(t("canvas.aiTypo"), result.text);
        const newPos = { 
          x: currentNode.position.x + 320, 
          y: currentNode.position.y 
        };
        
        const newNodeId = store.addNode({
          type: "aiTypo",
          position: newPos,
          data: {
            nodeType: "aiTypo" as CanvasNodeType,
            label: t("canvas.aiTypo"),
            status: "done",
            content: result.text,
            manuscriptTitle: manuscript.manuscriptTitle,
            manuscriptSections: manuscript.manuscriptSections,
            manuscriptEntities: manuscript.manuscriptEntities,
            manuscriptViewMode: manuscript.manuscriptViewMode,
            manuscriptEditorMode: manuscript.manuscriptEditorMode,
          },
        });

        store.addEdge({
          id: `edge-${id}-${newNodeId}`,
          source: id,
          target: newNodeId,
          type: "flow",
        });
        useHistoryStore.getState().addEntry({ type: "text", prompt: prompt.trim(), mediaUrl: "", params: {} });
      }
      updateNode(id, { generating: false, endTime: Date.now() } as Partial<CanvasNodeData>);
    } catch (err) {
      console.error("Text generation failed", err);
      updateNode(id, { generating: false, status: "error", error: String(err) } as Partial<CanvasNodeData>);
    } finally {
      setLoading(false);
    }
  }, [prompt, rpc, id, t, updateNode]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, content: prompt, label: d.label },
      })
    );
  }, [id, prompt, d.label]);

  const isGenerating = d.generating === true || loading;

  return (
    <div
      className={`canvas-node canvas-node-gen ${selected ? "selected" : ""} ${isGenerating ? "running" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div 
        className="canvas-node-header" 
        onClick={() => updateNode(id, { collapsed: !d.collapsed } as Partial<CanvasNodeData>)}
        style={{ cursor: "pointer" }}
      >
        <div className="canvas-node-icon-wrapper gen-icon">
          <Sparkles size={14} className="text-accent" />
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.textGen")}</span>
        {d.collapsed && <CollapsedSummary data={d} nodeKind="text" />}
        <LockToggle nodeId={id} locked={d.locked} />
        <span className="gen-collapse-toggle">
          {d.collapsed ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
        </span>
      </div>

      {!d.collapsed && (
        <div className="canvas-node-body gen-body nowheel">
          <div className="gen-prompt-wrapper">
            <textarea
              className="gen-prompt nowheel nodrag"
              placeholder={t("canvas.prompt")}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={4}
              disabled={isGenerating}
            />
            <PromptHistoryButton
              mediaType="text"
              disabled={isGenerating}
              onSelect={(p) => setPrompt(p)}
            />
          </div>
          <div className="gen-toolbar nodrag">
            <div style={{ flex: 1 }} />
            <button 
              className="gen-toolbar-submit" 
              onClick={handleGenerate} 
              disabled={isGenerating || !prompt.trim()}
              title={t("canvas.generate")}
            >
              {isGenerating ? <Loader2 size={16} className="animate-spin" /> : <Send size={16} />}
            </button>
          </div>
          <GenTimer generating={isGenerating} startTime={d.startTime} endTime={d.endTime} />
        </div>
      )}

      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
