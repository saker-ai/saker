import type { CanvasNodeData } from "../types";

interface CollapsedSummaryProps {
  data: CanvasNodeData;
  nodeKind: "image" | "video" | "voice" | "text";
}

/** Compact chip row shown in collapsed gen-node headers summarizing the active config. */
export function CollapsedSummary({ data, nodeKind }: CollapsedSummaryProps) {
  const chips: string[] = [];
  if (nodeKind === "image") {
    if (data.aspectRatio) chips.push(data.aspectRatio);
    if (data.resolution) chips.push(data.resolution);
    if (data.engine) chips.push(data.engine);
  } else if (nodeKind === "video") {
    if (data.resolution) chips.push(data.resolution);
    if (data.duration) chips.push(`${data.duration}s`);
    if (data.engine) chips.push(data.engine);
  } else if (nodeKind === "voice") {
    if (data.voice) chips.push(data.voice);
    if (data.language) chips.push(data.language);
    if (data.engine) chips.push(data.engine);
  } else if (nodeKind === "text") {
    if (data.llmMode) chips.push(data.llmMode);
    if (data.engine) chips.push(data.engine);
  }
  if (data.genCount && data.genCount > 1) chips.push(`×${data.genCount}`);
  if (chips.length === 0) return null;
  return (
    <span className="gen-collapsed-summary" aria-hidden>
      {chips.map((chip, i) => (
        <span key={i} className="gen-collapsed-chip">{chip}</span>
      ))}
    </span>
  );
}
