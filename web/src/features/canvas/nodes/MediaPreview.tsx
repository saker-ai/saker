"use client";

import { useEffect, useState, useCallback, useRef } from "react";
import { X, ZoomIn, ZoomOut, RotateCw } from "lucide-react";

interface PreviewData {
  url?: string;
  text?: string;
  type: "image" | "video" | "text";
  label?: string;
}

export function MediaPreview() {
  const [preview, setPreview] = useState<PreviewData | null>(null);
  const [scale, setScale] = useState(1);
  const [rotate, setRotate] = useState(0);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const dragging = useRef(false);
  const dragStart = useRef({ x: 0, y: 0 });

  const close = useCallback(() => {
    setPreview(null);
    setScale(1);
    setRotate(0);
    setOffset({ x: 0, y: 0 });
  }, []);

  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent).detail as PreviewData;
      setPreview(detail);
      setScale(1);
      setRotate(0);
      setOffset({ x: 0, y: 0 });
    };
    window.addEventListener("canvas-preview", handler);
    return () => window.removeEventListener("canvas-preview", handler);
  }, []);

  useEffect(() => {
    if (!preview) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [preview, close]);

  // Scroll to zoom for images
  const handleWheel = useCallback(
    (e: React.WheelEvent) => {
      if (preview?.type !== "image") return;
      e.preventDefault();
      setScale((s) => Math.max(0.1, Math.min(10, s + (e.deltaY < 0 ? 0.15 : -0.15))));
    },
    [preview]
  );

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      if (preview?.type !== "image") return;
      dragging.current = true;
      dragStart.current = { x: e.clientX - offset.x, y: e.clientY - offset.y };
    },
    [preview, offset]
  );

  const handleMouseMove = useCallback(
    (e: React.MouseEvent) => {
      if (!dragging.current) return;
      setOffset({
        x: e.clientX - dragStart.current.x,
        y: e.clientY - dragStart.current.y,
      });
    },
    []
  );

  const handleMouseUp = useCallback(() => {
    dragging.current = false;
  }, []);

  if (!preview) return null;

  const isImage = preview.type === "image";

  return (
    <div className="canvas-preview-overlay" onClick={close}>
      <button className="canvas-preview-close" onClick={close}>
        <X size={20} />
      </button>

      {/* Zoom/rotate controls for images */}
      {isImage && (
        <div className="canvas-preview-controls" onClick={(e) => e.stopPropagation()}>
          <button onClick={() => setScale((s) => Math.min(10, s + 0.25))} title="Zoom in">
            <ZoomIn size={16} />
          </button>
          <span className="canvas-preview-scale">{Math.round(scale * 100)}%</span>
          <button onClick={() => setScale((s) => Math.max(0.1, s - 0.25))} title="Zoom out">
            <ZoomOut size={16} />
          </button>
          <button onClick={() => setRotate((r) => (r + 90) % 360)} title="Rotate">
            <RotateCw size={16} />
          </button>
        </div>
      )}

      <div
        className="canvas-preview-content"
        onClick={(e) => e.stopPropagation()}
        onWheel={handleWheel}
        onMouseDown={handleMouseDown}
        onMouseMove={handleMouseMove}
        onMouseUp={handleMouseUp}
        onMouseLeave={handleMouseUp}
      >
        {isImage && preview.url && (
          <img
            src={preview.url}
            alt={preview.label || "Preview"}
            style={{
              transform: `translate(${offset.x}px, ${offset.y}px) scale(${scale}) rotate(${rotate}deg)`,
              cursor: scale > 1 ? "grab" : "default",
              transition: dragging.current ? "none" : "transform 0.15s ease",
            }}
            draggable={false}
          />
        )}
        {preview.type === "video" && preview.url && (
          <video src={preview.url} controls autoPlay />
        )}
        {preview.type === "text" && preview.text && (
          <div className="canvas-preview-text">
            <pre>{preview.text}</pre>
          </div>
        )}
      </div>
    </div>
  );
}
