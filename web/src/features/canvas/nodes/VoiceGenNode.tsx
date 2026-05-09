import { useState, useCallback, useEffect, memo, useMemo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Music, Loader2, Sparkles, ChevronDown, ChevronUp, User, Languages } from "lucide-react";
import type { CanvasNodeData, CanvasNodeType } from "../types";
import { useCanvasStore } from "../store";
import { NodeToolbar } from "./NodeToolbar";
import { useHistoryStore } from "../panels/historyStore";
import { useT } from "@/features/i18n";
import { autoLayoutCanvasAfterGeneration } from "../layoutActions";
import { GenTimer } from "./GenTimer";
import { ToolbarDropdown } from "./ToolbarDropdown";
import { GenErrorBar } from "./GenErrorBar";
import { showCanvasToast } from "../panels/CanvasToast";
import { submitAndPollTask } from "../taskPoller";
import { useToolSchema } from "./useToolSchema";
import { PromptHistoryButton } from "./PromptHistoryButton";
import { CollapsedSummary } from "./CollapsedSummary";
import { LockToggle } from "./LockToggle";

export const VoiceGenNode = memo(function VoiceGenNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const addNode = useCanvasStore((s) => s.addNode);
  const addEdge = useCanvasStore((s) => s.addEdge);
  
  const [text, setText] = useState(d.prompt || "");
  const [voice, setVoice] = useState(d.voice || "");
  const [language, setLanguage] = useState(d.language || "");
  const [instructions, setInstructions] = useState(d.instructions || "");
  const [selectedEngine, setSelectedEngine] = useState(d.engine || "");

  // Pass selectedEngine to enable dynamic capabilities fetching (e.g. engine-specific voices)
  const { schema, defaultEngine } = useToolSchema("text_to_speech", selectedEngine);

  useEffect(() => {
    if (defaultEngine && !selectedEngine) setSelectedEngine(defaultEngine);
  }, [defaultEngine, selectedEngine]);

  useEffect(() => {
    if (schema?.voices && schema.voices.length > 0 && !voice) setVoice(schema.voices[0]);
  }, [schema, voice]);

  useEffect(() => {
    updateNode(id, { engine: selectedEngine, voice, language, instructions, prompt: text } as Partial<CanvasNodeData>);
  }, [selectedEngine, voice, language, instructions, text, id, updateNode]);

  const handleGenerate = useCallback(async () => {
    if (!text.trim()) return;

    updateNode(id, {
      generating: true,
      error: undefined,
      status: "running",
      startTime: Date.now(),
    } as Partial<CanvasNodeData>);

    // Dynamic tool selection: if engine includes "music", use music task
    const isMusic = selectedEngine.toLowerCase().includes("music") || text.toLowerCase().includes("music");
    const toolName = isMusic ? "generate_music" : "text_to_speech";

    const params: Record<string, unknown> = { text: text.trim(), prompt: text.trim(), voice: voice.trim() };
    if (language.trim()) params.language = language.trim();
    if (instructions.trim()) params.instructions = instructions.trim();
    if (selectedEngine) params.engine = selectedEngine;

    try {
      const result = await submitAndPollTask(toolName, params, id);

      if (result.success && result.structured?.media_url) {
        const mediaUrl = result.structured.media_url;
        const thisNode = useCanvasStore.getState().nodes.find((n) => n.id === id);
        const newNodeId = addNode({
          type: "audio",
          position: {
            x: (thisNode?.position.x || 0) + 350,
            y: thisNode?.position.y || 0,
          },
          data: {
            nodeType: "audio" as CanvasNodeType,
            label: text.trim().slice(0, 30),
            mediaUrl,
            mediaType: "audio",
            status: "done",
          },
        });
        addEdge({ id: `edge_${id}_${newNodeId}`, source: id, target: newNodeId, type: "flow" });
        useHistoryStore.getState().addEntry({ type: "audio", prompt: text.trim(), mediaUrl, params });
        updateNode(id, { generating: false, status: "pending", error: undefined, endTime: Date.now() } as Partial<CanvasNodeData>);
        showCanvasToast("success", t("canvas.audioGenerated") || t("canvas.voiceGenerated")); 
        autoLayoutCanvasAfterGeneration();
        return;
      }

      updateNode(id, {
        generating: false,
        status: "error",
        error: result.output || t("canvas.error"),
        endTime: Date.now(),
      } as Partial<CanvasNodeData>);
    } catch (err) {
      updateNode(id, {
        generating: false,
        status: "error",
        error: String(err),
        endTime: Date.now(),
      } as Partial<CanvasNodeData>);
      showCanvasToast("error", t("canvas.audioGenFailed") || t("canvas.voiceGenFailed"));
    }
  }, [id, text, voice, language, instructions, selectedEngine, updateNode, addNode, addEdge, t]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, mediaUrl: d.mediaUrl, label: d.label },
      })
    );
  }, [id, d.mediaUrl, d.label]);

  const voiceOptions = useMemo(() => (schema?.voices || []).map(v => ({ value: v, label: v || "Default" })), [schema]);
  const langOptions = useMemo(() => (schema?.languages || []).map(l => ({ value: l, label: l || "Auto" })), [schema]);
  const engineOptions = useMemo(() => (schema?.engines || []).map(e => ({ value: e, label: e })), [schema]);

  const isGenerating = d.generating === true;
  const hasError = d.status === "error" && d.error;

  return (
    <div
      className={`canvas-node canvas-node-gen ${selected ? "selected" : ""} ${isGenerating ? "running" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
      aria-label={d.label || "Audio Generator"}
    >
      <NodeToolbar nodeId={id} selected={selected} />
      <div className="canvas-node-header" onClick={() => updateNode(id, { collapsed: !d.collapsed } as Partial<CanvasNodeData>)} style={{ cursor: "pointer" }}>
        <div className="canvas-node-icon-wrapper gen-icon">
          <Music size={14} />
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.audioGen")}</span>
        {d.collapsed && <CollapsedSummary data={d} nodeKind="voice" />}
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
              placeholder={t("canvas.audioText")}
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={3}
              disabled={isGenerating}
            />
            <PromptHistoryButton
              mediaType="audio"
              disabled={isGenerating}
              onSelect={(p) => setText(p)}
            />
          </div>

          <input
            className="gen-negative-prompt nowheel nodrag"
            placeholder={t("canvas.instructions")}
            value={instructions}
            onChange={(e) => setInstructions(e.target.value)}
            disabled={isGenerating}
          />

          <div className="gen-toolbar nodrag">
            {engineOptions.length > 0 && (
              <ToolbarDropdown
                icon={<Sparkles size={12} />}
                options={engineOptions}
                value={selectedEngine}
                onChange={setSelectedEngine}
                disabled={isGenerating}
              />
            )}

            <span className="gen-toolbar-sep">·</span>

            {voiceOptions.length > 0 && (
              <ToolbarDropdown
                icon={<User size={12} />}
                options={voiceOptions}
                value={voice}
                onChange={setVoice}
                disabled={isGenerating}
              />
            )}

            {langOptions.length > 0 && (
              <ToolbarDropdown
                icon={<Languages size={12} />}
                options={langOptions}
                value={language}
                onChange={setLanguage}
                disabled={isGenerating}
              />
            )}

            <div style={{ flex: 1 }} />

            <button
              className="gen-toolbar-submit"
              onClick={handleGenerate}
              disabled={isGenerating || !text.trim()}
              title={t("canvas.generate")}
            >
              {isGenerating ? <Loader2 size={16} className="animate-spin" /> : <Sparkles size={16} />}
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
