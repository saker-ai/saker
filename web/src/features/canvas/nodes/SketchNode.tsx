import { useState, useCallback, useEffect, useRef, memo } from "react";
import { createPortal } from "react-dom";
import { Handle, Position, type NodeProps, NodeResizer } from "@xyflow/react";
import {
  Brush, Eraser, Undo2, Redo2, Trash2, Check,
  Minus, Square, Circle as CircleIcon, ArrowUpRight,
  Type, ImagePlus, X, Pencil, PersonStanding,
  Maximize2, Minimize2,
} from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar, getMediaActions } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";
import { cacheCanvasMedia } from "../mediaCache";
import { showCanvasToast } from "../panels/CanvasToast";
import type { ToolType, FreehandStroke, ShapeStroke, TextStroke, Stroke, PoseKey, PoseLayer, PoseHandle } from "./SketchNode.types";
import {
  POSE_KEYS, JOINT_IDS, POSE_LAYER_PRESETS,
  clonePoseLayer, normalizePoses, flattenPoses,
  hitTestPoses, hitTestPoseBody, findPoseNearPoint, sameHandle,
} from "./SketchNode.pose";
import {
  COLORS, WIDTHS, CANVAS_W, CANVAS_H, SHAPE_TOOLS, CURSOR_MAP, redrawAll,
} from "./SketchNode.drawing";

export const SketchNode = memo(function SketchNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);

  const [editing, setEditing] = useState(!d.mediaUrl);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [tool, setTool] = useState<ToolType>("pen");
  const [color, setColor] = useState(COLORS[0]);
  const [width, setWidth] = useState(WIDTHS[1]);
  const [bg, setBg] = useState<"white" | "transparent" | "black">(d.sketchBackground || "white");

  const strokesRef = useRef<Stroke[]>([]);
  const redoStackRef = useRef<Stroke[]>([]);
  const isDrawingRef = useRef(false);
  const currentFreehandRef = useRef<FreehandStroke | null>(null);
  const shapeStartRef = useRef<[number, number] | null>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const bgImageRef = useRef<HTMLImageElement | null>(null);
  const bgFileRef = useRef<HTMLInputElement>(null);
  const [, forceUpdate] = useState(0);

  // Text input state
  const [textInput, setTextInput] = useState<{ x: number; y: number; cssX: number; cssY: number } | null>(null);
  const [textValue, setTextValue] = useState("");
  const textInputRef = useRef<HTMLInputElement>(null);

  // Pose menu visibility
  const [poseMenuOpen, setPoseMenuOpen] = useState(false);

  // Pose layers (draggable OpenPose skeleton overlay) — multiple poses coexist
  const posesRef = useRef<PoseLayer[]>(normalizePoses(d.poses));
  const draggingHandleRef = useRef<PoseHandle | null>(null);
  const [hoverHandle, setHoverHandle] = useState<PoseHandle | null>(null);
  // Whole-figure drag state: drag a bone (not a joint) to translate the
  // entire stick figure. Tracks the last canvas-space pointer so we can
  // compute frame-to-frame deltas.
  const draggingPoseRef = useRef<{ poseIndex: number; lastX: number; lastY: number } | null>(null);
  const [hoverPoseBody, setHoverPoseBody] = useState<number>(-1);
  const [hoverPoseDelete, setHoverPoseDelete] = useState<number>(-1);
  const [isCanvasHovered, setIsCanvasHovered] = useState(false);

  // Restore strokes and bg image from persisted data
  useEffect(() => {
    if (d.sketchData) {
      try { strokesRef.current = JSON.parse(d.sketchData); } catch { /* ignore */ }
    }
    if (d.sketchBgImage) {
      const img = new Image();
      img.onload = () => {
        bgImageRef.current = img;
        const cvs = canvasRef.current;
        if (cvs) {
          const ctx = cvs.getContext("2d");
          if (ctx) redrawAll(
            ctx, strokesRef.current, bg, img, cvs.width, cvs.height,
            posesRef.current, tool === "pose", hoverHandle,
          );
        }
      };
      img.src = d.sketchBgImage;
    }
  }, []);

  // Redraw canvas when entering edit mode, bg changes, or moving between
  // inline and fullscreen (canvas DOM is remounted across that boundary).
  useEffect(() => {
    if (!editing) return;
    const cvs = canvasRef.current;
    if (!cvs) return;
    const ctx = cvs.getContext("2d");
    if (!ctx) return;
    redrawAll(
      ctx, strokesRef.current, bg, bgImageRef.current, cvs.width, cvs.height,
      posesRef.current, tool === "pose", hoverHandle,
    );
  }, [editing, isFullscreen, bg, tool, hoverHandle]);

  // Escape exits fullscreen edit; convenient when keyboard focus is on the canvas.
  // Skip when focus is on an editable element so the text-input Escape (which
  // clears the inline input) doesn't also tear down the overlay.
  // Also: lock body scroll while fullscreen is active, and close any other
  // sketch node's fullscreen so two overlays never stack.
  useEffect(() => {
    if (!isFullscreen) return;

    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      const target = e.target as HTMLElement | null;
      if (target) {
        const tag = target.tagName;
        if (tag === "INPUT" || tag === "TEXTAREA" || target.isContentEditable) return;
      }
      setIsFullscreen(false);
    };
    window.addEventListener("keydown", onKey);

    // Tell every other sketch node to leave fullscreen.
    window.dispatchEvent(new CustomEvent("sketch-fullscreen-open", { detail: { id } }));
    const onPeerOpen = (e: Event) => {
      const detail = (e as CustomEvent).detail as { id?: string } | undefined;
      if (detail?.id && detail.id !== id) setIsFullscreen(false);
    };
    window.addEventListener("sketch-fullscreen-open", onPeerOpen);

    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("sketch-fullscreen-open", onPeerOpen);
      document.body.style.overflow = prevOverflow;
    };
  }, [isFullscreen, id]);

  // Focus text input when shown
  useEffect(() => {
    if (textInput && textInputRef.current) textInputRef.current.focus();
  }, [textInput]);

  const getCanvasXY = useCallback((e: React.MouseEvent | React.TouchEvent): [number, number] => {
    const cvs = canvasRef.current;
    if (!cvs) return [0, 0];
    const rect = cvs.getBoundingClientRect();
    const scaleX = cvs.width / rect.width;
    const scaleY = cvs.height / rect.height;
    if ("touches" in e) {
      const touch = e.touches[0] || e.changedTouches[0];
      return [(touch.clientX - rect.left) * scaleX, (touch.clientY - rect.top) * scaleY];
    }
    return [(e.clientX - rect.left) * scaleX, (e.clientY - rect.top) * scaleY];
  }, []);

  const getCssXY = useCallback((canvasX: number, canvasY: number): { cssX: number; cssY: number } => {
    const cvs = canvasRef.current;
    if (!cvs) return { cssX: 0, cssY: 0 };
    const rect = cvs.getBoundingClientRect();
    return {
      cssX: canvasX / (cvs.width / rect.width),
      cssY: canvasY / (cvs.height / rect.height),
    };
  }, []);

  const fullRedraw = useCallback((extraStroke?: Stroke) => {
    const cvs = canvasRef.current;
    if (!cvs) return;
    const ctx = cvs.getContext("2d");
    if (!ctx) return;
    const all = extraStroke ? [...strokesRef.current, extraStroke] : strokesRef.current;
    redrawAll(
      ctx, all, bg, bgImageRef.current, cvs.width, cvs.height,
      posesRef.current, tool === "pose", hoverHandle,
    );
  }, [bg, tool, hoverHandle]);

  const persistPoses = useCallback(() => {
    const arr = flattenPoses(posesRef.current);
    updateNode(id, { poses: arr.length > 0 ? arr : undefined } as Partial<CanvasNodeData>);
  }, [id, updateNode]);

  // ── Pointer handlers ──

  const onPointerDown = useCallback((e: React.MouseEvent | React.TouchEvent) => {
    e.stopPropagation();
    const [x, y] = getCanvasXY(e);

    if (tool === "pose") {
      const poses = posesRef.current;
      if (poses.length === 0) return;
      // 1) Joint hit: drag a single keypoint (existing behavior).
      const hit = hitTestPoses(poses, x, y);
      if (hit) {
        draggingHandleRef.current = hit;
        setHoverHandle(hit);
        return;
      }
      // 2) Bone hit: translate the whole figure so overlapping poses can
      // be separated quickly without dragging joints one by one.
      const bodyIdx = hitTestPoseBody(poses, x, y);
      if (bodyIdx >= 0) {
        draggingPoseRef.current = { poseIndex: bodyIdx, lastX: x, lastY: y };
      }
      return;
    }

    if (tool === "text") {
      const css = getCssXY(x, y);
      setTextInput({ x, y, cssX: css.cssX, cssY: css.cssY });
      setTextValue("");
      return;
    }

    isDrawingRef.current = true;
    redoStackRef.current = [];

    if (SHAPE_TOOLS.has(tool)) {
      shapeStartRef.current = [x, y];
    } else {
      currentFreehandRef.current = { points: [[x, y]], color, width, tool: tool as "pen" | "eraser" };
      fullRedraw(currentFreehandRef.current);
    }
  }, [tool, color, width, getCanvasXY, getCssXY, fullRedraw]);

  const onPointerMove = useCallback((e: React.MouseEvent | React.TouchEvent) => {
    const [x, y] = getCanvasXY(e);

    if (tool === "pose") {
      const poses = posesRef.current;
      if (poses.length === 0) return;
      if (draggingHandleRef.current) {
        e.stopPropagation();
        const { poseIndex, jointId } = draggingHandleRef.current;
        const pose = poses[poseIndex];
        if (pose) {
          pose[jointId] = [
            Math.max(0, Math.min(CANVAS_W, x)),
            Math.max(0, Math.min(CANVAS_H, y)),
          ];
          fullRedraw();
        }
      } else if (draggingPoseRef.current) {
        e.stopPropagation();
        const drag = draggingPoseRef.current;
        const pose = poses[drag.poseIndex];
        if (pose) {
          // Clamp the translation so no joint can be pushed outside the
          // canvas — preserves figure shape when dragged toward a wall.
          let dx = x - drag.lastX;
          let dy = y - drag.lastY;
          for (const jid of JOINT_IDS) {
            const [jx, jy] = pose[jid];
            dx = Math.max(-jx, Math.min(CANVAS_W - jx, dx));
            dy = Math.max(-jy, Math.min(CANVAS_H - jy, dy));
          }
          if (dx !== 0 || dy !== 0) {
            for (const jid of JOINT_IDS) {
              pose[jid] = [pose[jid][0] + dx, pose[jid][1] + dy];
            }
            fullRedraw();
          }
          draggingPoseRef.current = { poseIndex: drag.poseIndex, lastX: x, lastY: y };
        }
      } else {
        const hit = hitTestPoses(poses, x, y);
        if (!sameHandle(hit, hoverHandle)) setHoverHandle(hit);
        const bodyIdx = hit ? -1 : hitTestPoseBody(poses, x, y);
        if (bodyIdx !== hoverPoseBody) setHoverPoseBody(bodyIdx);
      }
      return;
    }

    if (!isDrawingRef.current) return;
    e.stopPropagation();

    if (SHAPE_TOOLS.has(tool) && shapeStartRef.current) {
      const preview: ShapeStroke = {
        tool: tool as ShapeStroke["tool"],
        start: shapeStartRef.current,
        end: [x, y],
        color, width,
      };
      fullRedraw(preview);
    } else if (currentFreehandRef.current) {
      currentFreehandRef.current.points.push([x, y]);
      fullRedraw(currentFreehandRef.current);
    }
  }, [tool, color, width, getCanvasXY, fullRedraw, hoverHandle, hoverPoseBody]);

  const onPointerUp = useCallback((e?: React.MouseEvent | React.TouchEvent) => {
    if (tool === "pose") {
      if (draggingHandleRef.current) {
        draggingHandleRef.current = null;
        persistPoses();
        fullRedraw();
        return;
      }
      if (draggingPoseRef.current) {
        draggingPoseRef.current = null;
        persistPoses();
        fullRedraw();
        forceUpdate((v) => v + 1);
        return;
      }
      return;
    }

    if (!isDrawingRef.current) return;
    isDrawingRef.current = false;

    if (SHAPE_TOOLS.has(tool) && shapeStartRef.current) {
      const [x, y] = e ? getCanvasXY(e) : shapeStartRef.current;
      const shape: ShapeStroke = {
        tool: tool as ShapeStroke["tool"],
        start: shapeStartRef.current,
        end: [x, y],
        color, width,
      };
      strokesRef.current = [...strokesRef.current, shape];
      shapeStartRef.current = null;
      fullRedraw();
    } else if (currentFreehandRef.current) {
      strokesRef.current = [...strokesRef.current, currentFreehandRef.current];
      currentFreehandRef.current = null;
    }
    forceUpdate((v) => v + 1);
  }, [tool, color, width, getCanvasXY, fullRedraw, persistPoses]);

  // Right-click on a pose in pose-tool mode removes that whole figure.
  const onCanvasContextMenu = useCallback((e: React.MouseEvent) => {
    if (tool !== "pose") return; // fall through to outer node context menu
    const poses = posesRef.current;
    if (poses.length === 0) return;
    const [x, y] = getCanvasXY(e);
    const idx = findPoseNearPoint(poses, x, y, 30);
    if (idx < 0) return;
    e.preventDefault();
    e.stopPropagation();
    posesRef.current = poses.filter((_, i) => i !== idx);
    setHoverHandle(null);
    persistPoses();
    if (posesRef.current.length === 0) setTool("pen");
    fullRedraw();
    forceUpdate((v) => v + 1);
  }, [tool, getCanvasXY, persistPoses, fullRedraw]);

  // ── Text commit ──

  const commitText = useCallback(() => {
    if (!textInput || !textValue.trim()) {
      setTextInput(null);
      setTextValue("");
      return;
    }
    const ts: TextStroke = {
      tool: "text",
      position: [textInput.x, textInput.y],
      text: textValue.trim(),
      color, width,
    };
    strokesRef.current = [...strokesRef.current, ts];
    redoStackRef.current = [];
    setTextInput(null);
    setTextValue("");
    fullRedraw();
    forceUpdate((v) => v + 1);
  }, [textInput, textValue, color, width, fullRedraw]);

  // ── Actions ──

  const handleUndo = useCallback(() => {
    if (strokesRef.current.length === 0) return;
    const popped = strokesRef.current[strokesRef.current.length - 1];
    strokesRef.current = strokesRef.current.slice(0, -1);
    redoStackRef.current = [...redoStackRef.current, popped];
    fullRedraw();
    forceUpdate((v) => v + 1);
  }, [fullRedraw]);

  const handleRedo = useCallback(() => {
    if (redoStackRef.current.length === 0) return;
    const popped = redoStackRef.current[redoStackRef.current.length - 1];
    redoStackRef.current = redoStackRef.current.slice(0, -1);
    strokesRef.current = [...strokesRef.current, popped];
    fullRedraw();
    forceUpdate((v) => v + 1);
  }, [fullRedraw]);

  const handleClear = useCallback(() => {
    strokesRef.current = [];
    redoStackRef.current = [];
    fullRedraw();
    forceUpdate((v) => v + 1);
  }, [fullRedraw]);

  const insertPose = useCallback((key: PoseKey) => {
    // Append a preset to the pose stack with a staggered offset so newly
    // added figures don't sit exactly on top of existing ones. Switching to
    // the pose tool makes the joint handles immediately usable.
    const next = clonePoseLayer(POSE_LAYER_PRESETS[key]);
    const count = posesRef.current.length;
    if (count > 0) {
      const dx = ((count % 4) + 1) * 25;
      const dy = Math.floor(count / 4) * 30;
      for (const jid of JOINT_IDS) {
        next[jid] = [
          Math.max(0, Math.min(CANVAS_W, next[jid][0] + dx)),
          Math.max(0, Math.min(CANVAS_H, next[jid][1] + dy)),
        ];
      }
    }
    posesRef.current = [...posesRef.current, next];
    setPoseMenuOpen(false);
    setTool("pose");
    persistPoses();
    fullRedraw();
    forceUpdate((v) => v + 1);
  }, [fullRedraw, persistPoses]);

  const clearPoses = useCallback(() => {
    posesRef.current = [];
    setHoverHandle(null);
    setHoverPoseBody(-1);
    setPoseMenuOpen(false);
    updateNode(id, { poses: undefined } as Partial<CanvasNodeData>);
    if (tool === "pose") setTool("pen");
    fullRedraw();
    forceUpdate((v) => v + 1);
  }, [fullRedraw, id, tool, updateNode]);

  const deletePose = useCallback((idx: number) => {
    if (idx < 0 || idx >= posesRef.current.length) return;
    posesRef.current = posesRef.current.filter((_, i) => i !== idx);
    setHoverHandle(null);
    setHoverPoseBody(-1);
    persistPoses();
    if (posesRef.current.length === 0 && tool === "pose") setTool("pen");
    fullRedraw();
    forceUpdate((v) => v + 1);
  }, [fullRedraw, persistPoses, tool]);

  const cycleBg = useCallback(() => {
    const order: Array<"white" | "transparent" | "black"> = ["white", "transparent", "black"];
    const next = order[(order.indexOf(bg) + 1) % order.length];
    setBg(next);
    updateNode(id, { sketchBackground: next } as Partial<CanvasNodeData>);
  }, [bg, id, updateNode]);

  // ── Background image import ──

  const handleImportBg = useCallback(() => {
    bgFileRef.current?.click();
  }, []);

  const handleBgFileChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file || !file.type.startsWith("image/")) return;
    const reader = new FileReader();
    reader.onload = async () => {
      const dataUrl = reader.result as string;
      const img = new Image();
      img.onload = async () => {
        bgImageRef.current = img;
        fullRedraw();
        // Offload heavy dataURL to media cache; persist stable URL instead.
        try {
          const stabilized = await cacheCanvasMedia(dataUrl, "image");
          updateNode(id, { sketchBgImage: stabilized.mediaUrl || dataUrl } as Partial<CanvasNodeData>);
        } catch {
          updateNode(id, { sketchBgImage: dataUrl } as Partial<CanvasNodeData>);
        }
      };
      img.src = dataUrl;
    };
    reader.readAsDataURL(file);
    if (e.target) e.target.value = "";
  }, [id, updateNode, fullRedraw]);

  const handleRemoveBg = useCallback(() => {
    bgImageRef.current = null;
    updateNode(id, { sketchBgImage: undefined } as Partial<CanvasNodeData>);
    fullRedraw();
  }, [id, updateNode, fullRedraw]);

  // ── Finish / export ──

  const handleFinish = useCallback(async () => {
    if (textInput) commitText();
    const cvs = canvasRef.current;
    if (!cvs) return;

    if (strokesRef.current.length === 0 && !bgImageRef.current && posesRef.current.length === 0) {
      setEditing(false);
      setIsFullscreen(false);
      return;
    }

    // Repaint once without joint handles so the exported PNG is a clean layer
    const ctx = cvs.getContext("2d");
    if (ctx) {
      redrawAll(
        ctx, strokesRef.current, bg, bgImageRef.current, cvs.width, cvs.height,
        posesRef.current, false, null,
      );
    }

    const dataUrl = cvs.toDataURL("image/png");
    const sketchData = JSON.stringify(strokesRef.current);
    const flatPoses = flattenPoses(posesRef.current);
    const poses = flatPoses.length > 0 ? flatPoses : undefined;

    updateNode(id, {
      mediaUrl: dataUrl,
      mediaType: "image",
      sketchData,
      sketchBackground: bg,
      poses,
      status: "done",
    } as Partial<CanvasNodeData>);

    setEditing(false);
    setIsFullscreen(false);

    try {
      const stabilized = await cacheCanvasMedia(dataUrl, "image");
      if (stabilized.mediaUrl && stabilized.mediaUrl !== dataUrl) {
        updateNode(id, {
          mediaUrl: stabilized.mediaUrl,
          mediaPath: stabilized.mediaPath,
          sourceUrl: stabilized.sourceUrl,
        } as Partial<CanvasNodeData>);
      }
    } catch { /* preview already set */ }
  }, [id, bg, updateNode, textInput, commitText]);

  const handleDoubleClick = useCallback(() => {
    if (!editing) setEditing(true);
  }, [editing]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, mediaUrl: d.mediaUrl, label: d.label },
      })
    );
  }, [id, d.mediaUrl, d.label]);

  // While editing, the top-right Fullscreen button toggles fullscreen edit
  // mode (instead of opening the read-only preview overlay). Icon and label
  // flip so the user can tell at a glance which direction the click goes.
  const actions = editing
    ? [{
        icon: isFullscreen ? <Minimize2 size={13} /> : <Maximize2 size={13} />,
        label: t((isFullscreen ? "canvas.sketchExitFullscreen" : "canvas.sketchFullscreen") as any),
        onClick: () => setIsFullscreen((v) => !v),
      }]
    : getMediaActions(d.mediaUrl, d.label);
  const isHighlighted = useCanvasStore((s) => s.highlightedTurnId != null && s.highlightedTurnId === d.turnId);
  const hasBgImage = !!d.sketchBgImage;

  const bgLabel = bg === "white" ? t("canvas.sketchBgWhite" as any)
    : bg === "black" ? t("canvas.sketchBgBlack" as any)
    : t("canvas.sketchBgTransparent" as any);

  return (
    <div
      className={`canvas-node canvas-node-sketch ${selected ? "selected" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""}`}
      role="article"
      aria-label={d.label || "Sketch"}
      onDoubleClick={handleDoubleClick}
      onContextMenu={handleContextMenu}
    >
      <NodeResizer
        color="var(--accent)"
        isVisible={selected && !editing}
        minWidth={200}
        minHeight={180}
        handleStyle={{ width: 8, height: 8, borderRadius: "50%", background: "var(--accent)", border: "2px solid white" }}
        lineStyle={{ border: "2px solid var(--accent)", opacity: 0.5 }}
      />
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          <Brush size={14} />
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.sketch" as any)}</span>
        {!editing && d.mediaUrl && !d.locked && (
          <button
            type="button"
            className="canvas-node-edit-btn"
            onClick={(e) => { e.stopPropagation(); setEditing(true); }}
            title={t("canvas.sketchEdit" as any)}
            aria-label={t("canvas.sketchEdit" as any)}
          >
            <Pencil size={12} />
          </button>
        )}
        <LockToggle nodeId={id} locked={d.locked} />
      </div>
      <div className="canvas-node-body media">
        {editing ? (
          isFullscreen ? (
            <div
              className="sketch-fullscreen-placeholder"
              onClick={(e) => { e.stopPropagation(); setIsFullscreen(false); }}
              title={t("canvas.sketchExitFullscreen" as any)}
            >
              <Maximize2 size={24} />
              <span>{t("canvas.sketchFullscreen" as any)}</span>
            </div>
          ) : (
            renderCanvasBlock()
          )
        ) : d.mediaUrl ? (
          <img src={d.mediaUrl} alt={d.label || "Sketch"} loading="lazy" />
        ) : (
          <div className="sketch-empty-hint" onClick={() => setEditing(true)}>
            <Brush size={32} />
            <span>{t("canvas.sketch" as any)}</span>
          </div>
        )}
      </div>

      {/* Hidden file input for bg image */}
      <input
        ref={bgFileRef}
        type="file"
        accept="image/*"
        style={{ display: "none" }}
        onChange={handleBgFileChange}
      />

      {editing && !isFullscreen && renderToolbar()}

      {editing && isFullscreen && typeof document !== "undefined" && createPortal(
        <div
          className="sketch-fullscreen-overlay nowheel nodrag nopan"
          onClick={() => setIsFullscreen(false)}
          onMouseDown={(e) => e.stopPropagation()}
          onContextMenu={(e) => e.stopPropagation()}
        >
          <button
            type="button"
            className="sketch-fullscreen-close"
            onClick={() => setIsFullscreen(false)}
            title={t("canvas.sketchExitFullscreen" as any)}
            aria-label={t("canvas.sketchExitFullscreen" as any)}
          >
            <X size={18} />
          </button>
          {renderCanvasBlock()}
          {renderToolbar()}
        </div>,
        document.body,
      )}

      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );

  function renderCanvasBlock() {
    // Pose-tool cursor: hint at the kind of grab the user will get.
    // - over a joint  → "grab"  (drag a single keypoint)
    // - over a bone   → "move"  (drag the whole figure)
    // - elsewhere     → "default"
    const dynamicCursor = tool !== "pose"
      ? CURSOR_MAP[tool]
      : hoverHandle ? "grab" : hoverPoseBody >= 0 ? "move" : "default";
    const showPoseDeleteButtons = tool === "pose" && posesRef.current.length > 0;
    return (
      <div
        className="sketch-canvas-wrapper"
        style={{ position: "relative" }}
        onClick={(e) => e.stopPropagation()}
      >
        {tool === "text" && !textInput && (
          <div className="sketch-text-hint" aria-hidden="true">
            {t("canvas.sketchTextHint" as any)}
          </div>
        )}
        {tool === "pose" && isCanvasHovered && (
          <div className="sketch-text-hint" aria-hidden="true">
            {t("canvas.sketchDragPoseHint" as any)}
          </div>
        )}
        <canvas
          ref={canvasRef}
          width={CANVAS_W}
          height={CANVAS_H}
          className="sketch-canvas nowheel nodrag nopan"
          style={{
            background: bg === "transparent"
              ? "repeating-conic-gradient(#d0d0d0 0% 25%, #fff 0% 50%) 0 0 / 16px 16px"
              : bg,
            cursor: dynamicCursor,
            touchAction: "none",
          }}
          onMouseDown={onPointerDown}
          onMouseMove={onPointerMove}
          onMouseUp={onPointerUp}
          onMouseEnter={() => setIsCanvasHovered(true)}
          onMouseLeave={() => {
            if (isDrawingRef.current) onPointerUp();
            setIsCanvasHovered(false);
            setHoverPoseBody(-1);
          }}
          onTouchStart={onPointerDown}
          onTouchMove={onPointerMove}
          onTouchEnd={onPointerUp}
          onContextMenu={onCanvasContextMenu}
        />
        {showPoseDeleteButtons && posesRef.current.map((pose, i) => {
          const visible = hoverPoseBody === i || hoverPoseDelete === i;
          if (!visible) return null;
          const [nx, ny] = pose.nose;
          return (
            <button
              key={`pose-del-${i}`}
              type="button"
              className="sketch-pose-delete-btn nowheel nodrag nopan"
              style={{
                left: `${(nx / CANVAS_W) * 100}%`,
                top: `${(ny / CANVAS_H) * 100}%`,
              }}
              onMouseDown={(e) => e.stopPropagation()}
              onTouchStart={(e) => e.stopPropagation()}
              onMouseEnter={() => setHoverPoseDelete(i)}
              onMouseLeave={() => setHoverPoseDelete(-1)}
              onClick={(e) => { e.stopPropagation(); deletePose(i); setHoverPoseDelete(-1); }}
              title={t("canvas.sketchDeletePose" as any)}
              aria-label={t("canvas.sketchDeletePose" as any)}
            >
              <X size={10} />
            </button>
          );
        })}
        {textInput && (
          <input
            ref={textInputRef}
            className="sketch-text-overlay nowheel nodrag nopan"
            style={{
              position: "absolute",
              left: textInput.cssX,
              top: textInput.cssY,
              color,
              fontSize: 10 + width * 2.5,
            }}
            value={textValue}
            onChange={(e) => setTextValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === "Escape") {
                // Keep keys local: Escape must not also exit fullscreen, and
                // Enter must not bubble to ReactFlow shortcuts.
                e.stopPropagation();
              }
              if (e.key === "Enter") commitText();
              if (e.key === "Escape") { setTextInput(null); setTextValue(""); }
            }}
            onBlur={commitText}
            placeholder={t("canvas.sketchText" as any)}
          />
        )}
      </div>
    );
  }

  function renderToolbar() {
    return (
      <div
        className="sketch-toolbar nowheel nodrag"
        onClick={(e) => e.stopPropagation()}
      >
          {/* Drawing tools */}
          <button className={`sketch-tool-btn ${tool === "pen" ? "active" : ""}`} onClick={() => setTool("pen")} title={t("canvas.sketchPen" as any)}>
            <Brush size={14} />
          </button>
          <button className={`sketch-tool-btn ${tool === "eraser" ? "active" : ""}`} onClick={() => setTool("eraser")} title={t("canvas.sketchEraser" as any)}>
            <Eraser size={14} />
          </button>

          <span className="sketch-sep" />

          {/* Shape tools */}
          <button className={`sketch-tool-btn ${tool === "line" ? "active" : ""}`} onClick={() => setTool("line")} title={t("canvas.sketchLine" as any)}>
            <Minus size={14} />
          </button>
          <button className={`sketch-tool-btn ${tool === "rect" ? "active" : ""}`} onClick={() => setTool("rect")} title={t("canvas.sketchRect" as any)}>
            <Square size={14} />
          </button>
          <button className={`sketch-tool-btn ${tool === "circle" ? "active" : ""}`} onClick={() => setTool("circle")} title={t("canvas.sketchCircle" as any)}>
            <CircleIcon size={14} />
          </button>
          <button className={`sketch-tool-btn ${tool === "arrow" ? "active" : ""}`} onClick={() => setTool("arrow")} title={t("canvas.sketchArrow" as any)}>
            <ArrowUpRight size={14} />
          </button>

          <span className="sketch-sep" />

          {/* Text tool */}
          <button className={`sketch-tool-btn ${tool === "text" ? "active" : ""}`} onClick={() => setTool("text")} title={t("canvas.sketchText" as any)}>
            <Type size={14} />
          </button>

          {/* Draggable pose layer — preset picker + joint-drag tool. Multiple
              figures supported: each preset click appends a new pose; right-click
              a pose on the canvas (in pose tool) to remove just that one. */}
          <div className="sketch-pose-wrapper">
            <button
              type="button"
              className={`sketch-tool-btn ${tool === "pose" || poseMenuOpen ? "active" : ""}`}
              onClick={() => {
                if (posesRef.current.length === 0) {
                  setPoseMenuOpen((v) => !v);
                } else if (tool !== "pose") {
                  setTool("pose");
                } else {
                  setPoseMenuOpen((v) => !v);
                }
              }}
              onContextMenu={(e) => { e.preventDefault(); setPoseMenuOpen((v) => !v); }}
              title={t("canvas.sketchInsertPose" as any)}
            >
              <PersonStanding size={14} />
            </button>
            {poseMenuOpen && (
              <div className="sketch-pose-menu">
                {POSE_KEYS.map((key) => (
                  <button
                    key={key}
                    type="button"
                    className="sketch-pose-item"
                    onClick={() => insertPose(key)}
                  >
                    {t(`canvas.pose.${key}` as any)}
                  </button>
                ))}
                {posesRef.current.length > 0 && (
                  <button
                    type="button"
                    className="sketch-pose-item"
                    onClick={clearPoses}
                    style={{ borderTop: "1px solid rgba(255,255,255,0.08)" }}
                  >
                    {t("canvas.sketchClearPose" as any)}
                  </button>
                )}
              </div>
            )}
          </div>

          <span className="sketch-sep" />

          {/* Colors */}
          {COLORS.map((c) => (
            <button
              key={c}
              className={`sketch-color-btn ${color === c ? "active" : ""}`}
              style={{ background: c, border: c === "#ffffff" ? "1px solid var(--border)" : "2px solid transparent" }}
              onClick={() => { setColor(c); if (tool === "eraser") setTool("pen"); }}
            />
          ))}

          <span className="sketch-sep" />

          {/* Widths */}
          {WIDTHS.map((w) => (
            <button key={w} className={`sketch-width-btn ${width === w ? "active" : ""}`} onClick={() => setWidth(w)}>
              <span className="sketch-width-dot" style={{ width: w + 4, height: w + 4 }} />
            </button>
          ))}

          <span className="sketch-sep" />

          {/* Background controls */}
          <button className="sketch-tool-btn" onClick={cycleBg} title={bgLabel}>
            <span className="sketch-bg-indicator" data-bg={bg} />
          </button>
          {hasBgImage ? (
            <button className="sketch-tool-btn" onClick={handleRemoveBg} title={t("canvas.sketchRemoveBg" as any)}>
              <X size={14} />
            </button>
          ) : (
            <button className="sketch-tool-btn" onClick={handleImportBg} title={t("canvas.sketchImportBg" as any)}>
              <ImagePlus size={14} />
            </button>
          )}

          <span className="sketch-sep" />

          {/* Undo / Redo / Clear */}
          <button className="sketch-tool-btn" onClick={handleUndo} disabled={strokesRef.current.length === 0} title="Undo">
            <Undo2 size={14} />
          </button>
          <button className="sketch-tool-btn" onClick={handleRedo} disabled={redoStackRef.current.length === 0} title="Redo">
            <Redo2 size={14} />
          </button>
          <button className="sketch-tool-btn" onClick={handleClear} title={t("canvas.sketchClear" as any)}>
            <Trash2 size={14} />
          </button>

          <div style={{ flex: 1 }} />

          <button
            type="button"
            className="sketch-tool-btn"
            onClick={() => setIsFullscreen((v) => !v)}
            title={isFullscreen ? t("canvas.sketchExitFullscreen" as any) : t("canvas.sketchFullscreen" as any)}
            aria-label={isFullscreen ? t("canvas.sketchExitFullscreen" as any) : t("canvas.sketchFullscreen" as any)}
          >
            {isFullscreen ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
          </button>

          <button className="sketch-finish-btn" onClick={handleFinish} title={t("canvas.sketchFinish" as any)}>
            <Check size={14} />
            <span>{t("canvas.sketchFinish" as any)}</span>
          </button>
        </div>
      );
  }
});