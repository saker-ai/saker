import { memo, useState, useCallback, useEffect, useMemo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Download, Archive, Loader2, Check, Film, ExternalLink } from "lucide-react";
import type { CanvasNodeData, ExportMode } from "../types";
import { useCanvasStore } from "../store";
import { useAssetStore } from "../panels/assetStore";
import { useT } from "@/features/i18n";
import { NodeToolbar } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { ToolbarDropdown } from "./ToolbarDropdown";
import { showCanvasToast } from "../panels/CanvasToast";
import {
  composeAndDownload,
  ComposeCorsError,
  ComposeUnsupportedError,
  isComposeSupported,
} from "@/features/editor-runtime";
import { buildEditorImportUrl, type EditorAsset } from "@/features/editor-bridge";

const BASE_MODES: ExportMode[] = ["download", "library"];

function inferExtension(url: string, mediaType?: string): string {
  try {
    const u = new URL(url, window.location.href);
    const match = u.pathname.match(/\.([a-zA-Z0-9]+)(?:$|\?)/);
    if (match) return match[1].toLowerCase();
  } catch {
    /* ignore */
  }
  if (mediaType === "video") return "mp4";
  if (mediaType === "audio") return "mp3";
  return "png";
}

export const ExportNode = memo(function ExportNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const edges = useCanvasStore((s) => s.edges);
  const nodes = useCanvasStore((s) => s.nodes);
  const [mode, setMode] = useState<ExportMode>((d.exportMode as ExportMode) || "download");

  const upstream = useMemo(() => {
    const upstreamIds = edges.filter((e) => e.target === id).map((e) => e.source);
    for (const sid of upstreamIds) {
      const n = nodes.find((x) => x.id === sid);
      const nd = n?.data as CanvasNodeData | undefined;
      if (nd && typeof nd.mediaUrl === "string" && nd.mediaUrl) {
        return {
          url: nd.mediaUrl,
          type: (typeof nd.mediaType === "string" ? nd.mediaType : "image") as "image" | "video" | "audio",
          label: nd.label || "Export",
        };
      }
    }
    return undefined;
  }, [id, edges, nodes]);

  useEffect(() => {
    updateNode(id, { exportMode: mode } as Partial<CanvasNodeData>);
  }, [mode, id, updateNode]);

  const [composePct, setComposePct] = useState(0);

  const supportsCompose = useMemo(
    () => (typeof window === "undefined" ? false : isComposeSupported()),
    [],
  );

  const handleExport = useCallback(async () => {
    if (!upstream) {
      showCanvasToast("error", t("canvas.exportNoUpstream") || "Connect a media node first");
      return;
    }
    updateNode(id, { exportStatus: "running" } as Partial<CanvasNodeData>);
    setComposePct(0);
    try {
      if (mode === "download") {
        const ext = inferExtension(upstream.url, upstream.type);
        const name = `${(upstream.label || "export").replace(/[^\w\-]+/g, "_").slice(0, 60)}.${ext}`;
        const a = document.createElement("a");
        a.href = upstream.url;
        a.download = name;
        a.rel = "noopener";
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
      } else if (mode === "library") {
        useAssetStore.getState().addAsset({
          type: upstream.type,
          url: upstream.url,
          label: upstream.label,
        });
      } else if (mode === "compose-mp4") {
        if (upstream.type !== "video") {
          throw new Error("compose-mp4 requires a video upstream");
        }
        if (!supportsCompose) {
          throw new ComposeUnsupportedError(
            t("canvas.compose.unsupported") || "Browser unsupported",
          );
        }
        const safeName = (upstream.label || "export").replace(/[^\w\-]+/g, "_").slice(0, 60) || "export";
        await composeAndDownload([{ url: upstream.url, label: upstream.label }], safeName, {
          onProgress: (p) => setComposePct(Math.max(0, Math.min(1, p))),
        });
      }
      updateNode(id, { exportStatus: "done", exportedAt: Date.now() } as Partial<CanvasNodeData>);
      showCanvasToast("success", t("canvas.exportSuccess") || "Exported");
    } catch (err) {
      updateNode(id, { exportStatus: "error" } as Partial<CanvasNodeData>);
      let label = t("canvas.exportFailed") || "Export failed";
      if (err instanceof ComposeCorsError) {
        label = t("canvas.compose.corsHint") || label;
      } else if (err instanceof ComposeUnsupportedError) {
        label = t("canvas.compose.unsupported") || label;
      } else if (err instanceof Error) {
        label = `${label}: ${err.message}`;
      } else {
        label = `${label}: ${err}`;
      }
      showCanvasToast("error", label);
    } finally {
      setComposePct(0);
    }
  }, [upstream, mode, id, updateNode, t, supportsCompose]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, mediaUrl: upstream?.url, label: d.label },
      })
    );
  }, [id, upstream, d.label]);

  const handleOpenInEditor = useCallback(() => {
    if (!upstream) {
      showCanvasToast("error", t("canvas.exportNoUpstream") || "Connect a media node first");
      return;
    }
    const asset: EditorAsset = {
      url: upstream.url,
      type: upstream.type,
      label: upstream.label,
    };
    const url = buildEditorImportUrl({ assets: [asset], originNodeId: id });
    // Keep opener for editor → main postMessage round-trip; same-origin makes this safe.
    window.open(url, "_blank");
  }, [upstream, id, t]);

  const availableModes = useMemo<ExportMode[]>(() => {
    const list = [...BASE_MODES];
    if (upstream?.type === "video" && supportsCompose) list.push("compose-mp4");
    return list;
  }, [upstream?.type, supportsCompose]);

  // If user previously picked compose-mp4 but upstream changed, fall back to download
  useEffect(() => {
    if (!availableModes.includes(mode)) setMode("download");
  }, [availableModes, mode]);

  const modeOptions = useMemo(
    () => availableModes.map((m) => ({ value: m, label: t(`canvas.exportMode.${m}` as any) || m })),
    [availableModes, t],
  );

  const isRunning = d.exportStatus === "running";
  const isDone = d.exportStatus === "done";

  const submitLabel = (() => {
    if (mode === "compose-mp4" && isRunning) {
      const pct = Math.round(composePct * 100);
      const tpl = t("canvas.compose.running") as unknown as string;
      if (typeof tpl === "string" && tpl.includes("{pct}")) return tpl.replace("{pct}", String(pct));
      return `${pct}%`;
    }
    return t("canvas.exportNow") || "Export";
  })();

  const submitIcon = (() => {
    if (isRunning) return <Loader2 size={14} className="animate-spin" />;
    if (isDone) return <Check size={14} />;
    if (mode === "compose-mp4") return <Film size={14} />;
    return <Download size={14} />;
  })();

  return (
    <div
      className={`canvas-node canvas-node-export ${selected ? "selected" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
      aria-label={d.label || "Export"}
    >
      <NodeToolbar nodeId={id} selected={selected} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          {mode === "download" ? <Download size={14} /> : <Archive size={14} />}
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.export") || "Export"}</span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>

      <div className="canvas-node-body export-body">
        {upstream?.url ? (
          <div className="export-preview">
            {upstream.type === "video" ? (
              <video src={upstream.url} muted loop playsInline className="export-preview-media" />
            ) : upstream.type === "audio" ? (
              <audio src={upstream.url} controls className="export-preview-audio" />
            ) : (
              <img src={upstream.url} alt="" className="export-preview-media" draggable={false} />
            )}
          </div>
        ) : (
          <div className="export-preview empty">{t("canvas.exportEmpty") || "Connect a media node"}</div>
        )}

        <div className="export-toolbar nodrag">
          <ToolbarDropdown
            options={modeOptions}
            value={mode}
            onChange={(v) => setMode(v as ExportMode)}
            disabled={isRunning}
          />
          <div style={{ flex: 1 }} />
          <button
            type="button"
            className="export-submit-btn"
            onClick={handleExport}
            disabled={isRunning || !upstream}
            title={t("canvas.exportNow") || "Export now"}
          >
            {submitIcon}
            <span>{submitLabel}</span>
          </button>
          {upstream && (upstream.type === "video" || upstream.type === "audio") && (
            <button
              type="button"
              className="export-submit-btn"
              onClick={handleOpenInEditor}
              disabled={isRunning}
              title={(t("canvas.editor.openIn") as string) || "Open in editor"}
            >
              <ExternalLink size={14} />
              <span>{(t("canvas.editor.openIn") as string) || "Editor"}</span>
            </button>
          )}
        </div>
        {d.exportedAt && (
          <div className="export-status-line">
            {t("canvas.exportedAt") || "Exported at"} {new Date(d.exportedAt).toLocaleTimeString()}
          </div>
        )}
      </div>

      <Handle type="target" position={Position.Left} className="canvas-handle" />
    </div>
  );
});
