import { memo, useCallback } from "react";
import { Handle, Position, type NodeProps, useReactFlow } from "@xyflow/react";
import { Music, Download } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";
import { MediaDropZone } from "./MediaDropZone";
import { isValidMediaUrl } from "../mediaUrl";

export const AudioNode = memo(function AudioNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const { getNode } = useReactFlow();

  const actions = d.mediaUrl
    ? [
        {
          icon: <Download size={13} />,
          label: "Download",
          onClick: () => {
            const a = document.createElement("a");
            a.href = d.mediaUrl!;
            a.download = d.label || "audio";
            a.click();
          },
        },
      ]
    : [];

  const handleMedia = useCallback((media: Pick<CanvasNodeData, "mediaUrl" | "mediaPath" | "sourceUrl" | "status">) => {
    updateNode(id, media as Partial<CanvasNodeData>);
  }, [id, updateNode]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, mediaUrl: d.mediaUrl, label: d.label },
      })
    );
  }, [id, d.mediaUrl, d.label]);

  const isHighlighted = useCanvasStore((s) => s.highlightedTurnId != null && s.highlightedTurnId === d.turnId);

  return (
    <div
      className={`canvas-node canvas-node-media ${selected ? "selected" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""}`}
      role="article"
      aria-label={`${d.label || "Audio"} — ${d.status || "pending"}`}
      onContextMenu={handleContextMenu}
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          <Music size={14} />
        </div>
        <span className="canvas-node-label">{d.label || "Audio"}</span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>
      <div className="canvas-node-body media">
        {d.mediaUrl ? (
          isValidMediaUrl(d.mediaUrl) ? (
            <audio src={d.mediaUrl} controls preload="metadata" style={{ width: "100%", height: "32px" }} />
          ) : (
            <div className="canvas-node-media-error" role="alert">
              <strong>Invalid audio URL</strong>
              <div title={d.mediaUrl}>{d.mediaUrl.length > 48 ? d.mediaUrl.slice(0, 48) + "…" : d.mediaUrl}</div>
              <small>Likely an unresumed async task — enable waitForCompletion or wire a Resumer.</small>
            </div>
          )
        ) : (
          <MediaDropZone
            nodeId={id}
            kind="audio"
            maxSize={50 * 1024 * 1024}
            onMedia={handleMedia}
          />
        )}
      </div>
      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
