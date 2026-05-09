import { memo } from "react";
import type { GenHistoryEntry } from "../types";
import { useT } from "@/features/i18n";

interface GenHistoryStripProps {
  nodeId: string;
  history?: GenHistoryEntry[];
  activeIndex?: number;
}

const MAX_VISIBLE = 5;

/** Footer strip on gen nodes: ≤5 dots representing past generations; click focuses the result node. */
export const GenHistoryStrip = memo(function GenHistoryStrip({ nodeId, history, activeIndex }: GenHistoryStripProps) {
  const { t } = useT();
  const entries = history ?? [];
  if (entries.length === 0) return null;

  const visible = entries.slice(0, MAX_VISIBLE);
  const overflow = Math.max(0, entries.length - MAX_VISIBLE);
  const effectiveActive = typeof activeIndex === "number" && activeIndex >= 0 && activeIndex < entries.length ? activeIndex : 0;

  const handleClick = (entry: GenHistoryEntry, index: number) => {
    const firstResult = entry.resultNodeIds?.[0];
    if (firstResult) {
      window.dispatchEvent(
        new CustomEvent("canvas-node-focus", { detail: { nodeId: firstResult } })
      );
    }
    window.dispatchEvent(
      new CustomEvent("canvas-gen-history-select", { detail: { nodeId, index } })
    );
  };

  return (
    <div className="gen-history-strip nodrag" role="group" aria-label={t("canvas.genHistory" as any)}>
      {visible.map((entry, i) => {
        const cls = [
          "gen-history-dot",
          entry.status === "error" ? "error" : "done",
          i === effectiveActive ? "active" : "",
        ].filter(Boolean).join(" ");
        const label = entry.prompt ? entry.prompt.slice(0, 80) : `#${i + 1}`;
        return (
          <button
            key={entry.id}
            type="button"
            className={cls}
            title={label}
            aria-label={label}
            onClick={(e) => { e.stopPropagation(); handleClick(entry, i); }}
          />
        );
      })}
      {overflow > 0 && (
        <span className="gen-history-overflow" title={t("canvas.genHistoryMore" as any)}>
          +{overflow}
        </span>
      )}
    </div>
  );
});
