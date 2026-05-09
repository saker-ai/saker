import { useState, useCallback, useEffect, memo, useMemo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Image, Loader2, Sparkles, ChevronUp, ChevronDown, Camera, ArrowUp, Zap } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { useCanvasStore } from "../store";
import { NodeToolbar, getMediaActions } from "./NodeToolbar";
import { useT } from "@/features/i18n";
import { GenTimer } from "./GenTimer";
import { GenErrorBar } from "./GenErrorBar";
import { GenProgressDots } from "./GenProgressDots";
import { useToolSchema } from "./useToolSchema";
import { ToolbarDropdown } from "./ToolbarDropdown";
import { useGenerate, useGenContextMenu } from "./useGenerate";
import { collectLinkedImageNodes, collectReferenceNodes } from "../videoReferences";
import { resolveCanvasReferenceUrl } from "../mediaCache";
import { showCanvasToast } from "../panels/CanvasToast";
import { PromptHistoryButton } from "./PromptHistoryButton";
import { CollapsedSummary } from "./CollapsedSummary";
import { GenHistoryStrip } from "./GenHistoryStrip";
import { LockToggle } from "./LockToggle";

const COUNT_OPTIONS = [1, 2, 4];
const MAX_REF_IMAGES = 3;

function parseReferenceImages(text: string) {
  return text
    .split(/\r?\n|,/)
    .map((value) => value.trim())
    .filter(Boolean);
}

export const ImageGenNode = memo(function ImageGenNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const [prompt, setPrompt] = useState(d.prompt || "");
  const [negativePrompt, setNegativePrompt] = useState(d.negativePrompt || "");
  const [selectedSize, setSelectedSize] = useState(d.size || "1024x1024");
  const [selectedAspectRatio, setSelectedAspectRatio] = useState(d.aspectRatio || "1:1");
  const [selectedResolution, setSelectedResolution] = useState(d.resolution || "2K");
  const [selectedCameraAngle, setSelectedCameraAngle] = useState(d.cameraAngle || "");
  const [selectedEngine, setSelectedEngine] = useState(d.engine || "");
  const [genCount, setGenCount] = useState(d.genCount || 1);
  const [referenceImagesText, setReferenceImagesText] = useState("");
  const { schema, defaultEngine } = useToolSchema("generate_image");
  const actions = getMediaActions(d.mediaUrl, d.label);
  const edges = useCanvasStore((s) => s.edges);
  const nodes = useCanvasStore((s) => s.nodes);
  const linkedImageNodes = useMemo(() => collectLinkedImageNodes(id, edges, nodes), [id, edges, nodes]);
  const linkedImageCount = linkedImageNodes.length;
  const linkedImage = linkedImageCount > 0;
  const referenceBundles = useMemo(() => collectReferenceNodes(id, edges, nodes), [id, edges, nodes]);

  // Set default engine from schema when first loaded
  useEffect(() => {
    if (defaultEngine && !selectedEngine) {
      setSelectedEngine(defaultEngine);
    }
  }, [defaultEngine, selectedEngine]);

  // Persist generation settings to node data
  useEffect(() => {
    updateNode(id, { engine: selectedEngine, size: selectedSize, aspectRatio: selectedAspectRatio, resolution: selectedResolution, cameraAngle: selectedCameraAngle, negativePrompt, genCount } as Partial<CanvasNodeData>);
  }, [selectedEngine, selectedSize, selectedAspectRatio, selectedResolution, selectedCameraAngle, negativePrompt, genCount, id, updateNode]);

  const buildParams = useCallback(async () => {
    const params: Record<string, unknown> = { prompt: prompt.trim(), size: selectedSize };
    if (negativePrompt.trim()) params.negative_prompt = negativePrompt.trim();
    if (selectedAspectRatio) params.aspect_ratio = selectedAspectRatio;
    if (selectedResolution) params.resolution = selectedResolution;
    if (selectedCameraAngle) params.camera_angle = selectedCameraAngle;
    if (selectedEngine) params.engine = selectedEngine;

    const rawRefs = linkedImage
      ? ((await Promise.all(linkedImageNodes.map((node) => resolveCanvasReferenceUrl(node, "image")))).filter(Boolean) as string[])
      : parseReferenceImages(referenceImagesText);

    if (rawRefs.length > MAX_REF_IMAGES) {
      showCanvasToast("error", t("canvas.referenceImagesCapped" as any).replace("{n}", String(MAX_REF_IMAGES)));
    }
    const referenceImages = rawRefs.slice(0, MAX_REF_IMAGES);

    if (referenceImages.length === 1) {
      params.reference_image = referenceImages[0];
    } else if (referenceImages.length > 1) {
      params.reference_images = referenceImages;
    }

    const typedRefs = referenceBundles
      .filter((b) => b.mediaUrl)
      .map((b) => ({ type: b.refType, strength: b.strength, url: b.mediaUrl, media_type: b.mediaType }));
    if (typedRefs.length > 0) {
      params.references = typedRefs;
    }

    return params;
  }, [prompt, selectedSize, negativePrompt, selectedAspectRatio, selectedResolution, selectedCameraAngle, selectedEngine, linkedImage, linkedImageNodes, referenceImagesText, referenceBundles, t]);

  const { handleGenerate } = useGenerate({
    id,
    prompt,
    genCount,
    toolName: "generate_image",
    mediaType: "image",
    buildParams,
    successToastKey: "canvas.imageGenerated",
    failToastKey: "canvas.imageGenFailed",
  });

  const handleContextMenu = useGenContextMenu(id, d.mediaUrl, d.label);

  // Memoized dropdown options
  const arResOptions = useMemo(() => {
    const ars = schema?.aspectRatios || ["1:1"];
    const ress = schema?.resolutions || ["2K"];
    return ars.flatMap((ar) => ress.map((res) => ({ value: `${ar}·${res}`, label: `${ar} · ${res}` })));
  }, [schema]);

  const cameraOptions = useMemo(() => {
    const opts = [{ value: "", label: t("canvas.cameraAngle") }];
    for (const ca of schema?.cameraAngles || []) {
      opts.push({ value: ca, label: t(`canvas.cameraAngle.${ca}` as any) });
    }
    return opts;
  }, [schema, t]);

  const countOptions = useMemo(() =>
    COUNT_OPTIONS.map((c) => ({ value: String(c), label: `${c}${t("canvas.countUnit")}` })),
  [t]);

  const isGenerating = d.generating === true;
  const hasError = d.status === "error" && d.error;

  // Detect param change since last error
  const paramsChanged = useMemo(() => {
    if (!hasError || !d.lastErrorParams) return false;
    try {
      const prev = JSON.parse(d.lastErrorParams);
      const keys = new Set([...Object.keys(prev), "prompt", "size", "aspect_ratio", "resolution", "camera_angle", "engine", "negative_prompt"]);
      const current: Record<string, unknown> = {
        prompt: prompt.trim(),
        size: selectedSize,
        aspect_ratio: selectedAspectRatio,
        resolution: selectedResolution,
        camera_angle: selectedCameraAngle,
        engine: selectedEngine,
        negative_prompt: negativePrompt.trim(),
      };
      for (const k of keys) {
        const a = (prev as Record<string, unknown>)[k] ?? "";
        const b = current[k] ?? "";
        if (typeof a === "string" && typeof b === "string") {
          if (a !== b) return true;
        } else if (JSON.stringify(a) !== JSON.stringify(b)) {
          return true;
        }
      }
      return false;
    } catch {
      return false;
    }
  }, [hasError, d.lastErrorParams, prompt, selectedSize, selectedAspectRatio, selectedResolution, selectedCameraAngle, selectedEngine, negativePrompt]);

  return (
    <div
      className={`canvas-node canvas-node-gen ${selected ? "selected" : ""} ${isGenerating ? "running" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
      aria-label={d.label || "Image Generator"}
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div className="canvas-node-header" onClick={() => updateNode(id, { collapsed: !d.collapsed } as Partial<CanvasNodeData>)} style={{ cursor: "pointer" }}>
        <div className="canvas-node-icon-wrapper gen-icon">
          <Image size={14} />
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.imageGen")}</span>
        {d.collapsed && <CollapsedSummary data={d} nodeKind="image" />}
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
            mediaType="image"
            disabled={isGenerating}
            onSelect={(p) => setPrompt(p)}
          />
        </div>
        <input
          className="gen-negative-prompt nowheel nodrag"
          placeholder={t("canvas.negativePrompt")}
          value={negativePrompt}
          onChange={(e) => setNegativePrompt(e.target.value)}
          disabled={isGenerating}
        />

        {/* Reference images */}
        <div className="gen-param-group">
          <span className="gen-param-label">{t("canvas.referenceImages")}</span>
          {linkedImage && (
            <span className="gen-linked-hint">
              [{t("canvas.linked")} {linkedImageCount}]
            </span>
          )}
        </div>
        <textarea
          className={`gen-prompt nowheel nodrag ${linkedImage ? "linked" : ""}`}
          placeholder={linkedImage ? t("canvas.referenceImagesLinked") : t("canvas.referenceImagesHint")}
          value={linkedImage ? linkedImageNodes.map((n) => n.mediaUrl || "").filter(Boolean).join("\n") : referenceImagesText}
          onChange={(e) => setReferenceImagesText(e.target.value)}
          rows={linkedImage ? Math.min(Math.max(linkedImageCount, 2), 4) : 2}
          disabled={isGenerating || linkedImage}
          title={linkedImage ? linkedImageNodes.map((n) => n.mediaUrl || "").join("\n") : undefined}
        />

        {/* Compact bottom toolbar */}
        <div className="gen-toolbar nodrag">
          {schema && schema.engines.length > 0 && (
            <ToolbarDropdown
              icon={<Sparkles size={12} />}
              options={schema.engines.map((e) => ({ value: e, label: e }))}
              value={selectedEngine}
              onChange={setSelectedEngine}
              disabled={isGenerating}
            />
          )}

          <span className="gen-toolbar-sep">·</span>

          {schema && ((schema.aspectRatios?.length ?? 0) > 0 || (schema.resolutions?.length ?? 0) > 0) && (
            <ToolbarDropdown
              options={arResOptions}
              value={`${selectedAspectRatio}·${selectedResolution}`}
              onChange={(v) => { const [ar, res] = v.split("·"); setSelectedAspectRatio(ar); setSelectedResolution(res); }}
              disabled={isGenerating}
            />
          )}

          {schema && (schema.cameraAngles?.length ?? 0) > 0 && (
            <ToolbarDropdown
              icon={<Camera size={12} />}
              options={cameraOptions}
              value={selectedCameraAngle}
              onChange={setSelectedCameraAngle}
              disabled={isGenerating}
            />
          )}

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
