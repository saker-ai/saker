import { useMemo } from "react";
import { Trash2, RotateCcw, Group, ArrowLeftToLine, ArrowRightToLine, ArrowUpToLine, ArrowDownToLine, AlignCenterVertical, AlignCenterHorizontal, Maximize } from "lucide-react";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";
import { useShallow } from "zustand/react/shallow";
import { useReactFlow } from "@xyflow/react";

export function BulkToolbar() {
  const { t } = useT();
  const { fitBounds } = useReactFlow();
  
  // Only subscribe to the subset of nodes that are selected, with shallow comparison
  const selected = useCanvasStore(useShallow((s) => s.nodes.filter((n) => n.selected)));
  
  const removeNodes = useCanvasStore((s) => s.removeNodes);
  const groupNodes = useCanvasStore((s) => s.groupNodes);
  const alignNodes = useCanvasStore((s) => s.alignNodes);

  if (selected.length < 2) return null;

  const genNodes = selected.filter(
    (n) => n.type === "imageGen" || n.type === "videoGen" || n.type === "voiceGen"
  );
  const hasRetryable = genNodes.some((n) => n.data.status === "error");

  const handleBulkDelete = () => {
    useCanvasStore.getState().commitHistory();
    removeNodes(selected.map((n) => n.id));
  };

  const handleBulkRetry = () => {
    const retryNode = useCanvasStore.getState().retryNode;
    genNodes
      .filter((n) => n.data.status === "error")
      .forEach((n) => retryNode(n.id));
  };

  const handleGroup = () => {
    groupNodes(selected.map((n) => n.id));
  };

  const handleFitSelection = () => {
    if (selected.length === 0) return;
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const n of selected) {
      const w = n.measured?.width || 280;
      const h = n.measured?.height || 160;
      minX = Math.min(minX, n.position.x);
      minY = Math.min(minY, n.position.y);
      maxX = Math.max(maxX, n.position.x + w);
      maxY = Math.max(maxY, n.position.y + h);
    }
    fitBounds({ x: minX, y: minY, width: maxX - minX, height: maxY - minY }, { padding: 0.2, duration: 400 });
  };

  return (
    <div className="canvas-bulk-toolbar">
      <div className="canvas-bulk-group">
        <span className="canvas-bulk-count">{selected.length} {t("canvas.selected")}</span>
        <button className="canvas-bulk-btn" onClick={handleFitSelection} title={t("canvas.zoomToSelection")}>
          <Maximize size={14} />
        </button>
      </div>

      <div className="canvas-bulk-divider" />

      <div className="canvas-bulk-group">
        <button className="canvas-bulk-btn" onClick={() => alignNodes(selected.map(n=>n.id), "left")} title={t("canvas.alignLeft")}>
          <ArrowLeftToLine size={14} />
        </button>
        <button className="canvas-bulk-btn" onClick={() => alignNodes(selected.map(n=>n.id), "center-h")} title={t("canvas.alignCenterH")}>
          <AlignCenterHorizontal size={14} />
        </button>
        <button className="canvas-bulk-btn" onClick={() => alignNodes(selected.map(n=>n.id), "right")} title={t("canvas.alignRight")}>
          <ArrowRightToLine size={14} />
        </button>
        <div className="canvas-bulk-divider mini" />
        <button className="canvas-bulk-btn" onClick={() => alignNodes(selected.map(n=>n.id), "top")} title={t("canvas.alignTop")}>
          <ArrowUpToLine size={14} />
        </button>
        <button className="canvas-bulk-btn" onClick={() => alignNodes(selected.map(n=>n.id), "center-v")} title={t("canvas.alignCenterV")}>
          <AlignCenterVertical size={14} />
        </button>
        <button className="canvas-bulk-btn" onClick={() => alignNodes(selected.map(n=>n.id), "bottom")} title={t("canvas.alignBottom")}>
          <ArrowDownToLine size={14} />
        </button>
      </div>

      <div className="canvas-bulk-divider" />

      <div className="canvas-bulk-group">
        <button className="canvas-bulk-btn" onClick={handleGroup} title={t("canvas.groupSelected")}>
          <Group size={14} />
        </button>
        {hasRetryable && (
          <button className="canvas-bulk-btn" onClick={handleBulkRetry} title={t("canvas.retry")}>
            <RotateCcw size={14} />
          </button>
        )}
        <button className="canvas-bulk-btn canvas-bulk-btn-danger" onClick={handleBulkDelete} title={t("canvas.delete")}>
          <Trash2 size={14} />
        </button>
      </div>
    </div>
  );
}
