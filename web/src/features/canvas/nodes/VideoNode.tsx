import { useRef, useState, useCallback, useEffect, memo, useMemo } from "react";
import { Handle, Position, type NodeProps, useNodeConnections, useReactFlow, NodeResizer } from "@xyflow/react";
import { Video, Camera, SkipBack, SkipForward, Pencil, Loader2, Sparkles, ChevronDown } from "lucide-react";
import type { CanvasNode, CanvasNodeData, CanvasNodeType } from "../types";
import { NodeToolbar, getMediaActions } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useCanvasStore } from "../store";
import { useHistoryStore } from "../panels/historyStore";
import { useT } from "@/features/i18n";
import { cacheCanvasMedia } from "../mediaCache";
import { submitAndPollTask } from "../taskPoller";
import { GenTimer } from "./GenTimer";
import { EngineSelector } from "./EngineSelector";
import { GenErrorBar } from "./GenErrorBar";
import { autoLayoutCanvasAfterGeneration } from "../layoutActions";
import { showCanvasToast } from "../panels/CanvasToast";
import { MediaDropZone } from "./MediaDropZone";
import { isValidMediaUrl } from "../mediaUrl";
import { useToolSchema } from "./useToolSchema";

const EDIT_PRESETS = [
  { key: "editStyle" as const, prompt: "Transform the video style to " },
  { key: "editEnhance" as const, prompt: "Enhance the video quality, make it sharper and more vivid" },
];

const VIDEO_SIZE_OPTIONS = [
  { label: "1280×720", value: "1280*720" },
  { label: "960×960", value: "960*960" },
  { label: "720×1280", value: "720*1280" },
  { label: "1920×1080", value: "1920*1080" },
  { label: "1080×1920", value: "1080*1920" },
];

function formatTime(sec: number) {
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return `${m}:${s.toString().padStart(2, "0")}`;
}

export const VideoNode = memo(function VideoNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const addNode = useCanvasStore((s) => s.addNode);
  const addEdge = useCanvasStore((s) => s.addEdge);
  const { getNode } = useReactFlow();
  const videoRef = useRef<HTMLVideoElement>(null);
  const [info, setInfo] = useState<{ duration: string; resolution: string } | null>(null);

  const [editMode, setEditMode] = useState(d.editOnCreate === true);
  const [editPrompt, setEditPrompt] = useState("");
  const [editing, setEditing] = useState(false);
  const [editError, setEditError] = useState("");
  const { schema: editSchema, defaultEngine } = useToolSchema("edit_video");
  const [selectedEngine, setSelectedEngine] = useState(d.engine || "");
  const [editSize, setEditSize] = useState("");

  // Detect upstream video from connections
  const connections = useNodeConnections({ handleType: "target" });
  const sourceNodeId = connections[0]?.source;

  // Targeted selector for source node data
  const sourceNodeData = useCanvasStore(
    useCallback((s) => {
      if (!sourceNodeId) return null;
      return s.nodes.find((n) => n.id === sourceNodeId)?.data as CanvasNodeData | undefined;
    }, [sourceNodeId])
  );

  const upstreamMediaUrl = useMemo(() => {
    if (d.mediaUrl || !sourceNodeData) return null;
    const srcData = sourceNodeData;
    if (srcData.nodeType === "video" && srcData.mediaUrl) return srcData.mediaUrl;
    if (srcData.nodeType === "tool" && srcData.mediaType === "video" && srcData.mediaUrl) return srcData.mediaUrl;
    return null;
  }, [d.mediaUrl, sourceNodeData]);

  // Auto-adopt upstream video
  useEffect(() => {
    if (upstreamMediaUrl && !d.mediaUrl) {
      updateNode(id, { mediaUrl: upstreamMediaUrl } as Partial<CanvasNodeData>);
    }
  }, [upstreamMediaUrl, d.mediaUrl, id, updateNode]);

  // Auto-set default engine from schema
  useEffect(() => {
    if (defaultEngine && !selectedEngine) setSelectedEngine(defaultEngine);
  }, [defaultEngine, selectedEngine]);

  const handleMetadata = useCallback(() => {
    const v = videoRef.current;
    if (!v) return;
    setInfo({
      duration: formatTime(v.duration),
      resolution: `${v.videoWidth}×${v.videoHeight}`,
    });
  }, []);

  const doCapture = useCallback(async (label: string) => {
    const v = videoRef.current;
    if (!v) return;
    const cvs = document.createElement("canvas");
    cvs.width = v.videoWidth || v.clientWidth;
    cvs.height = v.videoHeight || v.clientHeight;
    const ctx = cvs.getContext("2d");
    if (!ctx) return;
    ctx.drawImage(v, 0, 0, cvs.width, cvs.height);
    const dataUrl = cvs.toDataURL("image/png");
    const stabilized = await cacheCanvasMedia(dataUrl, "image");
    const finalUrl = stabilized.mediaUrl || dataUrl;
    const store = useCanvasStore.getState();
    const thisNode = store.nodes.find((n) => n.id === id);
    const pos = thisNode
      ? { x: thisNode.position.x + 320, y: thisNode.position.y }
      : { x: 0, y: 0 };
    const newId = store.addNode({
      type: "image",
      position: pos,
      data: {
        nodeType: "image",
        label,
        status: "done",
        mediaType: "image",
        mediaUrl: finalUrl,
        mediaPath: stabilized.mediaPath,
        sourceUrl: stabilized.sourceUrl,
        startTime: Date.now(),
        endTime: Date.now(),
      },
    });
    store.addEdge({ id: `edge_frame_${Date.now()}`, source: id, target: newId, type: "flow" });
  }, [id]);

  const captureFrame = useCallback(() => {
    const v = videoRef.current;
    if (!v) return;
    doCapture(`Frame @${formatTime(v.currentTime)}`);
  }, [doCapture]);

  const captureFirstFrame = useCallback(() => {
    const v = videoRef.current;
    if (!v) return;
    v.currentTime = 0;
    const onSeeked = () => {
      doCapture(t("canvas.firstFrame"));
      v.removeEventListener("seeked", onSeeked);
    };
    v.addEventListener("seeked", onSeeked);
  }, [doCapture, t]);

  const captureLastFrame = useCallback(() => {
    const v = videoRef.current;
    if (!v || !v.duration) return;
    v.currentTime = Math.max(0, v.duration - 0.1);
    const onSeeked = () => {
      doCapture(t("canvas.lastFrame"));
      v.removeEventListener("seeked", onSeeked);
    };
    v.addEventListener("seeked", onSeeked);
  }, [doCapture, t]);

  const baseActions = getMediaActions(d.mediaUrl, d.label, "video");
  const frameActions = d.mediaUrl ? [
    { icon: <SkipBack size={13} />, label: t("canvas.firstFrame"), onClick: captureFirstFrame },
    { icon: <Camera size={13} />, label: t("canvas.captureFrame"), onClick: captureFrame },
    { icon: <SkipForward size={13} />, label: t("canvas.lastFrame"), onClick: captureLastFrame },
  ] : [];
  const editAction = d.mediaUrl
    ? [{ icon: <Pencil size={13} />, label: t("canvas.edit"), onClick: () => setEditMode(!editMode) }]
    : [];
  const allActions = [...baseActions, ...frameActions, ...editAction];

  const handleMedia = useCallback((media: Pick<CanvasNodeData, "mediaUrl" | "mediaPath" | "sourceUrl" | "status">) => {
    updateNode(id, media as Partial<CanvasNodeData>);
  }, [id, updateNode]);

  const handleEdit = useCallback(async () => {
    if (!editPrompt.trim() || !d.mediaUrl) return;

    setEditing(true);
    setEditError("");
    updateNode(id, {
      generating: true,
      status: "running",
      startTime: Date.now(),
      error: undefined,
    } as Partial<CanvasNodeData>);

    const params: Record<string, unknown> = {
      prompt: editPrompt.trim(),
      video_url: d.mediaUrl,
    };
    if (selectedEngine) params.engine = selectedEngine;
    if (editSize) params.size = editSize;

    try {
      const res = await submitAndPollTask("edit_video", params, id);

      if (res.success && res.structured?.media_url) {
        const stabilized = await cacheCanvasMedia(res.structured.media_url, "video");
        const finalMediaUrl = stabilized.mediaUrl || res.structured.media_url;
        const thisNode = getNode(id);
        const newNodeId = addNode({
          type: "video",
          position: {
            x: (thisNode?.position.x || 0) + 350,
            y: thisNode?.position.y || 0,
          },
          data: {
            nodeType: "video" as CanvasNodeType,
            label: editPrompt.trim().slice(0, 30),
            mediaUrl: finalMediaUrl,
            mediaPath: stabilized.mediaPath,
            sourceUrl: stabilized.sourceUrl,
            mediaType: "video",
            status: "done",
          },
        });
        addEdge({ id: `edge_edit_${id}_${newNodeId}`, source: id, target: newNodeId, type: "flow" });
        useHistoryStore.getState().addEntry({
          type: "video",
          prompt: editPrompt.trim(),
          mediaUrl: finalMediaUrl,
          params: { source: "edit_video", video_url: d.mediaUrl, engine: selectedEngine || undefined },
        });
        updateNode(id, { generating: false, status: "done", endTime: Date.now(), error: undefined } as Partial<CanvasNodeData>);
        showCanvasToast("success", t("canvas.videoEdited"));
        autoLayoutCanvasAfterGeneration();
        setEditMode(false);
        setEditPrompt("");
        setEditError("");
      } else {
        const errorMsg = res.output || t("canvas.error");
        updateNode(id, { generating: false, status: "error", error: errorMsg, endTime: Date.now() } as Partial<CanvasNodeData>);
        setEditError(errorMsg);
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      updateNode(id, { generating: false, status: "error", error: msg, endTime: Date.now() } as Partial<CanvasNodeData>);
      setEditError(msg || t("canvas.error"));
      showCanvasToast("error", t("canvas.videoEditFailed"));
    } finally {
      setEditing(false);
    }
  }, [id, editPrompt, selectedEngine, d.mediaUrl, addNode, addEdge, updateNode, t, getNode]);

  const handleDoubleClick = useCallback(() => {
    if (d.mediaUrl) {
      window.dispatchEvent(
        new CustomEvent("canvas-preview", { detail: { url: d.mediaUrl, type: "video", label: d.label } })
      );
    }
  }, [d.mediaUrl, d.label]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, mediaUrl: d.mediaUrl, label: d.label },
      })
    );
  }, [id, d.mediaUrl, d.label]);

  const isGenerating = d.generating === true;
  const hasError = d.status === "error" && d.error;
  const isHighlighted = useCanvasStore((s) => s.highlightedTurnId != null && s.highlightedTurnId === d.turnId);

  return (
    <div
      className={`canvas-node canvas-node-media flex flex-col h-full ${selected ? "selected" : ""} ${isGenerating ? "running" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""}`}
      role="article"
      aria-label={`${d.label || "Video"} — ${d.status || "pending"}`}
      onDoubleClick={handleDoubleClick}
      onContextMenu={handleContextMenu}
    >
      <NodeResizer 
        color="var(--accent)" 
        isVisible={selected} 
        minWidth={160} 
        minHeight={100}
        handleStyle={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--accent)', border: '2px solid white' }}
        lineStyle={{ border: '2px solid var(--accent)', opacity: 0.5 }}
      />
      <NodeToolbar nodeId={id} selected={selected} actions={allActions} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          {d.editOnCreate ? <Pencil size={14} /> : <Video size={14} />}
        </div>
        <span className="canvas-node-label">{d.label || "Video"}</span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>
      <div className="canvas-node-body media flex-1">
        {d.mediaUrl ? (
          isValidMediaUrl(d.mediaUrl) ? (
            <video
              ref={videoRef}
              src={d.mediaUrl}
              className="w-full h-full object-contain"
              controls
              preload="metadata"
              onLoadedMetadata={handleMetadata}
            />
          ) : (
            <div className="canvas-node-media-error" role="alert">
              <strong>Invalid video URL</strong>
              <div title={d.mediaUrl}>{d.mediaUrl.length > 48 ? d.mediaUrl.slice(0, 48) + "…" : d.mediaUrl}</div>
              <small>Likely an unresumed async task — enable waitForCompletion or wire a Resumer.</small>
            </div>
          )
        ) : (
          <MediaDropZone
            nodeId={id}
            kind="video"
            upstreamMediaUrl={upstreamMediaUrl}
            showUrlInput
            maxSize={200 * 1024 * 1024}
            onMedia={handleMedia}
          />
        )}
      </div>
      {info && (
        <div className="canvas-node-footer">
          <span className="canvas-node-media-info">{info.duration}</span>
          <span className="canvas-node-media-info">{info.resolution}</span>
        </div>
      )}

      {/* Edit panel */}
      {editMode && d.mediaUrl && (
        <div className="canvas-node-edit-panel">
          <div className="edit-presets">
            {EDIT_PRESETS.map((p) => (
              <button
                key={p.key}
                className="edit-preset-btn"
                onClick={() => setEditPrompt(p.prompt)}
                disabled={editing}
              >
                {t(`canvas.${p.key}`)}
              </button>
            ))}
          </div>

          <textarea
            className="gen-prompt nowheel nodrag"
            placeholder={t("canvas.editPrompt")}
            value={editPrompt}
            onChange={(e) => setEditPrompt(e.target.value)}
            rows={2}
            disabled={editing}
          />

          <div className="gen-param-group">
            <span className="gen-param-label">{t("canvas.editSize")}</span>
            <div className="gen-select-wrapper">
              <select
                className="gen-select nodrag"
                value={editSize}
                onChange={(e) => setEditSize(e.target.value)}
                disabled={editing}
              >
                <option value="">{t("canvas.editOriginalSize")}</option>
                {VIDEO_SIZE_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>{o.label}</option>
                ))}
              </select>
              <ChevronDown size={12} className="gen-select-icon" />
            </div>
          </div>

          {editSchema && <EngineSelector engines={editSchema.engines} value={selectedEngine} onChange={setSelectedEngine} disabled={editing} />}

          <button className="gen-btn" onClick={handleEdit} disabled={editing || !editPrompt.trim()}>
            {editing ? (
              <><Loader2 size={14} className="animate-spin" /><span>{t("canvas.editing")}</span></>
            ) : (
              <><Sparkles size={14} /><span>{t("canvas.edit")}</span></>
            )}
          </button>
          <GenTimer generating={isGenerating} startTime={d.startTime} endTime={d.endTime} />
        </div>
      )}

      {hasError && <GenErrorBar error={d.error} onRetry={handleEdit} />}

      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
