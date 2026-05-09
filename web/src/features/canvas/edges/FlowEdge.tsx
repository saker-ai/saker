import { useState } from "react";
import {
  BaseEdge,
  EdgeLabelRenderer,
  getSmoothStepPath,
  type EdgeProps,
  useStore,
  type ReactFlowState,
} from "@xyflow/react";
import { X } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { useCanvasStore } from "../store";

/** Color mapping by source node type */
const TYPE_COLORS: Record<string, string> = {
  image: "#06b6d4",       // cyan — image data
  video: "#8b5cf6",       // purple — video data
  audio: "#f59e0b",       // amber — audio data
  prompt: "#10b981",      // green — prompt/text flow
  text: "#94a3b8",        // gray — text
  agent: "#10b981",       // green — agent flow
  skill: "#3b82f6",       // blue — skill
  tool: "#14b8a6",        // teal — tool
  composition: "#10b981",
  imageGen: "#06b6d4",    // cyan — image generation
  voiceGen: "#f59e0b",    // amber — voice generation
  videoGen: "#8b5cf6",    // purple — video generation
  group: "#94a3b8",
};

/** Short label for edge data type */
const TYPE_LABELS: Record<string, string> = {
  image: "IMG",
  video: "VID",
  audio: "AUD",
  prompt: "TXT",
  text: "TXT",
  imageGen: "GEN",
  voiceGen: "GEN",
  videoGen: "GEN",
};

/**
 * FlowEdge component representing a data/control flow between nodes.
 * Features an interactive layer, selection glow, and status-based animations.
 */
export function FlowEdge(props: EdgeProps) {
  const { 
    id, 
    selected, 
    sourceX, 
    sourceY, 
    targetX, 
    targetY, 
    sourcePosition, 
    targetPosition,
    style = {}
  } = props;
  
  const removeEdges = useCanvasStore((s) => s.removeEdges);
  const [hovered, setHovered] = useState(false);

  // Targeted selector: only re-render when source node info changes.
  const sourceInfo = useStore((s: ReactFlowState) => {
    const node = s.nodeLookup?.get(props.source);
    if (!node) return null;
    const data = node.data as CanvasNodeData | undefined;
    return { type: node.type || "", status: data?.status };
  });

  const sourceType = sourceInfo?.type || "";
  const isRunning = sourceInfo?.status === "running";
  const isDone = sourceInfo?.status === "done";
  const color = TYPE_COLORS[sourceType] || "#94a3b8";
  const label = TYPE_LABELS[sourceType];

  const [edgePath, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    borderRadius: 8,
  });

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
      
      {/* 2. Glow Layer: Visible when selected or source node is running */}
      {(selected || isRunning) && (
        <BaseEdge
          id={`${id}-glow`}
          path={edgePath}
          style={{
            stroke: selected ? "var(--accent, #3b82f6)" : color,
            strokeWidth: selected ? 8 : 6,
            opacity: selected ? 0.3 : 0.15,
            filter: "blur(4px)",
          }}
        />
      )}

      {/* 3. Main Visible Line: Pulse animation when running */}
      <BaseEdge
        id={id}
        path={edgePath}
        className={isRunning ? "flow-edge-running" : isDone ? "flow-edge-done" : ""}
        style={{
          ...style,
          stroke: selected ? "var(--accent, #3b82f6)" : color,
          strokeWidth: selected ? 3 : 2,
          strokeDasharray: isRunning ? "8 4" : "none",
          animation: isRunning ? "canvas-flow-pulse 1.2s linear infinite" : "none",
          opacity: selected ? 1 : isRunning ? 1 : isDone ? 0.8 : 0.6,
          transition: "stroke 0.3s, stroke-width 0.3s, opacity 0.3s",
        }}
      />
      
      {/* 4. Interactive Label Layer: Delete button (selected/hover) or Type label */}
      {(selected || hovered || label) && (
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
                className={`flow-edge-label ${isRunning ? "active" : ""}`}
                style={{
                  background: color,
                  pointerEvents: "none",
                }}
              >
                {label}
              </div>
            )}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}
