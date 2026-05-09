import { useState, useCallback, useEffect, memo, useMemo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Video, Loader2, Sparkles, ChevronUp, ChevronDown, ArrowUp, Zap } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { useCanvasStore } from "../store";
import { NodeToolbar, getMediaActions } from "./NodeToolbar";
import { useT } from "@/features/i18n";
import { collectVideoReferences, collectLinkedImageNodes, collectReferenceNodes } from "../videoReferences";
import { resolveCanvasReferenceUrl } from "../mediaCache";
import { GenTimer } from "./GenTimer";
import { GenErrorBar } from "./GenErrorBar";
import { GenProgressDots } from "./GenProgressDots";
import { useToolSchema } from "./useToolSchema";
import { ToolbarDropdown } from "./ToolbarDropdown";
import { useGenerate, useGenContextMenu } from "./useGenerate";
import { showCanvasToast } from "../panels/CanvasToast";
import { PromptHistoryButton } from "./PromptHistoryButton";
import { CollapsedSummary } from "./CollapsedSummary";
import { GenHistoryStrip } from "./GenHistoryStrip";
import { LockToggle } from "./LockToggle";

const COUNT_OPTIONS = [1, 2];
const DURATION_OPTIONS = [5, 10];
const MAX_REF_IMAGES = 3;

function parseReferenceImages(text: string) {
  return text
    .split(/\r?\n|,/)
    .map((value) => value.trim())
    .filter(Boolean);
}

export const VideoGenNode = memo(function VideoGenNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const [prompt, setPrompt] = useState(d.prompt || "");
  const [selectedSize, setSelectedSize] = useState(d.size || "1280*720");
  const [selectedResolution, setSelectedResolution] = useState(d.resolution || "720P");
  const [selectedAspectRatio, setSelectedAspectRatio] = useState(d.aspectRatio || "16:9");
  const [selectedEngine, setSelectedEngine] = useState(d.engine || "");
  const [duration, setDuration] = useState(d.duration || 5);
  const [genCount, setGenCount] = useState(d.genCount || 1);
  const [referenceImagesText, setReferenceImagesText] = useState("");
  const [referenceVideo, setReferenceVideo] = useState("");
  const { schema, defaultEngine } = useToolSchema("generate_video");
  const actions = getMediaActions(d.mediaUrl, d.label, "video");
  const edges = useCanvasStore((s) => s.edges);
  const nodes = useCanvasStore((s) => s.nodes);
  const linkedReferences = useMemo(() => collectVideoReferences(id, edges, nodes), [id, edges, nodes]);
  const linkedImageUrls = linkedReferences.imageUrls;
  const linkedVideoUrls = linkedReferences.videoUrls;
  const linkedImageNodes = useMemo(() => collectLinkedImageNodes(id, edges, nodes), [id, edges, nodes]);
  const referenceBundles = useMemo(() => collectReferenceNodes(id, edges, nodes), [id, edges, nodes]);

  // Set default engine from schema when first loaded
  useEffect(() => {
    if (defaultEngine && !selectedEngine) {
      setSelectedEngine(defaultEngine);
    }
  }, [defaultEngine, selectedEngine]);

  // Persist generation settings to node data
  useEffect(() => {
    updateNode(id, { engine: selectedEngine, size: selectedSize, resolution: selectedResolution, aspectRatio: selectedAspectRatio, duration, genCount } as Partial<CanvasNodeData>);
  }, [selectedEngine, selectedSize, selectedResolution, selectedAspectRatio, duration, genCount, id, updateNode]);

  const buildParams = useCallback(async () => {
    const params: Record<string, unknown> = {
      prompt: prompt.trim(),
      size: selectedSize,
      resolution: selectedResolution,
      aspect_ratio: selectedAspectRatio,
      duration,
    };
    const rawRefs =
      linkedImageNodes.length > 0
        ? ((await Promise.all(linkedImageNodes.map((node) => resolveCanvasReferenceUrl(node, "image")))).filter(Boolean) as string[])
        : parseReferenceImages(referenceImagesText);
    if (rawRefs.length > MAX_REF_IMAGES) {
      showCanvasToast("error", t("canvas.referenceImagesCapped" as any).replace("{n}", String(MAX_REF_IMAGES)));
    }
    const referenceImages = rawRefs.slice(0, MAX_REF_IMAGES);
    const resolvedReferenceVideo = linkedVideoUrls[0] || referenceVideo.trim();

    if (selectedEngine) params.engine = selectedEngine;
    if (referenceImages.length === 1) {
      params.reference_image = referenceImages[0];
    }
    if (referenceImages.length > 1) {
      params.reference_images = referenceImages;
    }
    if (resolvedReferenceVideo) {
      params.reference_video = resolvedReferenceVideo;
    }

    const typedRefs = referenceBundles
      .filter((b) => b.mediaUrl)
      .map((b) => ({ type: b.refType, strength: b.strength, url: b.mediaUrl, media_type: b.mediaType }));
    if (typedRefs.length > 0) {
      params.references = typedRefs;
    }

    return params;
  }, [prompt, selectedSize, selectedResolution, selectedAspectRatio, duration, selectedEngine, linkedImageNodes, linkedVideoUrls, referenceImagesText, referenceVideo, referenceBundles, t]);

  const { handleGenerate } = useGenerate({
    id,
    prompt,
    genCount,
    toolName: "generate_video",
    mediaType: "video",
    buildParams,
    successToastKey: "canvas.videoGenerated",
    failToastKey: "canvas.videoGenFailed",
  });

  const handleContextMenu = useGenContextMenu(id, d.mediaUrl, d.label);

  const effectiveEngines = schema?.engines || [];

  // Memoized dropdown options
  const engineOptions = useMemo(() =>
    effectiveEngines.map((e) => ({ value: e, label: e })),
  [effectiveEngines]);

  const resolutionDurationOptions = useMemo(() => {
    const ress = schema?.resolutions || ["720P"];
    return ress.flatMap((res) =>
      DURATION_OPTIONS.map((dur) => ({ value: `${res}·${dur}`, label: `${res} · ${dur}s` }))
    );
  }, [schema]);

  const countOptions = useMemo(() =>
    COUNT_OPTIONS.map((c) => ({ value: String(c), label: `${c}${t("canvas.countUnit")}` })),
  [t]);

  const linkedImage = linkedImageUrls.length > 0;
  const linkedVideo = linkedVideoUrls.length > 0;
  const isGenerating = d.generating === true;
  const hasError = d.status === "error" && d.error;

  const paramsChanged = useMemo(() => {
    if (!hasError || !d.lastErrorParams) return false;
    try {
      const prev = JSON.parse(d.lastErrorParams) as Record<string, unknown>;
      const currentKeys: Array<[string, unknown]> = [
        ["prompt", prompt.trim()],
        ["size", selectedSize],
        ["resolution", selectedResolution],
        ["aspect_ratio", selectedAspectRatio],
        ["duration", duration],
        ["engine", selectedEngine],
      ];
      for (const [k, v] of currentKeys) {
        if ((prev[k] ?? "") !== (v ?? "")) return true;
      }
      return false;
    } catch {
      return false;
    }
  }, [hasError, d.lastErrorParams, prompt, selectedSize, selectedResolution, selectedAspectRatio, duration, selectedEngine]);

  return (
    <div
      className={`canvas-node canvas-node-gen ${selected ? "selected" : ""} ${isGenerating ? "running" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
      aria-label={d.label || "Video Generator"}
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div className="canvas-node-header" onClick={() => updateNode(id, { collapsed: !d.collapsed } as Partial<CanvasNodeData>)} style={{ cursor: "pointer" }}>
        <div className="canvas-node-icon-wrapper gen-icon">
          <Video size={14} />
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.videoGen")}</span>
        {d.collapsed && <CollapsedSummary data={d} nodeKind="video" />}
        <LockToggle nodeId={id} locked={d.locked} />
        <span className="gen-collapse-toggle">
          {d.collapsed ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
        </span>
      </div>

      {!d.collapsed && <div className="canvas-node-body gen-body">
        <div className="gen-prompt-wrapper">
          <textarea
            className="gen-prompt nowheel nodrag"
            placeholder={t("canvas.prompt")}
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            rows={3}
            disabled={isGenerating}
          />
          <PromptHistoryButton
            mediaType="video"
            disabled={isGenerating}
            onSelect={(p) => setPrompt(p)}
          />
        </div>

        {/* Reference images */}
        <div className="gen-param-group">
          <span className="gen-param-label">{t("canvas.referenceImages")}</span>
          {linkedImage && (
            <span className="gen-linked-hint">
              [{t("canvas.linked")} {linkedImageUrls.length}]
            </span>
          )}
        </div>
        <textarea
          className={`gen-prompt nowheel nodrag ${linkedImage ? "linked" : ""}`}
          placeholder={linkedImage ? t("canvas.referenceImagesLinked") : t("canvas.referenceImagesHint")}
          value={linkedImage ? linkedImageUrls.join("\n") : referenceImagesText}
          onChange={(e) => setReferenceImagesText(e.target.value)}
          rows={linkedImage ? Math.min(Math.max(linkedImageUrls.length, 2), 4) : 2}
          disabled={isGenerating || linkedImage}
          title={linkedImage ? linkedImageUrls.join("\n") : undefined}
        />

        {/* Reference video */}
        <input
          className={`gen-negative-prompt nowheel nodrag ${linkedVideo ? "linked" : ""}`}
          placeholder={linkedVideo ? `[${t("canvas.linked")}] ${t("canvas.referenceVideo")}` : t("canvas.referenceVideo")}
          value={linkedVideo ? linkedVideoUrls[0] : referenceVideo}
          onChange={(e) => setReferenceVideo(e.target.value)}
          disabled={isGenerating || linkedVideo}
          title={linkedVideo ? linkedVideoUrls[0] : undefined}
        />

        {/* Compact bottom toolbar */}
        <div className="gen-toolbar nodrag">
          {effectiveEngines.length > 0 && (
            <ToolbarDropdown
              icon={<Sparkles size={12} />}
              options={engineOptions}
              value={selectedEngine}
              onChange={setSelectedEngine}
              disabled={isGenerating}
            />
          )}

          <span className="gen-toolbar-sep">·</span>

          <ToolbarDropdown
            options={resolutionDurationOptions}
            value={`${selectedResolution}·${duration}`}
            onChange={(v) => { const [res, dur] = v.split("·"); setSelectedResolution(res); setDuration(Number(dur)); }}
            disabled={isGenerating}
          />

          <ToolbarDropdown
            icon={<Zap size={10} />}
            options={countOptions}
            value={String(genCount)}
            onChange={(v) => setGenCount(Number(v))}
            disabled={isGenerating}
          />

          <div style={{ flex: 1 }} />

          <button className="gen-toolbar-submit" onClick={handleGenerate} disabled={isGenerating || !prompt.trim()} title={t("canvas.generate")}>
            {isGenerating ? <Loader2 size={16} className="animate-spin" /> : <Sparkles size={16} />}
          </button>
        </div>
        <GenTimer generating={isGenerating} startTime={d.startTime} endTime={d.endTime} />
        <GenProgressDots generating={isGenerating} progress={d.genProgress} total={genCount} />
        <GenHistoryStrip nodeId={id} history={d.generationHistory} activeIndex={d.activeHistoryIndex} />
      </div>}

      {hasError && <GenErrorBar error={d.error} onRetry={handleGenerate} paramsChanged={paramsChanged} />}

      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
