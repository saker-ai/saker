// ── Sketch drawing constants and helper functions ──

import type { FreehandStroke, ShapeStroke, TextStroke, Stroke, PoseLayer, PoseHandle } from "./SketchNode.types";
import type { ToolType } from "./SketchNode.types";
import { drawPoseOverlay } from "./SketchNode.pose";

// ── Constants ──

export const COLORS = ["#000000", "#ef4444", "#3b82f6", "#22c55e", "#f97316", "#a855f7", "#ffffff"];
export const WIDTHS = [2, 5, 10];
export const CANVAS_W = 400;
export const CANVAS_H = 300;
export const SHAPE_TOOLS = new Set<ToolType>(["line", "rect", "circle", "arrow"]);

export const CURSOR_MAP: Record<ToolType, string> = {
  pen: "crosshair", eraser: "cell", line: "crosshair",
  rect: "crosshair", circle: "crosshair", arrow: "crosshair", text: "text",
  pose: "move",
};

// ── Drawing helpers ──

function drawFreehand(ctx: CanvasRenderingContext2D, s: FreehandStroke) {
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
    ctx.strokeStyle = s.color;
  }
  ctx.beginPath();
  const [x0, y0] = s.points[0];
  ctx.moveTo(x0, y0);
  if (s.points.length === 1) {
    ctx.lineTo(x0 + 0.1, y0);
  } else {
    for (let i = 1; i < s.points.length; i++) {
      const [px, py] = s.points[i - 1];
      const [cx, cy] = s.points[i];
      ctx.quadraticCurveTo(px, py, (px + cx) / 2, (py + cy) / 2);
    }
    const last = s.points[s.points.length - 1];
    ctx.lineTo(last[0], last[1]);
  }
  ctx.stroke();
  ctx.restore();
}

function drawShape(ctx: CanvasRenderingContext2D, s: ShapeStroke) {
  ctx.save();
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.lineWidth = s.width;
  ctx.strokeStyle = s.color;
  ctx.globalCompositeOperation = "source-over";

  const [x1, y1] = s.start;
  const [x2, y2] = s.end;

  if (s.tool === "line" || s.tool === "arrow") {
    ctx.beginPath();
    ctx.moveTo(x1, y1);
    ctx.lineTo(x2, y2);
    ctx.stroke();
    if (s.tool === "arrow") {
      const angle = Math.atan2(y2 - y1, x2 - x1);
      const headLen = Math.max(10, s.width * 3);
      ctx.beginPath();
      ctx.moveTo(x2, y2);
      ctx.lineTo(x2 - headLen * Math.cos(angle - Math.PI / 6), y2 - headLen * Math.sin(angle - Math.PI / 6));
      ctx.moveTo(x2, y2);
      ctx.lineTo(x2 - headLen * Math.cos(angle + Math.PI / 6), y2 - headLen * Math.sin(angle + Math.PI / 6));
      ctx.stroke();
    }
  } else if (s.tool === "rect") {
    ctx.strokeRect(Math.min(x1, x2), Math.min(y1, y2), Math.abs(x2 - x1), Math.abs(y2 - y1));
  } else if (s.tool === "circle") {
    const cx = (x1 + x2) / 2;
    const cy = (y1 + y2) / 2;
    const rx = Math.abs(x2 - x1) / 2;
    const ry = Math.abs(y2 - y1) / 2;
    ctx.beginPath();
    ctx.ellipse(cx, cy, rx, ry, 0, 0, Math.PI * 2);
    ctx.stroke();
  }
  ctx.restore();
}

function drawText(ctx: CanvasRenderingContext2D, s: TextStroke) {
  if (!s.text) return;
  ctx.save();
  ctx.globalCompositeOperation = "source-over";
  const fontSize = 10 + s.width * 2.5;
  ctx.font = `${fontSize}px sans-serif`;
  ctx.fillStyle = s.color;
  ctx.textBaseline = "top";
  ctx.fillText(s.text, s.position[0], s.position[1]);
  ctx.restore();
}

function drawStroke(ctx: CanvasRenderingContext2D, s: Stroke) {
  if (s.tool === "pen" || s.tool === "eraser") drawFreehand(ctx, s as FreehandStroke);
  else if (s.tool === "text") drawText(ctx, s as TextStroke);
  else drawShape(ctx, s as ShapeStroke);
}

export function redrawAll(
  ctx: CanvasRenderingContext2D,
  strokes: Stroke[],
  bg: "white" | "transparent" | "black",
  bgImage: HTMLImageElement | null,
  w: number,
  h: number,
  poses: PoseLayer[] = [],
  showPoseHandles: boolean = false,
  poseHover: PoseHandle | null = null,
) {
  ctx.clearRect(0, 0, w, h);
  if (bg !== "transparent") {
    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, w, h);
  }
  if (bgImage) {
    ctx.drawImage(bgImage, 0, 0, w, h);
  }
  // Draw strokes on offscreen canvas so eraser only erases drawn content, not bg
  const off = document.createElement("canvas");
  off.width = w;
  off.height = h;
  const offCtx = off.getContext("2d");
  if (offCtx) {
    for (const s of strokes) drawStroke(offCtx, s);
    ctx.drawImage(off, 0, 0);
  }
  // Pose overlay sits on top of strokes so it reads as a clear reference layer
  drawPoseOverlay(ctx, poses, showPoseHandles, poseHover);
}