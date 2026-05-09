import { memo, useState, useCallback, useEffect, useMemo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { LogOut } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";
import { ToolbarDropdown } from "./ToolbarDropdown";

const OUTPUT_KINDS = ["text", "image", "video", "audio"] as const;
type OutputKind = (typeof OUTPUT_KINDS)[number];

export const AppOutputNode = memo(function AppOutputNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const edges = useCanvasStore((s) => s.edges);

  const [outputKind, setOutputKind] = useState<OutputKind>((d.appOutputKind as OutputKind) ?? "text");

  useEffect(() => {
    updateNode(id, { appOutputKind: outputKind } as Partial<CanvasNodeData>);
  }, [outputKind, id, updateNode]);

  const upstreamNodeId = useMemo(() => {
    const inbound = edges.find((e) => e.target === id);
    return inbound?.source ?? null;
  }, [edges, id]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, label: d.label },
      })
    );
  }, [id, d.label]);

  const kindOptions = OUTPUT_KINDS.map((k) => ({
    value: k,
    label: t(`canvas.appOutputKind.${k}` as any) || k,
  }));

  return (
    <div
      className={`canvas-node canvas-node-app-output ${selected ? "selected" : ""}`}
      role="article"
      aria-label={d.label || t("canvas.appOutputLabel")}
      onContextMenu={handleContextMenu}
    >
      <NodeToolbar nodeId={id} selected={selected} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          <LogOut size={14} />
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.appOutputLabel")}</span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>

      <div className="canvas-node-body nowheel">
        <div className="gen-toolbar nodrag" style={{ flexDirection: "column", gap: 6 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
            <label style={{ fontSize: 11, opacity: 0.6, minWidth: 54 }}>
              {t("canvas.appOutputKind")}
            </label>
            <ToolbarDropdown
              options={kindOptions}
              value={outputKind}
              onChange={(v) => setOutputKind(v as OutputKind)}
            />
          </div>

          <div style={{ fontSize: 11, opacity: 0.5, paddingTop: 2 }}>
            {upstreamNodeId
              ? `← ${upstreamNodeId}`
              : t("canvas.appOutputNoUpstream")}
          </div>
        </div>
      </div>

      <Handle type="target" position={Position.Left} className="canvas-handle" />
    </div>
  );
});
