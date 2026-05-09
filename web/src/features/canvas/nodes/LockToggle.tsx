import { Lock, Unlock } from "lucide-react";
import type { MouseEvent } from "react";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";
import type { CanvasNodeData } from "../types";

interface LockToggleProps {
  nodeId: string;
  locked?: boolean;
}

/** Small lock/unlock button intended for a canvas-node-header slot.
 *  Stops propagation so clicking it doesn't toggle the header's collapse. */
export function LockToggle({ nodeId, locked }: LockToggleProps) {
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);

  const handleClick = (e: MouseEvent) => {
    e.stopPropagation();
    updateNode(nodeId, { locked: !locked } as Partial<CanvasNodeData>);
  };

  return (
    <button
      type="button"
      className={`canvas-node-lock-btn ${locked ? "locked" : ""}`}
      onClick={handleClick}
      title={locked ? t("canvas.unlockNode" as any) : t("canvas.lockNode" as any)}
      aria-label={locked ? t("canvas.unlockNode" as any) : t("canvas.lockNode" as any)}
    >
      {locked ? <Lock size={12} /> : <Unlock size={12} />}
    </button>
  );
}
