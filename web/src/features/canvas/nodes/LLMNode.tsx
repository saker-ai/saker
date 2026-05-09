import { memo, useState, useCallback, useEffect, useMemo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Brain, Loader2, Send, ChevronUp, ChevronDown, Languages } from "lucide-react";
import type { CanvasNodeData, CanvasNodeType, LLMMode } from "../types";
import { useRpcStore } from "@/features/rpc/rpcStore";
import { useCanvasStore } from "../store";
import { useHistoryStore } from "../panels/historyStore";
import { useT } from "@/features/i18n";
import { NodeToolbar, getTextActions } from "./NodeToolbar";
import { GenTimer } from "./GenTimer";
import { GenErrorBar } from "./GenErrorBar";
import { ToolbarDropdown } from "./ToolbarDropdown";
import { createDefaultManuscript } from "../manuscript";
import { autoLayoutCanvasAfterGeneration } from "../layoutActions";
import { showCanvasToast } from "../panels/CanvasToast";
import { PromptHistoryButton } from "./PromptHistoryButton";
import { CollapsedSummary } from "./CollapsedSummary";
import { LockToggle } from "./LockToggle";

const MODES: LLMMode[] = ["refine", "translate", "summarize", "custom"];

function composePrompt(mode: LLMMode, input: string, targetLang: string, customInstr: string): string {
  switch (mode) {
    case "refine":
      return `Polish and improve the following text while preserving meaning. Return only the improved text without commentary.\n\n---\n${input}`;
    case "translate":
      return `Translate the following text to ${targetLang || "English"}. Return only the translation without commentary.\n\n---\n${input}`;
    case "summarize":
      return `Summarize the following text concisely. Return only the summary without commentary.\n\n---\n${input}`;
    case "custom":
    default:
      return `${customInstr || "Process the following text as instructed."}\n\n---\n${input}`;
  }
}

function aggregateUpstreamText(nodeId: string): string {
  const { nodes, edges } = useCanvasStore.getState();
  const upstreamIds = edges.filter((e) => e.target === nodeId).map((e) => e.source);
  const parts: string[] = [];
  for (const sid of upstreamIds) {
    const n = nodes.find((x) => x.id === sid);
    if (!n) continue;
    const d = n.data as CanvasNodeData;
    const candidate = d.content || d.prompt || d.manuscriptTitle;
    if (candidate) parts.push(candidate.trim());
  }
  return parts.join("\n\n");
}

export const LLMNode = memo(function LLMNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const [prompt, setPrompt] = useState(d.prompt || "");
  const [mode, setMode] = useState<LLMMode>((d.llmMode as LLMMode) || "refine");
  const [targetLang, setTargetLang] = useState(d.llmTargetLang || "English");
  const [customInstr, setCustomInstr] = useState(d.llmCustomInstructions || "");
  const rpc = useRpcStore((s) => s.rpc);
  const actions = getTextActions(prompt);

  useEffect(() => {
    updateNode(id, {
      prompt,
      llmMode: mode,
      llmTargetLang: targetLang,
      llmCustomInstructions: customInstr,
    } as Partial<CanvasNodeData>);
  }, [prompt, mode, targetLang, customInstr, id, updateNode]);

  const handleGenerate = useCallback(async () => {
    if (!rpc) return;
    const upstream = aggregateUpstreamText(id);
    const userInput = upstream || prompt.trim();
    if (!userInput) {
      showCanvasToast("error", t("canvas.llmNoInput"));
      return;
    }
    updateNode(id, { generating: true, status: "running", error: undefined, startTime: Date.now(), endTime: undefined } as Partial<CanvasNodeData>);
    try {
      const composed = composePrompt(mode, userInput, targetLang, customInstr);
      const result = await rpc.request<{ text: string }>("canvas/text-gen", { prompt: composed });
      const store = useCanvasStore.getState();
      const currentNode = store.nodes.find((n) => n.id === id);
      if (currentNode) {
        const manuscript = createDefaultManuscript(t("canvas.aiTypo"), result.text);
        const newNodeId = store.addNode({
          type: "aiTypo",
          position: { x: currentNode.position.x + 320, y: currentNode.position.y },
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
        store.addEdge({ id: `edge-${id}-${newNodeId}`, source: id, target: newNodeId, type: "flow" });
        useHistoryStore.getState().addEntry({ type: "text", prompt: prompt.trim() || userInput.slice(0, 120), mediaUrl: "", params: { mode, targetLang } });
      }
      updateNode(id, { generating: false, status: "pending", error: undefined, endTime: Date.now() } as Partial<CanvasNodeData>);
      autoLayoutCanvasAfterGeneration();
    } catch (err) {
      updateNode(id, { generating: false, status: "error", error: String(err), endTime: Date.now() } as Partial<CanvasNodeData>);
      showCanvasToast("error", t("canvas.llmFailed"));
    }
  }, [rpc, id, prompt, mode, targetLang, customInstr, t, updateNode]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, content: prompt, label: d.label },
      })
    );
  }, [id, prompt, d.label]);

  const modeOptions = useMemo(
    () => MODES.map((m) => ({ value: m, label: t(`canvas.llmMode.${m}` as any) || m })),
    [t],
  );

  const isGenerating = d.generating === true;
  const hasError = d.status === "error" && d.error;

  return (
    <div
      className={`canvas-node canvas-node-gen ${selected ? "selected" : ""} ${isGenerating ? "running" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
      aria-label={d.label || "LLM"}
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div
        className="canvas-node-header"
        onClick={() => updateNode(id, { collapsed: !d.collapsed } as Partial<CanvasNodeData>)}
        style={{ cursor: "pointer" }}
      >
        <div className="canvas-node-icon-wrapper gen-icon">
          <Brain size={14} />
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.llm" as any) || "LLM"}</span>
        {d.collapsed && <CollapsedSummary data={d} nodeKind="text" />}
        <LockToggle nodeId={id} locked={d.locked} />
        <span className="gen-collapse-toggle">
          {d.collapsed ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
        </span>
      </div>

      {!d.collapsed && (
        <div className="canvas-node-body gen-body">
          <div className="gen-prompt-wrapper">
            <textarea
              className="gen-prompt nowheel nodrag"
              placeholder={t("canvas.llmPromptHint")}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={3}
              disabled={isGenerating}
            />
            <PromptHistoryButton mediaType="text" disabled={isGenerating} onSelect={(p) => setPrompt(p)} />
          </div>

          {mode === "translate" && (
            <input
              className="gen-negative-prompt nowheel nodrag"
              placeholder={t("canvas.llmTargetLang" as any) || "Target language"}
              value={targetLang}
              onChange={(e) => setTargetLang(e.target.value)}
              disabled={isGenerating}
            />
          )}
          {mode === "custom" && (
            <input
              className="gen-negative-prompt nowheel nodrag"
              placeholder={t("canvas.llmCustomInstr" as any) || "Custom instruction"}
              value={customInstr}
              onChange={(e) => setCustomInstr(e.target.value)}
              disabled={isGenerating}
            />
          )}

          <div className="gen-toolbar nodrag">
            <ToolbarDropdown
              icon={<Languages size={12} />}
              options={modeOptions}
              value={mode}
              onChange={(v) => setMode(v as LLMMode)}
              disabled={isGenerating}
            />
            <div style={{ flex: 1 }} />
            <button
              className="gen-toolbar-submit"
              onClick={handleGenerate}
              disabled={isGenerating}
              title={t("canvas.generate")}
            >
              {isGenerating ? <Loader2 size={16} className="animate-spin" /> : <Send size={16} />}
            </button>
          </div>
          <GenTimer generating={isGenerating} startTime={d.startTime} endTime={d.endTime} />
        </div>
      )}

      {hasError && <GenErrorBar error={d.error} onRetry={handleGenerate} />}

      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
