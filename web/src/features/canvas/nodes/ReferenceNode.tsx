import { memo, useState, useCallback, useEffect, useMemo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Link2 } from "lucide-react";
import type { CanvasNodeData, RefType } from "../types";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";
import { NodeToolbar } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { ToolbarDropdown } from "./ToolbarDropdown";

const REF_TYPES: RefType[] = ["style", "character", "composition", "pose"];

export const ReferenceNode = memo(function ReferenceNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const [refType, setRefType] = useState<RefType>((d.refType as RefType) || "style");
  const [strength, setStrength] = useState<number>(typeof d.refStrength === "number" ? d.refStrength : 1);

  const edges = useCanvasStore((s) => s.edges);
  const nodes = useCanvasStore((s) => s.nodes);
  const upstream = useMemo(() => {
    const upstreamIds = edges.filter((e) => e.target === id).map((e) => e.source);
    for (const sid of upstreamIds) {
      const n = nodes.find((x) => x.id === sid);
      const nd = n?.data as CanvasNodeData | undefined;
      if (nd && typeof nd.mediaUrl === "string" && nd.mediaUrl) {
        return { url: nd.mediaUrl, type: typeof nd.mediaType === "string" ? nd.mediaType : "image" };
      }
    }
    return { url: undefined as string | undefined, type: undefined as string | undefined };
  }, [id, edges, nodes]);
  const previewUrl = d.mediaUrl || upstream.url;

  useEffect(() => {
    updateNode(id, {
      refType,
      refStrength: strength,
      mediaUrl: d.mediaUrl || upstream.url || "",
      mediaType: (d.mediaType as any) || upstream.type || "image",
    } as Partial<CanvasNodeData>);
  }, [refType, strength, id, updateNode, d.mediaUrl, d.mediaType, upstream.url, upstream.type]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, mediaUrl: previewUrl, label: d.label },
      })
    );
  }, [id, previewUrl, d.label]);

  const refTypeOptions = useMemo(
    () => REF_TYPES.map((r) => ({ value: r, label: t(`canvas.refType.${r}` as any) || r })),
    [t],
  );

  return (
    <div
      className={`canvas-node canvas-node-reference ${selected ? "selected" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
      aria-label={d.label || "Reference"}
    >
      <NodeToolbar nodeId={id} selected={selected} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          <Link2 size={14} />
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.reference" as any) || "Reference"}</span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>

      <div className="canvas-node-body reference-body">
        {previewUrl ? (
          <div className="reference-preview">
            {(d.mediaType || upstream.type) === "video" ? (
              <video src={previewUrl} muted loop playsInline className="reference-preview-media" />
            ) : (
              <img src={previewUrl} alt="" className="reference-preview-media" draggable={false} />
            )}
          </div>
        ) : (
          <div className="reference-preview empty">{t("canvas.referenceEmpty" as any) || "Connect a media node above"}</div>
        )}

        <div className="reference-toolbar nodrag">
          <ToolbarDropdown
            options={refTypeOptions}
            value={refType}
            onChange={(v) => setRefType(v as RefType)}
          />
          <label className="reference-strength-label">
            {t("canvas.refStrength" as any) || "Strength"}
            <input
              type="range"
              min={0}
              max={1}
              step={0.05}
              value={strength}
              onChange={(e) => setStrength(Number(e.target.value))}
              className="reference-strength-slider nodrag"
            />
            <span className="reference-strength-val">{strength.toFixed(2)}</span>
          </label>
        </div>
      </div>

      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
