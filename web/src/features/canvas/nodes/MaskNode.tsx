import { useState, useCallback, useEffect, useRef, memo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Brush, Eraser, Trash2, Check, Layers } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar, getMediaActions } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";
import { cacheCanvasMedia } from "../mediaCache";
import { showCanvasToast } from "../panels/CanvasToast";

interface Stroke {
  tool: "pen" | "eraser";
  points: Array<[number, number]>;
  width: number;
}

const CANVAS_W = 400;
const CANVAS_H = 300;
const WIDTHS = [10, 20, 40];

function drawStroke(ctx: CanvasRenderingContext2D, s: Stroke) {
  if (s.points.length === 0) return;
  ctx.save();
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.lineWidth = s.width;
  if (s.tool === "eraser") {
    ctx.globalCompositeOperation = "destination-out";
    ctx.strokeStyle = "rgba(0,0,0,1)";
  } else {
    ctx.globalCompositeOperation = "source-over";
    ctx.strokeStyle = "#ffffff";
  }
  ctx.beginPath();
  const [x0, y0] = s.points[0];
  ctx.moveTo(x0, y0);
  if (s.points.length === 1) {
    ctx.lineTo(x0 + 0.1, y0);
  } else {
    for (let i = 1; i < s.points.length; i++) {
      const [x, y] = s.points[i];
      ctx.lineTo(x, y);
    }
  }
  ctx.stroke();
  ctx.restore();
}

function findUpstreamImage(nodeId: string): string | undefined {
  const { nodes, edges } = useCanvasStore.getState();
  const upstreamIds = edges.filter((e) => e.target === nodeId).map((e) => e.source);
  for (const sid of upstreamIds) {
    const n = nodes.find((x) => x.id === sid);
    if (!n) continue;
    const d = n.data as CanvasNodeData;
    if (d.nodeType === "image" && d.mediaUrl) return d.mediaUrl;
  }
  return undefined;
}

export const MaskNode = memo(function MaskNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const [strokes, setStrokes] = useState<Stroke[]>(() => {
    try {
      return d.maskData ? (JSON.parse(d.maskData) as Stroke[]) : [];
    } catch {
      return [];
    }
  });
  const [tool, setTool] = useState<"pen" | "eraser">("pen");
  const [width, setWidth] = useState<number>(20);
  const [drawing, setDrawing] = useState(false);
  const [currentStroke, setCurrentStroke] = useState<Stroke | null>(null);
  const actions = getMediaActions(d.mediaUrl, d.label, "image");

  const bgUrl = d.sketchBgImage || findUpstreamImage(id);

  // Persist strokes
  useEffect(() => {
    updateNode(id, { maskData: JSON.stringify(strokes) } as Partial<CanvasNodeData>);
  }, [strokes, id, updateNode]);

  // Redraw
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    ctx.clearRect(0, 0, CANVAS_W, CANVAS_H);
    for (const s of strokes) drawStroke(ctx, s);
    if (currentStroke) drawStroke(ctx, currentStroke);
  }, [strokes, currentStroke]);

  const getPoint = useCallback((e: React.MouseEvent<HTMLCanvasElement>): [number, number] => {
    const rect = e.currentTarget.getBoundingClientRect();
    const scaleX = CANVAS_W / rect.width;
    const scaleY = CANVAS_H / rect.height;
    return [(e.clientX - rect.left) * scaleX, (e.clientY - rect.top) * scaleY];
  }, []);

  const onMouseDown = useCallback((e: React.MouseEvent<HTMLCanvasElement>) => {
    e.stopPropagation();
    setDrawing(true);
    setCurrentStroke({ tool, width, points: [getPoint(e)] });
  }, [tool, width, getPoint]);

  const onMouseMove = useCallback((e: React.MouseEvent<HTMLCanvasElement>) => {
    if (!drawing || !currentStroke) return;
    setCurrentStroke({ ...currentStroke, points: [...currentStroke.points, getPoint(e)] });
  }, [drawing, currentStroke, getPoint]);

  const onMouseUp = useCallback(() => {
    if (drawing && currentStroke) {
      setStrokes((prev) => [...prev, currentStroke]);
    }
    setDrawing(false);
    setCurrentStroke(null);
  }, [drawing, currentStroke]);

  const handleClear = useCallback(() => {
    setStrokes([]);
  }, []);

  const handleExport = useCallback(async () => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    // Create a full-opacity binarized PNG: white strokes on black background.
    const exportCanvas = document.createElement("canvas");
    exportCanvas.width = CANVAS_W;
    exportCanvas.height = CANVAS_H;
    const ctx = exportCanvas.getContext("2d");
    if (!ctx) return;
    ctx.fillStyle = "#000000";
    ctx.fillRect(0, 0, CANVAS_W, CANVAS_H);
    for (const s of strokes) drawStroke(ctx, s);
    const dataUrl = exportCanvas.toDataURL("image/png");
    try {
      const stabilized = await cacheCanvasMedia(dataUrl, "image");
      updateNode(id, {
        mediaUrl: stabilized.mediaUrl || dataUrl,
        mediaPath: stabilized.mediaPath,
        mediaType: "image",
        status: "done",
      } as Partial<CanvasNodeData>);
      showCanvasToast("success", t("canvas.maskExported" as any) || "Mask exported");
    } catch {
      updateNode(id, { mediaUrl: dataUrl, mediaType: "image", status: "done" } as Partial<CanvasNodeData>);
    }
  }, [strokes, id, updateNode, t]);

  return (
    <div
      className={`canvas-node canvas-node-mask ${selected ? "selected" : ""}`}
      role="article"
      aria-label={d.label || "Mask"}
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          <Layers size={14} />
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.mask" as any) || "Mask"}</span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>

      <div className="canvas-node-body mask-body nowheel nodrag">
        <div className="mask-canvas-wrapper" style={{ width: CANVAS_W, height: CANVAS_H }}>
          {bgUrl && (
            <img
              src={bgUrl}
              alt=""
              className="mask-bg-image"
              style={{ position: "absolute", inset: 0, width: "100%", height: "100%", objectFit: "cover", opacity: 0.6, pointerEvents: "none" }}
              draggable={false}
            />
          )}
          <canvas
            ref={canvasRef}
            width={CANVAS_W}
            height={CANVAS_H}
            style={{ position: "absolute", inset: 0, width: "100%", height: "100%", cursor: "crosshair", background: bgUrl ? "transparent" : "rgba(0,0,0,0.2)" }}
            onMouseDown={onMouseDown}
            onMouseMove={onMouseMove}
            onMouseUp={onMouseUp}
            onMouseLeave={onMouseUp}
          />
        </div>

        <div className="mask-toolbar">
          <button
            type="button"
            className={`mask-tool-btn ${tool === "pen" ? "active" : ""}`}
            onClick={() => setTool("pen")}
            title={t("canvas.brush")}
          >
            <Brush size={14} />
          </button>
          <button
            type="button"
            className={`mask-tool-btn ${tool === "eraser" ? "active" : ""}`}
            onClick={() => setTool("eraser")}
            title={t("canvas.eraser")}
          >
            <Eraser size={14} />
          </button>
          <span className="mask-toolbar-sep">·</span>
          {WIDTHS.map((w) => (
            <button
              key={w}
              type="button"
              className={`mask-width-btn ${width === w ? "active" : ""}`}
              onClick={() => setWidth(w)}
              title={`${w}px`}
            >
              <span className="mask-width-dot" style={{ width: w / 2, height: w / 2 }} />
            </button>
          ))}
          <div style={{ flex: 1 }} />
          <button
            type="button"
            className="mask-tool-btn"
            onClick={handleClear}
            title={t("canvas.clear")}
          >
            <Trash2 size={14} />
          </button>
          <button
            type="button"
            className="mask-export-btn"
            onClick={handleExport}
            title={t("canvas.export" as any) || "Export"}
          >
            <Check size={14} /> {t("canvas.export" as any) || "Export"}
          </button>
        </div>
      </div>

      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
