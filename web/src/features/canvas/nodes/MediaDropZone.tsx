import { useState, useCallback, useRef } from "react";
import { Upload, Loader2, Link2 } from "lucide-react";
import { useT, type TKey } from "@/features/i18n";
import { cacheCanvasMedia } from "../mediaCache";
import type { CanvasNodeData } from "../types";

export type MediaKind = "image" | "video" | "audio";

interface MediaDropZoneProps {
  nodeId: string;
  kind: MediaKind;
  /** If set, show a "source linked" indicator instead of the drop zone. */
  upstreamMediaUrl?: string | null;
  /** Whether to show a URL input row (image/video have it, audio doesn't). */
  showUrlInput?: boolean;
  /** Max file size in bytes. */
  maxSize?: number;
  /** Called with the stabilized media data after a successful drop/paste/URL. */
  onMedia: (data: Pick<CanvasNodeData, "mediaUrl" | "mediaPath" | "sourceUrl" | "status">) => void;
  /** Called for instant blob preview (image only). Return true to skip data URL read. */
  onInstantPreview?: (blobUrl: string) => void;
}

const ACCEPT_MAP: Record<MediaKind, string> = {
  image: "image/*",
  video: "video/*",
  audio: "audio/*",
};

const DROP_LABEL_KEY: Record<MediaKind, TKey> = {
  image: "canvas.dropImage",
  video: "canvas.dropVideo",
  audio: "canvas.dropAudio",
};

const URL_PLACEHOLDER_KEY: Record<MediaKind, TKey> = {
  image: "canvas.pasteUrl",
  video: "canvas.pasteVideoUrl",
  audio: "canvas.pasteUrl",
};

export function MediaDropZone({
  kind,
  upstreamMediaUrl,
  showUrlInput = false,
  maxSize = 50 * 1024 * 1024,
  onMedia,
  onInstantPreview,
}: MediaDropZoneProps) {
  const { t } = useT();
  const [dragOver, setDragOver] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [urlInput, setUrlInput] = useState("");
  const [error, setError] = useState("");
  const fileInputRef = useRef<HTMLInputElement>(null);

  const handleFileDrop = useCallback(async (file: File) => {
    if (!file.type.startsWith(`${kind}/`)) return;
    if (file.size > maxSize) { setError(t("canvas.fileTooLarge")); return; }

    if (onInstantPreview) {
      const blobUrl = URL.createObjectURL(file);
      onInstantPreview(blobUrl);
      // Stabilize in background
      try {
        const dataUrl = await new Promise<string>((resolve, reject) => {
          const reader = new FileReader();
          reader.onload = () => resolve(reader.result as string);
          reader.onerror = reject;
          reader.readAsDataURL(file);
        });
        const stabilized = await cacheCanvasMedia(dataUrl, kind);
        onMedia({
          mediaUrl: stabilized.mediaUrl || dataUrl,
          mediaPath: stabilized.mediaPath,
          sourceUrl: stabilized.sourceUrl,
        } as Pick<CanvasNodeData, "mediaUrl" | "mediaPath" | "sourceUrl" | "status">);
      } catch { /* preview already showing */ }
      finally { URL.revokeObjectURL(blobUrl); }
      return;
    }

    setUploading(true);
    try {
      const dataUrl = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result as string);
        reader.onerror = reject;
        reader.readAsDataURL(file);
      });
      const stabilized = await cacheCanvasMedia(dataUrl, kind);
      onMedia({
        mediaUrl: stabilized.mediaUrl || dataUrl,
        mediaPath: stabilized.mediaPath,
        sourceUrl: stabilized.sourceUrl,
        status: "done",
      });
    } catch {
      setError(t("canvas.uploadFailed"));
    } finally {
      setUploading(false);
    }
  }, [kind, maxSize, onMedia, onInstantPreview, t]);

  const handlePaste = useCallback((e: React.ClipboardEvent) => {
    const items = e.clipboardData?.items;
    if (!items) return;
    for (const item of items) {
      if (item.type.startsWith(`${kind}/`)) {
        e.preventDefault();
        const file = item.getAsFile();
        if (file) handleFileDrop(file);
        return;
      }
    }
  }, [kind, handleFileDrop]);

  const handleFileSelect = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) handleFileDrop(file);
    if (e.target) e.target.value = "";
  }, [handleFileDrop]);

  const handleUrlSubmit = useCallback(async () => {
    const url = urlInput.trim();
    if (!url) return;
    setUploading(true);
    try {
      const stabilized = await cacheCanvasMedia(url, kind);
      onMedia({
        mediaUrl: stabilized.mediaUrl || url,
        mediaPath: stabilized.mediaPath,
        sourceUrl: stabilized.sourceUrl,
        status: "done",
      });
      setUrlInput("");
    } catch {
      setError(t("canvas.loadUrlFailed"));
    } finally {
      setUploading(false);
    }
  }, [urlInput, kind, onMedia, t]);

  if (upstreamMediaUrl) {
    return (
      <div className="image-drop-zone linked">
        <Link2 size={20} />
        <span>{t("canvas.sourceLinked")}</span>
      </div>
    );
  }

  return (
    <div
      className={`image-drop-zone ${dragOver ? "drag-over" : ""}`}
      tabIndex={0}
      onDragOver={(e) => { e.preventDefault(); e.stopPropagation(); setDragOver(true); }}
      onDragLeave={() => setDragOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        e.stopPropagation();
        setDragOver(false);
        const file = e.dataTransfer.files[0];
        if (file) handleFileDrop(file);
      }}
      onPaste={handlePaste}
    >
      <input
        ref={fileInputRef}
        type="file"
        accept={ACCEPT_MAP[kind]}
        style={{ display: "none" }}
        onChange={handleFileSelect}
      />
      {uploading ? (
        <Loader2 size={20} className="animate-spin" />
      ) : (
        <div
          className="image-drop-clickable"
          onClick={() => fileInputRef.current?.click()}
        >
          <Upload size={20} />
          <span>{t(DROP_LABEL_KEY[kind])}</span>
        </div>
      )}
      {showUrlInput && (
        <div className="image-url-input-row nodrag nowheel">
          <input
            className="image-url-input"
            placeholder={t(URL_PLACEHOLDER_KEY[kind])}
            value={urlInput}
            onChange={(e) => setUrlInput(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") handleUrlSubmit(); }}
            disabled={uploading}
          />
        </div>
      )}
      {error && <span style={{ color: "var(--error)", fontSize: "0.7rem" }}>{error}</span>}
    </div>
  );
}
