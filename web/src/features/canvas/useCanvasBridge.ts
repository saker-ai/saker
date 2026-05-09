import { useEffect, useRef, useCallback } from "react";
import type { RPCClient } from "@/features/rpc/client";
import type { StreamEvent } from "@/features/rpc/types";
import { SessionCanvasBridge } from "./SessionCanvasBridge";
import { useCanvasStore, saveToServer, loadFromServer, clearForLoad, rebuildFromHistory, saveToLocalStorage, loadFromLocalStorage, INFINITE_CANVAS_KEY } from "./store";

/**
 * React hook that bridges RPC stream events to canvas node operations.
 * When enabled, listens for stream/event and turn/finished notifications.
 * Automatically loads canvas data when threadId changes and saves after turns.
 */
export function useCanvasBridge(
  rpc: RPCClient | null,
  enabled: boolean,
  threadId?: string
) {
  const bridgeRef = useRef<SessionCanvasBridge | null>(null);
  const prevThreadRef = useRef<string | undefined>(undefined);
  const offlineRef = useRef(false);

  if (!bridgeRef.current) {
    bridgeRef.current = new SessionCanvasBridge();
  }

  const addPrompt = useCallback(
    (text: string, parentNodeId?: string) => {
      bridgeRef.current?.addPrompt(text, parentNodeId);
    },
    []
  );

  const setTurnId = useCallback(
    (turnId: string) => {
      bridgeRef.current?.setCurrentTurnId(turnId);
    },
    []
  );

  const resetCanvas = useCallback(() => {
    bridgeRef.current?.hardReset();
    useCanvasStore.getState().resetCanvas();
  }, []);

  // Auto load canvas data when threadId changes.
  useEffect(() => {
    if (!enabled) return;
    const current = threadId || INFINITE_CANVAS_KEY;
    if (prevThreadRef.current === current) return;
    prevThreadRef.current = current;

    bridgeRef.current?.reset();
    clearForLoad();
    if (bridgeRef.current) bridgeRef.current.loading = true;

    // Thread-less infinite canvas: localStorage-only persistence.
    if (!threadId) {
      try {
        loadFromLocalStorage(INFINITE_CANVAS_KEY);
        bridgeRef.current?.restoreLastNode();
      } catch { /* ignore */ }
      if (bridgeRef.current) bridgeRef.current.loading = false;
      return;
    }

    if (!rpc) return;
    loadFromServer(rpc, threadId)
      .then(async () => {
        bridgeRef.current?.restoreLastNode();
        if (useCanvasStore.getState().nodes.length === 0) {
          await rebuildFromHistory(rpc, threadId);
          bridgeRef.current?.restoreLastNode();
        }
      })
      .catch((err) => {
        console.warn("canvas: loadFromServer failed, falling back to localStorage", err);
        try {
          loadFromLocalStorage(threadId);
          bridgeRef.current?.restoreLastNode();
        } catch { /* localStorage also unavailable */ }
      })
      .finally(() => {
        if (bridgeRef.current) bridgeRef.current.loading = false;
      });
  }, [rpc, enabled, threadId]);

  // Listen for stream events and auto-save on turn finish.
  useEffect(() => {
    if (!rpc || !enabled) return;

    const bridge = bridgeRef.current!;

    const unsubStream = rpc.on("stream/event", (params: unknown) => {
      bridge.processEvent(params as StreamEvent);
    });

    const unsubFinish = rpc.on("turn/finished", () => {
      bridge.finalize();
      if (threadId) {
        saveToServer(rpc, threadId).catch(() => {
          // Offline fallback: save to localStorage if server save fails.
          saveToLocalStorage(threadId);
        });
      }
    });

    return () => {
      unsubStream();
      unsubFinish();
    };
  }, [rpc, enabled, threadId]);

  // Auto-save canvas on node/edge changes (debounced).
  useEffect(() => {
    if (!enabled) return;
    let timer: ReturnType<typeof setTimeout>;
    let prevNodes = useCanvasStore.getState().nodes;
    let prevEdges = useCanvasStore.getState().edges;
    const unsub = useCanvasStore.subscribe((state) => {
      if (state.nodes === prevNodes && state.edges === prevEdges) return;
      prevNodes = state.nodes;
      prevEdges = state.edges;
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (bridgeRef.current?.loading) return;
        if (threadId && rpc) {
          saveToServer(rpc, threadId).catch(() => {
            saveToLocalStorage(threadId);
          });
        } else {
          saveToLocalStorage(INFINITE_CANVAS_KEY);
        }
      }, 1500);
    });
    return () => {
      clearTimeout(timer);
      unsub();
    };
  }, [rpc, enabled, threadId]);

  // Offline detection: listen for RPC disconnect/reconnect events.
  useEffect(() => {
    if (!rpc) return;

    const unsubDisconnect = rpc.on("_disconnected", () => {
      offlineRef.current = true;
    });

    const unsubConnect = rpc.on("_connected", () => {
      if (offlineRef.current && threadId) {
        offlineRef.current = false;
        // Sync local state to server on reconnect.
        saveToServer(rpc, threadId).catch((err) => {
          console.warn("canvas: reconnect save failed", err);
        });
      }
    });

    return () => {
      unsubDisconnect();
      unsubConnect();
    };
  }, [rpc, threadId]);

  // Multi-tab sync via BroadcastChannel.
  useEffect(() => {
    if (!threadId || typeof BroadcastChannel === "undefined") return;

    const channel = new BroadcastChannel(`canvas-sync-${threadId}`);
    let ignoreUntil = 0;

    // Broadcast local changes to other tabs.
    const unsub = useCanvasStore.subscribe((state) => {
      if (Date.now() < ignoreUntil) return;
      if (bridgeRef.current?.loading) return;
      try {
        channel.postMessage({
          type: "canvas-update",
          nodes: state.nodes,
          edges: state.edges,
          viewport: state.viewport,
        });
      } catch { /* serialization error — ignore */ }
    });

    // Receive changes from other tabs.
    channel.onmessage = (event) => {
      if (event.data?.type !== "canvas-update") return;
      ignoreUntil = Date.now() + 50;
      useCanvasStore.setState({
        nodes: event.data.nodes,
        edges: event.data.edges,
        viewport: event.data.viewport,
      });
    };

    return () => {
      unsub();
      channel.close();
    };
  }, [threadId]);

  return { addPrompt, resetCanvas, setTurnId };
}
