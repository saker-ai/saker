import { memo, useCallback, useMemo, useState } from "react";
import { Handle, Position, type NodeProps, useNodeConnections } from "@xyflow/react";
import { Scissors, Loader2, CheckCircle2, Download, ExternalLink } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useT } from "@/features/i18n";
import { useCanvasStore } from "../store";
import { useShallow } from "zustand/react/shallow";
import { showCanvasToast } from "../panels/CanvasToast";
import {
  composeAndDownload,
  ComposeCorsError,
  ComposeUnsupportedError,
  isComposeSupported,
  type ComposeInput,
} from "@/features/editor-runtime";
import { buildEditorImportUrl, type EditorAsset } from "@/features/editor-bridge";

interface ConnectedInput {
  id: string;
  label: string;
  mediaUrl?: string;
  mediaType?: string;
}

export const CompositionNode = memo(function CompositionNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const isRunning = d.status === "running";
  const isDone = d.status === "done";
  const isHighlighted = useCanvasStore((s) => s.highlightedTurnId != null && s.highlightedTurnId === d.turnId);

  // Find all video inputs connected to this node via connections
  const connections = useNodeConnections({ handleType: "target" });
  const sourceIds = useMemo(() => connections.map((c) => c.source), [connections]);

  // Subscribe to label + media fields of each connected upstream node, shallow-equal for performance
  const inputs = useCanvasStore(
    useShallow((s) =>
      sourceIds.map<ConnectedInput>((sid) => {
        const node = s.nodes.find((n) => n.id === sid);
        const nd = node?.data as CanvasNodeData | undefined;
        return {
          id: sid,
          label: nd?.label || "Clip",
          mediaUrl: typeof nd?.mediaUrl === "string" ? nd.mediaUrl : undefined,
          mediaType: typeof nd?.mediaType === "string" ? nd.mediaType : undefined,
        };
      }),
    ),
  );

  const composeInputs = useMemo<ComposeInput[]>(
    () =>
      inputs
        .filter((it) => !!it.mediaUrl)
        .map((it) => ({
          url: it.mediaUrl as string,
          label: it.label,
          type: (it.mediaType === "audio" || it.mediaType === "image" ? it.mediaType : "video") as
            | "video"
            | "audio"
            | "image",
        })),
    [inputs],
  );
  // Back-compat alias used by buttons that still reference videoInputs.
  const videoInputs = composeInputs;

  const [composeStatus, setComposeStatus] = useState<"idle" | "running" | "done" | "error">("idle");
  const [composePct, setComposePct] = useState(0);

  const supported = useMemo(() => (typeof window === "undefined" ? true : isComposeSupported()), []);

  const handleLocalCompose = useCallback(async () => {
    if (videoInputs.length === 0) {
      showCanvasToast("error", t("canvas.compose.noVideoInputs") || "Connect video inputs first");
      return;
    }
    if (!supported) {
      showCanvasToast(
        "error",
        t("canvas.compose.unsupported") || "Browser unsupported — try Chrome/Edge 113+",
      );
      return;
    }
    setComposeStatus("running");
    setComposePct(0);
    try {
      const safeName = (d.label || "composition").replace(/[^\w\-]+/g, "_").slice(0, 60) || "composition";
      await composeAndDownload(videoInputs, safeName, {
        onProgress: (p) => setComposePct(Math.max(0, Math.min(1, p))),
      });
      setComposeStatus("done");
      showCanvasToast("success", t("canvas.compose.done") || "Saved");
      setTimeout(() => setComposeStatus("idle"), 2500);
    } catch (err) {
      setComposeStatus("error");
      let msg = t("canvas.compose.failed") || "Compose failed";
      if (err instanceof ComposeUnsupportedError) {
        msg = t("canvas.compose.unsupported") || msg;
      } else if (err instanceof ComposeCorsError) {
        msg = t("canvas.compose.corsHint") || msg;
      } else if (err instanceof Error) {
        msg = `${msg}: ${err.message}`;
      }
      showCanvasToast("error", msg);
      setTimeout(() => setComposeStatus("idle"), 3000);
    }
  }, [videoInputs, supported, d.label, t]);

  const handleOpenInEditor = useCallback(() => {
    if (inputs.length === 0) {
      showCanvasToast("error", t("canvas.editor.noInputs") || "Connect media inputs first");
      return;
    }
    const editorAssets: EditorAsset[] = inputs
      .filter((it) => !!it.mediaUrl)
      .map((it) => ({
        url: it.mediaUrl as string,
        type: ((it.mediaType === "audio" || it.mediaType === "image") ? it.mediaType : "video") as EditorAsset["type"],
        label: it.label,
      }));
    if (editorAssets.length === 0) {
      showCanvasToast("error", t("canvas.editor.noInputs") || "Connect media inputs first");
      return;
    }
    const url = buildEditorImportUrl({ assets: editorAssets, originNodeId: id });
    // Intentionally NOT noopener: the editor uses window.opener.postMessage
    // to ship the export back. Same-origin only, so this is safe.
    window.open(url, "_blank");
  }, [inputs, id, t]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, label: d.label },
      })
    );
  }, [id, d.label]);

  const composeBtnLabel = (() => {
    if (composeStatus === "running") {
      const pct = Math.round(composePct * 100);
      const tpl = t("canvas.compose.running") as unknown as string;
      if (typeof tpl === "string" && tpl.includes("{pct}")) return tpl.replace("{pct}", String(pct));
      return `${pct}%`;
    }
    if (composeStatus === "done") return t("canvas.compose.done") || "Saved";
    return t("canvas.compose.local") || "Compose mp4 in browser";
  })();

  const composeBtnDisabled = composeStatus === "running" || videoInputs.length === 0;

  return (
    <div
      className={`canvas-node canvas-node-composition ${isRunning ? "running" : ""} ${isDone ? "done" : ""} ${selected ? "selected" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
      aria-label={d.label || "Composition"}
    >
      <NodeToolbar nodeId={id} selected={selected} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          {isRunning ? (
            <Loader2 className="animate-spin text-accent" size={16} />
          ) : isDone ? (
            <CheckCircle2 className="text-success" size={16} />
          ) : (
            <Scissors size={16} />
          )}
        </div>
        <span className="canvas-node-label">{d.label || "Video Composition"}</span>
        <span className="canvas-node-beta-tag">Beta</span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>

      <div className="canvas-node-body">
        {inputs.length > 0 ? (
          <div className="canvas-composition-inputs">
            {inputs.map((item, i) => (
              <div key={item.id} className="canvas-composition-segment">
                <span className="canvas-composition-index">{i + 1}</span>
                <span className="canvas-composition-name">
                  {item.label || `Clip ${i + 1}`}
                </span>
                {item.mediaType && item.mediaType !== "video" && (
                  <span className="canvas-composition-skip" title={item.mediaType}>
                    {item.mediaType}
                  </span>
                )}
              </div>
            ))}
          </div>
        ) : (
          <div className="canvas-node-placeholder">
            {t("canvas.connectToCompose")}
          </div>
        )}

        <div className="export-toolbar nodrag">
          {supported ? (
            <button
              type="button"
              className="export-submit-btn"
              onClick={handleLocalCompose}
              disabled={composeBtnDisabled}
              title={
                videoInputs.length === 0
                  ? (t("canvas.compose.noVideoInputs") as string) || "Connect video inputs"
                  : (t("canvas.compose.local") as string) || "Compose locally"
              }
            >
              {composeStatus === "running" ? (
                <Loader2 size={14} className="animate-spin" />
              ) : composeStatus === "done" ? (
                <CheckCircle2 size={14} />
              ) : (
                <Download size={14} />
              )}
              <span>{composeBtnLabel}</span>
            </button>
          ) : (
            <span
              className="canvas-node-placeholder"
              style={{ fontSize: "11px", padding: "4px 6px" }}
              title={(t("canvas.compose.unsupported") as string) || "Browser unsupported"}
            >
              {(t("canvas.compose.unsupported") as string) || "Browser unsupported"}
            </span>
          )}
          <button
            type="button"
            className="export-submit-btn"
            onClick={handleOpenInEditor}
            disabled={inputs.length === 0}
            title={(t("canvas.editor.openIn") as string) || "Open in editor"}
          >
            <ExternalLink size={14} />
            <span>{(t("canvas.editor.openIn") as string) || "Editor"}</span>
          </button>
        </div>
      </div>

      {d.mediaUrl && (
        <div className="canvas-node-body media">
          <video src={d.mediaUrl} controls preload="metadata" />
        </div>
      )}

      {d.content && (
        <div className="canvas-node-footer">
          <span className="time-badge">{d.content}</span>
        </div>
      )}

      <Handle type="target" position={Position.Left} className="canvas-handle" id="in" />
      <Handle type="source" position={Position.Right} className="canvas-handle" id="out" />
    </div>
  );
});
