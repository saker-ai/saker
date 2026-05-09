import { memo, useCallback } from "react";
import { type NodeProps } from "@xyflow/react";
import { Group, ChevronDown, ChevronRight, Ungroup } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar } from "./NodeToolbar";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";

export const GroupNode = memo(function GroupNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const collapsed = d.collapsed === true;
  const childCount = useCanvasStore((s) => s.nodes.filter((n) => n.parentId === id).length);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, label: d.label },
      })
    );
  }, [id, d.label]);

  const toggleCollapse = (e: React.MouseEvent) => {
    e.stopPropagation();
    const store = useCanvasStore.getState();
    if (collapsed) {
      store.expandGroup(id);
    } else {
      store.collapseGroup(id);
    }
  };

  const handleUngroup = (e: React.MouseEvent) => {
    e.stopPropagation();
    useCanvasStore.getState().ungroupNodes(id);
  };

  return (
    <div
      className={`canvas-node canvas-group-node ${selected ? "selected" : ""} ${collapsed ? "collapsed" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
      aria-label={d.label || "Group"}
    >
      <NodeToolbar nodeId={id} selected={selected} />
      <div className="canvas-group-header" onClick={toggleCollapse}>
        <button className="canvas-group-toggle">
          {collapsed ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
        </button>
        <Group size={14} />
        <span className="canvas-node-label">{d.label || t("canvas.group")}</span>
        <span className="canvas-group-badge">{childCount}</span>
        <button
          className="canvas-ungroup-btn"
          title={t("canvas.ungroup")}
          onClick={handleUngroup}
        >
          <Ungroup size={12} />
        </button>
      </div>
      {!collapsed && (
        <div className="canvas-group-body" />
      )}
    </div>
  );
});
