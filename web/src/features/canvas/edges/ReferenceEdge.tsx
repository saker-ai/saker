import { useState } from "react";
import {
  BaseEdge,
  EdgeLabelRenderer,
  getSmoothStepPath,
  type EdgeProps,
} from "@xyflow/react";
import { X } from "lucide-react";
import { useCanvasStore } from "../store";

/**
 * Dashed edge indicating a media reference relationship
 * (e.g., generate_video referencing an upstream image).
 */
export function ReferenceEdge(props: EdgeProps) {
  const {
    id,
    selected,
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    style = {},
  } = props;

  const removeEdges = useCanvasStore((s) => s.removeEdges);
  const [hovered, setHovered] = useState(false);

  const [edgePath, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    borderRadius: 8,
  });

  const color = "#f59e0b";

  return (
    <>
      {/* 1. Interaction Buffer Layer: Makes it easier to select thin lines */}
      <path
        d={edgePath}
        fill="none"
        stroke="transparent"
        strokeWidth={20}
        style={{ cursor: "pointer", pointerEvents: "stroke" }}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
      />

      {/* 2. Glow Layer: Visible when selected */}
      {selected && (
        <BaseEdge
          id={`${id}-glow`}
          path={edgePath}
          style={{
            stroke: color,
            strokeWidth: 8,
            opacity: 0.3,
            filter: "blur(4px)",
          }}
        />
      )}

      {/* 3. Main Visible Line */}
      <BaseEdge
        id={id}
        path={edgePath}
        style={{
          ...style,
          stroke: color,
          strokeWidth: selected ? 2.5 : 1.5,
          strokeDasharray: "6 4",
          opacity: selected ? 1 : 0.6,
          transition: "stroke-width 0.3s, opacity 0.3s",
        }}
      />

      {/* 4. Interactive Label Layer: Delete button (selected/hover) or Type label */}
      <EdgeLabelRenderer>
        <div
          className={`flow-edge-label-container ${selected ? "selected" : ""} ${hovered ? "hovered" : ""}`}
          style={{
            position: "absolute",
            transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
            pointerEvents: "all",
          }}
          onMouseEnter={() => setHovered(true)}
          onMouseLeave={() => setHovered(false)}
        >
          {selected || hovered ? (
            <button
              className="flow-edge-delete-btn"
              title="Remove edge"
              onClick={(e) => {
                e.stopPropagation();
                removeEdges([id]);
              }}
            >
              <X size={10} />
            </button>
          ) : (
            <div
              className="flow-edge-label"
              style={{
                background: color,
                pointerEvents: "none",
              }}
            >
              REF
            </div>
          )}
        </div>
      </EdgeLabelRenderer>
    </>
  );
}
