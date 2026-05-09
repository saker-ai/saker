import { useCallback, useEffect } from "react";
import type { CanvasNodeData, CanvasNodeType, GenHistoryEntry } from "../types";
import { useCanvasStore } from "../store";
import { useHistoryStore } from "../panels/historyStore";
import { useT } from "@/features/i18n";
import { autoLayoutCanvasAfterGeneration } from "../layoutActions";
import { cacheCanvasMedia } from "../mediaCache";
import { showCanvasToast } from "../panels/CanvasToast";
import { submitAndPollTask } from "../taskPoller";

const MAX_GEN_HISTORY = 20;

function pushHistory(prev: GenHistoryEntry[] | undefined, entry: GenHistoryEntry): GenHistoryEntry[] {
  const next = [entry, ...(prev ?? [])];
  return next.slice(0, MAX_GEN_HISTORY);
}

export interface UseGenerateOptions {
  id: string;
  prompt: string;
  genCount: number;
  toolName: string;
  mediaType: "image" | "video";
  buildParams: () => Record<string, unknown> | Promise<Record<string, unknown>>;
  successToastKey: string;
  failToastKey: string;
}

/**
 * Shared generation logic for ImageGenNode and VideoGenNode.
 * Handles: submit & poll, result node creation, progress updates,
 * error handling, retry listener, and context menu.
 */
export function useGenerate(opts: UseGenerateOptions) {
  const { id, prompt, genCount, toolName, mediaType, buildParams, successToastKey, failToastKey } = opts;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const addNode = useCanvasStore((s) => s.addNode);
  const addEdge = useCanvasStore((s) => s.addEdge);

  const handleGenerate = useCallback(async () => {
    if (!prompt.trim()) return;

    updateNode(id, {
      prompt,
      generating: true,
      error: undefined,
      status: "running",
      startTime: Date.now(),
      genProgress: genCount > 1 ? `0/${genCount}` : undefined,
    } as Partial<CanvasNodeData>);

    const params = await buildParams();

    try {
      const promises = Array.from({ length: genCount }, () =>
        submitAndPollTask(toolName, params, id)
      );

      const results = await Promise.allSettled(promises);
      const thisNode = useCanvasStore.getState().nodes.find((n) => n.id === id);
      let successCount = 0;
      const createdResultNodeIds: string[] = [];
      const successMediaUrls: Array<{ url: string; path?: string }> = [];

      for (const [i, result] of results.entries()) {
        if (result.status !== "fulfilled" || !result.value.success || !result.value.structured?.media_url) {
          continue;
        }

        const mediaUrl = result.value.structured.media_url;
        const stabilized = await cacheCanvasMedia(mediaUrl, mediaType);
        const finalMediaUrl = stabilized.mediaUrl || mediaUrl;
        // Grid arrangement: 2 columns when genCount > 1 so multiple outputs
        // stay visually grouped rather than cascading straight down.
        const col = genCount > 1 ? i % 2 : 0;
        const row = genCount > 1 ? Math.floor(i / 2) : 0;
        const GRID_DX = 320;
        const GRID_DY = 200;
        const newNodeId = addNode({
          type: mediaType,
          position: {
            x: (thisNode?.position.x || 0) + 350 + col * GRID_DX,
            y: (thisNode?.position.y || 0) + row * GRID_DY,
          },
          data: {
            nodeType: mediaType as CanvasNodeType,
            label: prompt.trim().slice(0, 30),
            mediaUrl: finalMediaUrl,
            mediaPath: stabilized.mediaPath,
            sourceUrl: stabilized.sourceUrl,
            mediaType,
            status: "done",
          },
        });
        addEdge({ id: `edge_${id}_${newNodeId}`, source: id, target: newNodeId, type: "flow" });
        useHistoryStore.getState().addEntry({ type: mediaType, prompt: prompt.trim(), mediaUrl: finalMediaUrl, params });
        createdResultNodeIds.push(newNodeId);
        successMediaUrls.push({ url: finalMediaUrl, path: stabilized.mediaPath });
        successCount++;

        // Update progress indicator
        if (genCount > 1) {
          updateNode(id, { genProgress: `${successCount}/${genCount}` } as Partial<CanvasNodeData>);
        }
      }

      if (successCount > 0) {
        const prevHist = useCanvasStore.getState().nodes.find((n) => n.id === id)?.data.generationHistory;
        const primaryMedia = successMediaUrls[0];
        const historyEntry: GenHistoryEntry = {
          id: `gh_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`,
          mediaUrl: primaryMedia?.url || "",
          mediaPath: primaryMedia?.path,
          prompt: prompt.trim(),
          params,
          createdAt: Date.now(),
          status: "done",
          resultNodeIds: createdResultNodeIds,
        };
        updateNode(id, {
          generating: false,
          status: "pending",
          error: undefined,
          endTime: Date.now(),
          genProgress: undefined,
          generationHistory: pushHistory(prevHist, historyEntry),
          activeHistoryIndex: 0,
        } as Partial<CanvasNodeData>);
        showCanvasToast("success", `${t(successToastKey as any)} (${successCount})`);
        autoLayoutCanvasAfterGeneration();
      } else {
        const firstFulfilled = results.find((r) => r.status === "fulfilled") as PromiseFulfilledResult<any> | undefined;
        const firstRejected = results.find((r) => r.status === "rejected") as PromiseRejectedResult | undefined;
        const errorMsg = firstFulfilled?.value?.output || firstRejected?.reason?.message || String(firstRejected?.reason) || t("canvas.error");
        const prevHist = useCanvasStore.getState().nodes.find((n) => n.id === id)?.data.generationHistory;
        const historyEntry: GenHistoryEntry = {
          id: `gh_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`,
          mediaUrl: "",
          prompt: prompt.trim(),
          params,
          createdAt: Date.now(),
          status: "error",
          error: errorMsg,
        };
        updateNode(id, {
          generating: false,
          status: "error",
          error: errorMsg,
          endTime: Date.now(),
          genProgress: undefined,
          lastErrorParams: JSON.stringify(params),
          generationHistory: pushHistory(prevHist, historyEntry),
        } as Partial<CanvasNodeData>);
      }
    } catch (err) {
      const prevHist = useCanvasStore.getState().nodes.find((n) => n.id === id)?.data.generationHistory;
      const historyEntry: GenHistoryEntry = {
        id: `gh_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`,
        mediaUrl: "",
        prompt: prompt.trim(),
        params,
        createdAt: Date.now(),
        status: "error",
        error: String(err),
      };
      updateNode(id, {
        generating: false,
        status: "error",
        error: String(err),
        endTime: Date.now(),
        genProgress: undefined,
        lastErrorParams: JSON.stringify(params),
        generationHistory: pushHistory(prevHist, historyEntry),
      } as Partial<CanvasNodeData>);
      showCanvasToast("error", t(failToastKey as any));
    }
  }, [id, prompt, genCount, toolName, mediaType, buildParams, successToastKey, failToastKey, updateNode, addNode, addEdge, t]);

  // Listen for retry events from BulkToolbar
  useEffect(() => {
    const handler = (e: Event) => {
      if ((e as CustomEvent).detail?.nodeId === id) handleGenerate();
    };
    window.addEventListener("canvas-retry-node", handler);
    return () => window.removeEventListener("canvas-retry-node", handler);
  }, [id, handleGenerate]);

  return { handleGenerate };
}

/** Shared context menu handler for gen nodes. */
export function useGenContextMenu(id: string, mediaUrl?: string, label?: string) {
  return useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, mediaUrl, label },
      })
    );
  }, [id, mediaUrl, label]);
}
