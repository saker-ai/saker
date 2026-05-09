import { create } from "zustand";
import type { Viewport } from "@xyflow/react";
import type { CanvasNode, CanvasEdge, CanvasNodeData } from "./types";
import { sanitizeLoadedCanvas } from "./persistence";
import { autoLayoutGraph, type LayoutMode } from "./layout";

type Snapshot = { nodes: CanvasNode[]; edges: CanvasEdge[] };

const MAX_HISTORY = 50;

/** Storage key used when the canvas is opened without a thread context
 *  (infinite canvas / sandbox mode). Scoped under the standard
 *  `canvas-state-<key>` localStorage prefix. */
export const INFINITE_CANVAS_KEY = "__infinite__";

// Fix #4: nodeCounter is still module-level for simplicity but syncCounter
// is only called once during load (not on every addNode).
let nodeCounter = 0;
const nextNodeId = () => `node_${nodeCounter++}`;

const syncCounter = (nodes: CanvasNode[]) => {
  for (const n of nodes) {
    const m = /^node_(\d+)$/.exec(n.id);
    if (m) nodeCounter = Math.max(nodeCounter, Number(m[1]) + 1);
  }
};

// Fix #14: structuredClone can throw on non-cloneable data; fall back to JSON.
const cloneSnap = (nodes: CanvasNode[], edges: CanvasEdge[]): Snapshot => {
  try {
    return { nodes: structuredClone(nodes), edges: structuredClone(edges) };
  } catch {
    return JSON.parse(JSON.stringify({ nodes, edges }));
  }
};

interface CanvasState {
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  viewport: Viewport;
  history: Snapshot[];
  historyIndex: number;
  fitViewTrigger: number;
  /** Currently highlighted turn ID for canvas↔chat bidirectional linking. */
  highlightedTurnId: string | null;
  /** Node ID to branch from (set by UI, consumed by doSend). */
  pendingBranchNodeId: string | null;
  /** Current layout mode for auto-layout. */
  layoutMode: LayoutMode;
  /** True while the user is panning/zooming the canvas. */
  isUserInteracting: boolean;

  // mutations
  addNode: (node: Omit<CanvasNode, "id"> & { id?: string }) => string;
  updateNode: (id: string, data: Partial<CanvasNode["data"]>) => void;
  removeNode: (id: string) => void;
  removeNodes: (ids: string[]) => void;
  removeEdges: (ids: string[]) => void;
  addEdge: (edge: CanvasEdge) => void;
  setNodes: (nodes: CanvasNode[]) => void;
  setEdges: (edges: CanvasEdge[]) => void;
  setViewport: (viewport: Viewport) => void;
  triggerFitView: () => void;
  retryNode: (nodeId: string) => void;
  commitHistory: () => void;
  resetCanvas: () => void;
  groupNodes: (nodeIds: string[], label?: string) => string;
  setHighlightedTurnId: (turnId: string | null) => void;
  setPendingBranchNodeId: (nodeId: string | null) => void;
  setLayoutMode: (mode: LayoutMode) => void;
  setUserInteracting: (v: boolean) => void;
  /** Toggle pinned state on a node (pinned nodes skip auto-layout). */
  togglePinNode: (id: string) => void;
  /** Collapse a group node, hiding its children. */
  collapseGroup: (groupId: string) => void;
  /** Expand a collapsed group node. */
  expandGroup: (groupId: string) => void;
  /** Dissolve a group, restoring children to top-level. */
  ungroupNodes: (groupId: string) => void;
  /** Align selected nodes. */
  alignNodes: (nodeIds: string[], type: "left" | "right" | "top" | "bottom" | "center-v" | "center-h") => void;

  // undo/redo
  canUndo: () => boolean;
  canRedo: () => boolean;
  undo: () => void;
  redo: () => void;
}

function pushHistory(state: CanvasState, snap: Snapshot) {
  const history = state.history.slice(0, state.historyIndex + 1);
  history.push(snap);
  if (history.length > MAX_HISTORY) history.shift();
  return { history, historyIndex: history.length - 1 };
}

// Fix #3: Track whether the canvas was intentionally cleared so we can
// persist the empty state instead of silently skipping the save.
let intentionalClear = false;

/** Debounced fitView — consolidates multiple rapid calls into one.
 *  Skips if the user is actively panning/zooming to avoid interruption. */
let fitViewTimer: ReturnType<typeof setTimeout> | null = null;
export function deferredFitView() {
  if (fitViewTimer) clearTimeout(fitViewTimer);
  fitViewTimer = setTimeout(() => {
    if (!useCanvasStore.getState().isUserInteracting) {
      useCanvasStore.getState().triggerFitView();
    }
    fitViewTimer = null;
  }, 100);
}

/** Save current canvas state to server with exponential backoff retry. */
export async function saveToServer(
  rpc: { request: <T>(method: string, params?: Record<string, unknown>) => Promise<T> },
  threadId: string
) {
  const { nodes, edges, viewport } = useCanvasStore.getState();
  if (nodes.length === 0 && edges.length === 0 && !intentionalClear) return;
  intentionalClear = false;

  const MAX_RETRIES = 3;
  for (let attempt = 0; attempt <= MAX_RETRIES; attempt++) {
    try {
      await rpc.request("canvas/save", { threadId, nodes, edges, viewport });
      return;
    } catch (err) {
      if (attempt === MAX_RETRIES) throw err;
      await new Promise((r) => setTimeout(r, 500 * Math.pow(2, attempt)));
    }
  }
}

/** Save canvas state to localStorage for offline backup. */
export function saveToLocalStorage(threadId: string) {
  const { nodes, edges, viewport } = useCanvasStore.getState();
  try {
    localStorage.setItem(`canvas-state-${threadId}`, JSON.stringify({ nodes, edges, viewport }));
  } catch { /* quota exceeded — ignore */ }
}

/** Load canvas state from localStorage (offline fallback). */
export function loadFromLocalStorage(threadId: string): boolean {
  try {
    const saved = localStorage.getItem(`canvas-state-${threadId}`);
    if (!saved) return false;
    const { nodes, edges, viewport } = JSON.parse(saved);
    if (!nodes?.length) return false;
    syncCounter(nodes);
    useCanvasStore.setState({
      nodes,
      edges: edges || [],
      viewport: viewport || { x: 100, y: 50, zoom: 0.8 },
    });
    return true;
  } catch { return false; }
}

/** Clear canvas state without marking as intentional (for pre-load reset). */
export function clearForLoad() {
  nodeCounter = 0;
  // Do NOT set intentionalClear — this is a transient clear before loading.
  useCanvasStore.setState({
    nodes: [],
    edges: [],
    viewport: { x: 100, y: 50, zoom: 0.8 },
    history: [],
    historyIndex: -1,
  });
}

/** Load canvas state from server, replacing current state. */
export async function loadFromServer(
  rpc: { request: <T>(method: string, params?: Record<string, unknown>) => Promise<T> },
  threadId: string
) {
  // Clear intentionalClear before loading — any pending auto-save during the
  // load phase must NOT persist empty state.
  intentionalClear = false;
  const res = await rpc.request<{
    nodes?: CanvasNode[];
    edges?: CanvasEdge[];
    viewport?: { x: number; y: number; zoom: number } | null;
  }>("canvas/load", { threadId });
  const { nodes: loadedNodes, edges: loadedEdges } = sanitizeLoadedCanvas(res.nodes || [], res.edges || []);
  const existing = useCanvasStore.getState();

  // Merge: if bridge created nodes during loading, preserve them alongside loaded data.
  if (loadedNodes.length === 0) {
    // Server has nothing — keep whatever the bridge created.
    if (existing.nodes.length > 0) return;
  }

  // Deduplicate: loaded nodes take priority, add any bridge-created nodes not in loaded set.
  const loadedIds = new Set(loadedNodes.map((n) => n.id));
  const finalNodes = [...loadedNodes, ...existing.nodes.filter((n) => !loadedIds.has(n.id))];
  const loadedEdgeIdSet = new Set(loadedEdges.map((e) => e.id));
  const finalEdges = [...loadedEdges, ...existing.edges.filter((e) => !loadedEdgeIdSet.has(e.id))];

  syncCounter(finalNodes);
  useCanvasStore.setState({
    nodes: finalNodes,
    edges: finalEdges,
    viewport: res.viewport || { x: 100, y: 50, zoom: 0.8 },
    history: [],
    historyIndex: -1,
  });
  // Fit view after loading so all nodes are visible.
  deferredFitView();
}

/** Rebuild canvas from thread message history when no saved canvas exists. */
export async function rebuildFromHistory(
  rpc: { request: <T>(method: string, params?: Record<string, unknown>) => Promise<T> },
  threadId: string
) {
  type HistoryItem = { role: string; content: string; tool_name?: string; artifacts?: { type: string; url: string; name?: string }[]; created_at: string };
  const res = await rpc.request<{ items?: HistoryItem[] }>("thread/history", { threadId });
  const items = res.items || [];
  if (items.length === 0) return;

  // Group items into turns: each turn starts with a "user" item and includes
  // all subsequent tool/assistant items until the next "user".
  type Turn = { user: HistoryItem; tools: HistoryItem[]; assistant: HistoryItem | null };
  const turns: Turn[] = [];
  let current: Turn | null = null;
  for (const item of items) {
    if (item.role === "user") {
      if (current) turns.push(current);
      current = { user: item, tools: [], assistant: null };
    } else if (current) {
      if (item.role === "assistant") {
        current.assistant = item;
      } else if (item.role === "tool") {
        current.tools.push(item);
      }
    }
  }
  if (current) turns.push(current);

  const nodes: CanvasNode[] = [];
  const edges: CanvasEdge[] = [];
  let nodeIdx = 0;
  let edgeIdx = 0;
  let lastMainId: string | null = null; // tracks the main chain (prompt → agent → prompt → ...)

  const addNode = (node: CanvasNode) => { nodes.push(node); };
  const addEdge = (source: string, target: string) => {
    edges.push({ id: `edge_${edgeIdx++}`, source, target, type: "flow" });
  };

  for (const turn of turns) {
    // 1. Prompt node
    const promptTs = new Date(turn.user.created_at).getTime() || Date.now();
    const promptId = `node_${nodeIdx++}`;
    addNode({
      id: promptId,
      type: "prompt",
      position: { x: 0, y: 0 },
      data: {
        nodeType: "prompt",
        label: "Prompt",
        status: "done",
        content: turn.user.content.slice(0, 300),
        startTime: promptTs,
      },
    } as CanvasNode);
    if (lastMainId) addEdge(lastMainId, promptId);
    lastMainId = promptId;

    // 2. Agent node (from assistant response)
    if (turn.assistant) {
      const agentTs = new Date(turn.assistant.created_at).getTime() || Date.now();
      const agentId = `node_${nodeIdx++}`;
      addNode({
        id: agentId,
        type: "agent",
        position: { x: 0, y: 0 },
        data: {
          nodeType: "agent",
          label: "Thinking",
          status: "done",
          content: turn.assistant.content.slice(0, 500),
          startTime: agentTs,
          endTime: agentTs,
        },
      } as CanvasNode);
      addEdge(promptId, agentId);
      lastMainId = agentId;

      // 3. Tool/media nodes branch off from agent
      for (const toolItem of turn.tools) {
        if (!toolItem.artifacts || toolItem.artifacts.length === 0) continue;
        const toolTs = new Date(toolItem.created_at).getTime() || Date.now();
        for (const art of toolItem.artifacts) {
          const mediaType = art.type as "image" | "video" | "audio";
          if (!["image", "video", "audio"].includes(mediaType)) continue;
          const mediaId = `node_${nodeIdx++}`;
          addNode({
            id: mediaId,
            type: mediaType,
            position: { x: 0, y: 0 },
            data: {
              nodeType: mediaType,
              label: art.name || toolItem.tool_name || mediaType,
              status: "done",
              mediaType,
              mediaUrl: art.url,
              startTime: toolTs,
              endTime: toolTs,
            },
          } as CanvasNode);
          addEdge(agentId, mediaId);
        }
      }
    } else {
      // No assistant response — attach tool nodes directly to prompt
      for (const toolItem of turn.tools) {
        if (!toolItem.artifacts || toolItem.artifacts.length === 0) continue;
        const toolTs = new Date(toolItem.created_at).getTime() || Date.now();
        for (const art of toolItem.artifacts) {
          const mediaType = art.type as "image" | "video" | "audio";
          if (!["image", "video", "audio"].includes(mediaType)) continue;
          const mediaId = `node_${nodeIdx++}`;
          addNode({
            id: mediaId,
            type: mediaType,
            position: { x: 0, y: 0 },
            data: {
              nodeType: mediaType,
              label: art.name || toolItem.tool_name || mediaType,
              status: "done",
              mediaType,
              mediaUrl: art.url,
              startTime: toolTs,
              endTime: toolTs,
            },
          } as CanvasNode);
          addEdge(promptId, mediaId);
        }
      }
    }
  }

  if (nodes.length === 0) return;

  const laidOut = autoLayoutGraph(nodes, edges);
  syncCounter(laidOut);
  useCanvasStore.setState({
    nodes: laidOut,
    edges,
    viewport: { x: 100, y: 50, zoom: 0.8 },
    history: [],
    historyIndex: -1,
  });
  deferredFitView();

  // Persist the rebuilt canvas.
  await rpc.request("canvas/save", {
    threadId,
    nodes: laidOut,
    edges,
    viewport: { x: 100, y: 50, zoom: 0.8 },
  });
}

export const useCanvasStore = create<CanvasState>((set, get) => ({
  nodes: [],
  edges: [],
  viewport: { x: 100, y: 50, zoom: 0.8 },
  history: [],
  historyIndex: -1,
  fitViewTrigger: 0,
  highlightedTurnId: null,
  pendingBranchNodeId: null,
  layoutMode: "auto" as LayoutMode,
  isUserInteracting: false,

  addNode: (spec) => {
    const id = spec.id || nextNodeId();
    const node: CanvasNode = {
      ...spec,
      id,
      data: { ...spec.data },
    } as CanvasNode;
    set((s) => ({ nodes: [...s.nodes, node] }));
    return id;
  },

  updateNode: (id, data) => {
    set((s) => ({
      nodes: s.nodes.map((n) =>
        n.id === id ? { ...n, data: { ...n.data, ...data } } : n
      ),
    }));
  },

  removeNode: (id) => {
    set((s) => {
      const target = s.nodes.find((n) => n.id === id);
      if (target?.data?.locked) return s;
      return {
        nodes: s.nodes.filter((n) => n.id !== id),
        edges: s.edges.filter((e) => e.source !== id && e.target !== id),
      };
    });
  },

  removeNodes: (ids) => {
    const idSet = new Set(ids);
    set((s) => {
      const deletable = new Set(
        s.nodes.filter((n) => idSet.has(n.id) && !n.data?.locked).map((n) => n.id)
      );
      if (deletable.size === 0) return s;
      return {
        nodes: s.nodes.filter((n) => !deletable.has(n.id)),
        edges: s.edges.filter((e) => !deletable.has(e.source) && !deletable.has(e.target)),
      };
    });
  },

  removeEdges: (ids) => {
    const idSet = new Set(ids);
    set((s) => ({
      edges: s.edges.filter((e) => !idSet.has(e.id)),
    }));
  },

  addEdge: (edge) => {
    set((s) => {
      if (s.edges.some((e) => e.id === edge.id)) return s;
      return { edges: [...s.edges, edge] };
    });
  },

  setNodes: (nodes) => {
    // syncCounter removed from hot path — only called during external data load.
    set({ nodes });
  },
  setEdges: (edges) => set({ edges }),
  setViewport: (viewport) => set({ viewport }),

  triggerFitView: () => set((s) => ({ fitViewTrigger: s.fitViewTrigger + 1 })),

  /** Retry generation for a node by re-dispatching its generate action. */
  retryNode: (nodeId: string) => {
    const node = get().nodes.find((n) => n.id === nodeId);
    if (!node || node.data.status !== "error") return;
    // Dispatch a custom event that the gen node listens for
    window.dispatchEvent(new CustomEvent("canvas-retry-node", { detail: { nodeId } }));
  },

  commitHistory: () => {
    set((s) => {
      const snap = cloneSnap(s.nodes, s.edges);
      // If history is empty, just set the initial snapshot (no double-push).
      if (s.history.length === 0) {
        return { history: [snap], historyIndex: 0 };
      }
      return pushHistory(s, snap);
    });
  },

  resetCanvas: () => {
    nodeCounter = 0;
    intentionalClear = true;
    // Clear stale localStorage entries for the current thread (or infinite canvas).
    try {
      const hash = typeof window !== "undefined" ? window.location.hash : "";
      const m = /[#/](?:canvas|chats)\/([a-f0-9-]+)/i.exec(hash);
      const key = m ? m[1] : INFINITE_CANVAS_KEY;
      localStorage.removeItem(`canvas-state-${key}`);
    } catch { /* ignore */ }
    set({
      nodes: [],
      edges: [],
      viewport: { x: 100, y: 50, zoom: 0.8 },
      history: [],
      historyIndex: -1,
    });
  },

  groupNodes: (nodeIds, label) => {
    if (nodeIds.length < 2) return "";
    const s = get();
    // Nested group guard: ungroup children that already belong to another group.
    const alreadyGrouped = s.nodes.filter((n) => nodeIds.includes(n.id) && n.parentId);
    if (alreadyGrouped.length > 0) {
      const parentIds = new Set(alreadyGrouped.map((n) => n.parentId!));
      // Dissolve affected groups first to avoid nested grouping.
      for (const pid of parentIds) {
        get().ungroupNodes(pid);
      }
    }
    const s2 = get(); // re-read after potential ungroup
    const targets = s2.nodes.filter((n) => nodeIds.includes(n.id));
    if (targets.length < 2) return "";

    // Compute bounding box of selected nodes.
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const n of targets) {
      const w = n.measured?.width || 280;
      const h = n.measured?.height || 160;
      minX = Math.min(minX, n.position.x);
      minY = Math.min(minY, n.position.y);
      maxX = Math.max(maxX, n.position.x + w);
      maxY = Math.max(maxY, n.position.y + h);
    }

    syncCounter(s.nodes);
    const groupId = nextNodeId();
    const padding = 20;
    const headerH = 40;
    const groupNode: CanvasNode = {
      id: groupId,
      type: "group",
      position: { x: minX - padding, y: minY - headerH },
      data: {
        nodeType: "group",
        label: label || `Group (${targets.length})`,
        status: "done",
      },
      style: { width: maxX - minX + padding * 2, height: maxY - minY + headerH + padding },
    } as CanvasNode;

    // Fix #8: Set parentId on children so React Flow treats them as grouped.
    const groupedChildren = get().nodes.map((n) =>
      nodeIds.includes(n.id)
        ? {
            ...n,
            parentId: groupId,
            extent: "parent" as const,
            position: { x: n.position.x - (minX - padding), y: n.position.y - (minY - headerH) },
          }
        : n
    );

    set({ nodes: [groupNode, ...groupedChildren] });
    return groupId;
  },

  setHighlightedTurnId: (turnId) => set({ highlightedTurnId: turnId }),
  setPendingBranchNodeId: (nodeId) => set({ pendingBranchNodeId: nodeId }),
  setLayoutMode: (mode) => set({ layoutMode: mode }),
  setUserInteracting: (v) => set({ isUserInteracting: v }),
  togglePinNode: (id) => {
    set((s) => ({
      nodes: s.nodes.map((n) =>
        n.id === id ? { ...n, data: { ...n.data, pinned: !n.data.pinned } } : n
      ),
    }));
  },

  collapseGroup: (groupId) => {
    set((s) => ({
      nodes: s.nodes.map((n) => {
        if (n.id === groupId) return { ...n, data: { ...n.data, collapsed: true } };
        if (n.parentId === groupId) return { ...n, hidden: true };
        return n;
      }),
    }));
  },

  expandGroup: (groupId) => {
    set((s) => ({
      nodes: s.nodes.map((n) => {
        if (n.id === groupId) return { ...n, data: { ...n.data, collapsed: false } };
        if (n.parentId === groupId) return { ...n, hidden: false };
        return n;
      }),
    }));
  },

  ungroupNodes: (groupId) => {
    const s = get();
    const group = s.nodes.find((n) => n.id === groupId);
    if (!group) return;
    const gx = group.position.x;
    const gy = group.position.y;
    set({
      nodes: s.nodes
        .filter((n) => n.id !== groupId)
        .map((n) =>
          n.parentId === groupId
            ? { ...n, parentId: undefined, extent: undefined, hidden: false, position: { x: n.position.x + gx, y: n.position.y + gy } }
            : n
        ),
      edges: s.edges.filter((e) => e.source !== groupId && e.target !== groupId),
    });
  },

  alignNodes: (nodeIds, type) => {
    const s = get();
    const targets = s.nodes.filter((n) => nodeIds.includes(n.id));
    if (targets.length < 2) return;

    let minX = Math.min(...targets.map((n) => n.position.x));
    let minY = Math.min(...targets.map((n) => n.position.y));
    let maxX = Math.max(...targets.map((n) => n.position.x + (n.measured?.width || 280)));
    let maxY = Math.max(...targets.map((n) => n.position.y + (n.measured?.height || 160)));

    const updatedNodes = s.nodes.map((n) => {
      if (!nodeIds.includes(n.id)) return n;
      const w = n.measured?.width || 280;
      const h = n.measured?.height || 160;
      const pos = { ...n.position };

      if (type === "left") pos.x = minX;
      if (type === "right") pos.x = maxX - w;
      if (type === "top") pos.y = minY;
      if (type === "bottom") pos.y = maxY - h;
      if (type === "center-h") pos.x = (minX + maxX) / 2 - w / 2;
      if (type === "center-v") pos.y = (minY + maxY) / 2 - h / 2;

      return { ...n, position: pos };
    });

    set({ nodes: updatedNodes });
  },

  canUndo: () => get().historyIndex > 0,
  canRedo: () => get().historyIndex < get().history.length - 1,

  undo: () => {
    const s = get();
    if (s.historyIndex <= 0) return;
    const i = s.historyIndex - 1;
    const snap = s.history[i];
    set({ nodes: snap.nodes, edges: snap.edges, historyIndex: i });
  },

  redo: () => {
    const s = get();
    if (s.historyIndex >= s.history.length - 1) return;
    const i = s.historyIndex + 1;
    const snap = s.history[i];
    set({ nodes: snap.nodes, edges: snap.edges, historyIndex: i });
  },
}));
