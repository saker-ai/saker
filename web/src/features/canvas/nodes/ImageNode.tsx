import { useState, useCallback, useEffect, memo, useMemo } from "react";
import { Handle, Position, type NodeProps, useNodeConnections, useReactFlow, NodeResizer } from "@xyflow/react";
import { Image, Pencil, Loader2, Sparkles } from "lucide-react";
import type { CanvasNode, CanvasNodeData, CanvasNodeType } from "../types";
import { NodeToolbar, getMediaActions } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useCanvasStore } from "../store";
import { useHistoryStore } from "../panels/historyStore";
import { useT } from "@/features/i18n";
import { cacheCanvasMedia } from "../mediaCache";
import { submitAndPollTask } from "../taskPoller";
import { GenTimer } from "./GenTimer";
import { ToolbarDropdown } from "./ToolbarDropdown";
import { GenErrorBar } from "./GenErrorBar";
import { autoLayoutCanvasAfterGeneration } from "../layoutActions";
import { showCanvasToast } from "../panels/CanvasToast";
import { MediaDropZone } from "./MediaDropZone";
import { isValidMediaUrl } from "../mediaUrl";
import { useToolSchema } from "./useToolSchema";

const EDIT_PRESETS = [
  { key: "editBg" as const, prompt: "Change the background to " },
  { key: "editStyle" as const, prompt: "Transform the style to " },
  { key: "editRemoveText" as const, prompt: "Remove all text and watermarks from the image" },
  { key: "editEnhance" as const, prompt: "Enhance the image quality, make it sharper and more vivid" },
];

const EDIT_SIZE_OPTIONS = ["", "1024x1024", "1024x1536", "1536x1024"] as const;

export const ImageNode = memo(function ImageNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const addNode = useCanvasStore((s) => s.addNode);
  const addEdge = useCanvasStore((s) => s.addEdge);
  const { getNode } = useReactFlow();

  const [editMode, setEditMode] = useState(d.editOnCreate === true);
  const [editPrompt, setEditPrompt] = useState("");
  const [editSize, setEditSize] = useState("");
  const [editing, setEditing] = useState(false);
  const [editError, setEditError] = useState("");
  const { schema: editSchema, defaultEngine } = useToolSchema("edit_image");
  const [selectedEngine, setSelectedEngine] = useState(d.engine || "");

  // Detect upstream image from connections
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
    if (srcData.nodeType === "image" && srcData.mediaUrl) return srcData.mediaUrl;
    if (srcData.nodeType === "tool" && srcData.mediaType === "image" && srcData.mediaUrl) return srcData.mediaUrl;
    return null;
  }, [d.mediaUrl, sourceNodeData]);

  // Auto-adopt upstream image
  useEffect(() => {
    if (upstreamMediaUrl && !d.mediaUrl) {
      updateNode(id, { mediaUrl: upstreamMediaUrl } as Partial<CanvasNodeData>);
    }
  }, [upstreamMediaUrl, d.mediaUrl, id, updateNode]);

  // Auto-set default engine from schema
  useEffect(() => {
    if (defaultEngine && !selectedEngine) setSelectedEngine(defaultEngine);
  }, [defaultEngine, selectedEngine]);

  const baseActions = getMediaActions(d.mediaUrl, d.label);
  const editAction = d.mediaUrl
    ? [{ icon: <Pencil size={13} />, label: t("canvas.edit"), onClick: () => setEditMode(!editMode) }]
    : [];
  const actions = [...baseActions, ...editAction];

  const handleMedia = useCallback((media: Pick<CanvasNodeData, "mediaUrl" | "mediaPath" | "sourceUrl" | "status">) => {
    updateNode(id, media as Partial<CanvasNodeData>);
  }, [id, updateNode]);

  const handleInstantPreview = useCallback((blobUrl: string) => {
    updateNode(id, { mediaUrl: blobUrl, status: "done" } as Partial<CanvasNodeData>);
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
      image_url: d.mediaUrl,
    };
    if (editSize) params.size = editSize;
    if (selectedEngine) params.engine = selectedEngine;

    try {
      const res = await submitAndPollTask("edit_image", params, id);

      if (res.success && res.structured?.media_url) {
        const stabilizedMedia = await cacheCanvasMedia(res.structured.media_url, "image");
        const finalMediaUrl = stabilizedMedia.mediaUrl || res.structured.media_url;
        const thisNode = getNode(id);
        const newNodeId = addNode({
          type: "image",
          position: {
            x: (thisNode?.position.x || 0) + 350,
            y: thisNode?.position.y || 0,
          },
          data: {
            nodeType: "image" as CanvasNodeType,
            label: editPrompt.trim().slice(0, 30),
            mediaUrl: finalMediaUrl,
            mediaPath: stabilizedMedia.mediaPath,
            sourceUrl: stabilizedMedia.sourceUrl,
            mediaType: "image",
            status: "done",
          },
        });
        addEdge({ id: `edge_edit_${id}_${newNodeId}`, source: id, target: newNodeId, type: "flow" });
        useHistoryStore.getState().addEntry({
          type: "image",
          prompt: editPrompt.trim(),
          mediaUrl: finalMediaUrl,
          params: { source: "edit_image", image_url: d.mediaUrl, size: editSize || undefined, engine: selectedEngine || undefined },
        });
        updateNode(id, { generating: false, status: "done", endTime: Date.now(), error: undefined } as Partial<CanvasNodeData>);
        showCanvasToast("success", t("canvas.imageEdited"));
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
      showCanvasToast("error", t("canvas.imageEditFailed"));
    } finally {
      setEditing(false);
    }
  }, [id, editPrompt, editSize, selectedEngine, d.mediaUrl, addNode, addEdge, updateNode, t, getNode]);

  const handleDoubleClick = useCallback(() => {
    if (d.mediaUrl) {
      window.dispatchEvent(
        new CustomEvent("canvas-preview", { detail: { url: d.mediaUrl, type: "image", label: d.label } })
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
      className={`canvas-node canvas-node-media ${selected ? "selected" : ""} ${isGenerating ? "running" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""}`}
      role="article"
      aria-label={`${d.label || "Image"} — ${d.status || "pending"}`}
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
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          {d.editOnCreate ? <Pencil size={14} /> : <Image size={14} />}
        </div>
        <span className="canvas-node-label">{d.label || "Image"}</span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>
      <div className="canvas-node-body media">
        {d.mediaUrl ? (
          isValidMediaUrl(d.mediaUrl) ? (
            <img src={d.mediaUrl} alt={d.label} loading="lazy" />
          ) : (
            <div className="canvas-node-media-error" role="alert">
              <strong>Invalid image URL</strong>
              <div title={d.mediaUrl}>{d.mediaUrl.length > 48 ? d.mediaUrl.slice(0, 48) + "…" : d.mediaUrl}</div>
              <small>Likely an unresumed async task — enable waitForCompletion or wire a Resumer.</small>
            </div>
          )
        ) : (
          <MediaDropZone
            nodeId={id}
            kind="image"
            upstreamMediaUrl={upstreamMediaUrl}
            showUrlInput
            onMedia={handleMedia}
            onInstantPreview={handleInstantPreview}
          />
        )}
      </div>

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

          <div className="gen-toolbar nodrag">
            {editSchema && editSchema.engines.length > 0 && (
              <ToolbarDropdown
                icon={<Sparkles size={12} />}
                options={editSchema.engines.map((e) => ({ value: e, label: e }))}
                value={selectedEngine}
                onChange={setSelectedEngine}
                disabled={editing}
              />
            )}

            <span className="gen-toolbar-sep">·</span>

            <ToolbarDropdown
              options={EDIT_SIZE_OPTIONS.map((s) => ({ value: s, label: s || t("canvas.editOriginalSize") }))}
              value={editSize}
              onChange={setEditSize}
              disabled={editing}
            />

            <div style={{ flex: 1 }} />

            <button className="gen-toolbar-submit" onClick={handleEdit} disabled={editing || !editPrompt.trim()} title={t("canvas.edit")}>
              {editing ? <Loader2 size={16} className="animate-spin" /> : <Sparkles size={16} />}
            </button>
          </div>
          <GenTimer generating={isGenerating} startTime={d.startTime} endTime={d.endTime} />
        </div>
      )}

      {hasError && <GenErrorBar error={d.error} onRetry={handleEdit} />}

      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
