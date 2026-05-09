"use client";

import { useEffect } from "react";
import { useAssetStore } from "@/features/canvas/panels/assetStore";
import { useCanvasStore } from "@/features/canvas/store";
import type { CanvasNodeData } from "@/features/canvas/types";
import { showCanvasToast } from "@/features/canvas/panels/CanvasToast";
import {
  EDITOR_EXPORT_BROADCAST_CHANNEL,
  type EditorExportMessage,
  isEditorExportMessage,
} from "./protocol";

interface BridgeOpts {
  importedLabel?: string;
}

const SEEN_CAP = 64;

/**
 * Listen for editor exports from two transports:
 *   1. window.message (postMessage on the opener window)
 *   2. BroadcastChannel('saker:editor:export') — survives opener loss
 *
 * Same-origin guard prevents cross-origin messages from polluting state.
 * Dedupe via the messageId carried by both transports.
 */
export function useEditorBridge(opts: BridgeOpts = {}): void {
  useEffect(() => {
    if (typeof window === "undefined") return;

    const seen = new Set<string>();
    const seenOrder: string[] = [];

    function rememberOnce(id: string | undefined): boolean {
      if (!id) return true;
      if (seen.has(id)) return false;
      seen.add(id);
      seenOrder.push(id);
      while (seenOrder.length > SEEN_CAP) {
        const drop = seenOrder.shift();
        if (drop) seen.delete(drop);
      }
      return true;
    }

    function ingest(msg: EditorExportMessage) {
      const { filename, dataUrl, blob, mimeType, originNodeId } = msg;
      const type = inferAssetType(filename, mimeType, blob?.type);
      try {
        let url: string | null = null;
        if (blob instanceof Blob) {
          url = URL.createObjectURL(blob);
        } else if (typeof dataUrl === "string" && dataUrl.length > 0) {
          url = dataUrl;
        }
        if (!url) return;

        if (originNodeId) {
          // In-place update: the editor was opened from this node, so the
          // export belongs back in it rather than the library.
          const store = useCanvasStore.getState();
          const exists = store.nodes.some((n) => n.id === originNodeId);
          if (exists) {
            store.updateNode(originNodeId, {
              mediaUrl: url,
              mediaType: type,
              exportStatus: "done",
              exportedAt: Date.now(),
            } as Partial<CanvasNodeData>);
            showCanvasToast(
              "success",
              opts.importedLabel || "Updated from editor",
            );
            return;
          }
          // Fall through to library if the source node is gone.
        }

        useAssetStore.getState().addAsset({
          type,
          url,
          label: filename || "Editor export",
        });
        showCanvasToast("success", opts.importedLabel || "Imported from editor");
      } catch (err) {
        showCanvasToast("error", `Editor import failed: ${(err as Error).message}`);
      }
    }

    function onWindowMessage(ev: MessageEvent) {
      if (ev.origin !== window.location.origin) return;
      if (!isEditorExportMessage(ev.data)) return;
      if (!rememberOnce(ev.data.messageId)) return;
      ingest(ev.data);
    }

    let channel: BroadcastChannel | null = null;
    function onChannelMessage(ev: MessageEvent) {
      if (!isEditorExportMessage(ev.data)) return;
      if (!rememberOnce(ev.data.messageId)) return;
      ingest(ev.data);
    }

    window.addEventListener("message", onWindowMessage);
    if (typeof BroadcastChannel !== "undefined") {
      try {
        channel = new BroadcastChannel(EDITOR_EXPORT_BROADCAST_CHANNEL);
        channel.addEventListener("message", onChannelMessage);
      } catch {
        channel = null;
      }
    }

    return () => {
      window.removeEventListener("message", onWindowMessage);
      if (channel) {
        try {
          channel.removeEventListener("message", onChannelMessage);
          channel.close();
        } catch {
          /* ignore */
        }
      }
    };
  }, [opts.importedLabel]);
}

function inferAssetType(
  filename: string,
  mimeType?: string,
  blobType?: string,
): "video" | "audio" | "image" {
  const mime = (mimeType || blobType || "").toLowerCase();
  if (mime.startsWith("video/")) return "video";
  if (mime.startsWith("audio/")) return "audio";
  if (mime.startsWith("image/")) return "image";
  const ext = filename.toLowerCase().split(".").pop() || "";
  if (["mp4", "mov", "webm", "mkv", "m4v"].includes(ext)) return "video";
  if (["mp3", "wav", "m4a", "aac", "ogg", "flac"].includes(ext)) return "audio";
  return "image";
}
