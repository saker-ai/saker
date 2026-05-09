import type { StreamEvent } from "@/features/rpc/types";
import type { CanvasNodeData, CanvasNodeType, MediaType } from "./types";
import { extractMedia } from "@/features/media/extractMedia";
import { useCanvasStore, deferredFitView } from "./store";
import { autoLayoutGraph } from "./layout";
import { cacheCanvasMedia } from "./mediaCache";

/**
 * Tools that always show on canvas (media/creative tools).
 * All other tools are hidden unless they produce media output.
 */
const ALWAYS_SHOW_TOOLS = new Set([
  // aigo tooldef names (lowercased for matching)
  "generate_image",
  "edit_image",
  "generate_video",
  "edit_video",
  "text_to_speech",
  "design_voice",
  "transcribe_audio",
  // Note: imageread is intentionally excluded — its media output is detected
  // via extractMedia and shown as a standalone Image node instead.
]);

/** Tool input keys that contain media reference URLs. */
const MEDIA_REF_KEYS = ["reference_image", "image_url", "video_url", "reference_video"];

/** Horizontal/vertical spacing for incremental placement. */
const H_GAP = 320;
const V_GAP = 160;

/** Max tracked media URLs before eviction (prevents unbounded growth). */
const MAX_SEEN_MEDIA = 500;

/** Maps stream events to canvas node operations with smart filtering. */
export class SessionCanvasBridge {
  private agentNodeId: string | null = null;
  private turnActive = false;
  private toolNodeMap = new Map<string, string>(); // tool_use_id → canvas node id
  private edgeCounter = 0;
  /** Accumulated full content for the current agent node (avoids stale-store reads). */
  private agentContent = "";
  private deltaTimer: ReturnType<typeof setTimeout> | null = null;
  /** Track the last placed node for incremental positioning. */
  private lastNodeId: string | null = null;
  private childCountAtParent = new Map<string, number>(); // parentId → child count
  /** Track media URLs already shown on canvas to avoid duplicates. */
  private seenMediaUrls = new Set<string>();
  private toolSeqCounter = 0;
  /** Temporarily holds tool input for the next createToolNode call. */
  private pendingToolInput?: Record<string, unknown>;
  /** Cached node Map for O(1) lookups in nextPosition. */
  private nodeMap?: Map<string, import("./types").CanvasNode>;
  private nodeMapGeneration = -1;
  /** Current turn ID for bidirectional canvas↔chat linking. */
  private currentTurnId: string | null = null;
  /** Node IDs created in the current turn (for auto-grouping). */
  private turnNodeIds: string[] = [];
  /** Debounce timer for incremental auto-layout during streaming. */
  private layoutTimer: ReturnType<typeof setTimeout> | null = null;
  private static readonly LAYOUT_DEBOUNCE_MS = 500;
  /** True while loadFromServer is in progress; events are queued. */
  private _loading = false;
  private _eventQueue: StreamEvent[] = [];

  get loading() { return this._loading; }
  set loading(v: boolean) {
    this._loading = v;
    if (!v && this._eventQueue.length > 0) {
      const queued = this._eventQueue.splice(0);
      for (const evt of queued) this.processEvent(evt);
    }
  }

  reset() {
    this.agentNodeId = null;
    this.turnActive = false;
    this.toolNodeMap.clear();
    // Don't reset edgeCounter — prevents ID collision with persisted edges.
    this.agentContent = "";
    if (this.deltaTimer) clearTimeout(this.deltaTimer);
    this.deltaTimer = null;
    if (this.layoutTimer) clearTimeout(this.layoutTimer);
    this.layoutTimer = null;
    this.lastNodeId = null;
    this.childCountAtParent.clear();
    this.toolSeqCounter = 0;
    this.nodeMap = undefined;
    this.nodeMapGeneration = -1;
    this._loading = false;
    this._eventQueue = [];
    this.currentTurnId = null;
    this.turnNodeIds = [];
    // Cap seenMediaUrls instead of clearing — prevents duplicates across turns
    // while bounding memory. Full clear only on hard reset (thread switch).
  }

  /** Full reset including media tracking and edge counter (thread switch). */
  hardReset() {
    this.reset();
    this.edgeCounter = 0;
    this.seenMediaUrls.clear();
  }

  /** Creates the root prompt node, linking to the previous turn if any.
   *  @param parentNodeId — optional: branch from this node instead of lastNodeId
   */
  addPrompt(text: string, parentNodeId?: string): string {
    const store = useCanvasStore.getState();

    // Self-heal: if lastNodeId was lost (e.g. bridge recreated, HMR, reconnect)
    // but nodes already exist on the canvas, restore it from the latest node.
    if (!this.lastNodeId && store.nodes.length > 0) {
      this.restoreLastNode();
    }

    // Branch support: if parentNodeId is specified, use it instead of lastNodeId.
    const prevLastNode = parentNodeId || this.lastNodeId;
    const id = store.addNode({
      type: "prompt",
      position: { x: 0, y: 0 },
      data: {
        nodeType: "prompt" as CanvasNodeType,
        label: "Prompt",
        status: "done",
        content: text,
        startTime: Date.now(),
        turnId: this.currentTurnId || undefined,
      },
    });
    // Connect to the previous turn's last node for cross-turn continuity.
    if (prevLastNode) {
      this.addEdge(prevLastNode, id);
    }
    this.lastNodeId = id;
    this.turnNodeIds = [id];
    return id;
  }

  /** Set the current turn ID (called after turn/send returns the turnId). */
  setCurrentTurnId(turnId: string) {
    this.currentTurnId = turnId;
    // Retroactively update nodes created before we knew the turnId.
    const store = useCanvasStore.getState();
    for (const nodeId of this.turnNodeIds) {
      store.updateNode(nodeId, { turnId });
    }
  }

  /** Get current turn's node IDs (for auto-grouping). */
  getTurnNodeIds(): string[] {
    return [...this.turnNodeIds];
  }

  /** Process a stream event and update the canvas accordingly. */
  processEvent(evt: StreamEvent) {
    // Queue events while loading saved canvas data to prevent overwrites.
    if (this._loading) {
      this._eventQueue.push(evt);
      return;
    }

    switch (evt.type) {
      case "skill_activation": {
        const store = useCanvasStore.getState();
        const name = evt.name || "Skill";
        const output = evt.output as Record<string, unknown> | undefined;
        const desc = (output?.description as string) || "";
        const pos = this.nextPosition(this.lastNodeId);
        const id = store.addNode({
          type: "skill",
          position: pos,
          data: {
            nodeType: "skill" as CanvasNodeType,
            label: name,
            status: "done",
            content: desc,
            startTime: Date.now(),
            turnId: this.currentTurnId || undefined,
          },
        });
        if (this.lastNodeId) {
          this.addEdge(this.lastNodeId, id);
        }
        this.lastNodeId = id;
        this.turnNodeIds.push(id);
        this.scheduleLayout();
        break;
      }

      case "content_block_start": {
        // One agent node per turn.
        if (!this.agentNodeId || !this.turnActive) {
          const store = useCanvasStore.getState();
          const pos = this.nextPosition(this.lastNodeId);
          const id = store.addNode({
            type: "agent",
            position: pos,
            data: {
              nodeType: "agent" as CanvasNodeType,
              label: "Thinking",
              status: "running",
              content: "",
              startTime: Date.now(),
              turnId: this.currentTurnId || undefined,
            },
          });
          this.agentNodeId = id;
          this.agentContent = "";
          this.turnActive = true;
          this.turnNodeIds.push(id);

          if (this.lastNodeId) {
            this.addEdge(this.lastNodeId, id);
          }
          this.lastNodeId = id;
          this.scheduleLayout();
        }
        break;
      }

      case "content_block_delta": {
        // Fix #1: Accumulate delta text in the bridge instead of reading
        // from the store, avoiding stale-store race with scheduleLayout.
        if (!this.agentNodeId) break;
        const text = evt.delta?.text ?? "";
        this.agentContent += text;
        if (!this.deltaTimer) {
          this.deltaTimer = setTimeout(() => {
            if (this.agentNodeId) {
              useCanvasStore.getState().updateNode(this.agentNodeId, {
                content: this.agentContent,
              });
            }
            this.deltaTimer = null;
          }, 100);
        }
        break;
      }

      case "content_block_stop": {
        // Fix #5: Only flush remaining delta; don't mark "done" here.
        // A turn may have multiple content blocks + tool calls; finalize()
        // sets the final status when the turn truly ends.
        if (this.deltaTimer) {
          clearTimeout(this.deltaTimer);
          this.deltaTimer = null;
        }
        if (this.agentNodeId) {
          useCanvasStore.getState().updateNode(this.agentNodeId, {
            content: this.agentContent,
          });
        }
        break;
      }

      case "tool_execution_start": {
        const toolId = evt.tool_use_id || `tool_${++this.toolSeqCounter}_${Date.now()}`;
        const name = evt.name || "";
        const nameLower = name.toLowerCase();

        if (ALWAYS_SHOW_TOOLS.has(nameLower)) {
          this.pendingToolInput = evt.input;
          const nodeId = this.createToolNode(toolId, name);
          this.linkMediaReferences(nodeId, evt.input);
          this.scheduleLayout();
        } else {
          this.pendingToolInput = undefined;
        }
        break;
      }

      case "tool_execution_output": {
        const toolId = evt.tool_use_id || "";
        const nodeId = this.toolNodeMap.get(toolId);
        if (nodeId && evt.output != null) {
          const content =
            typeof evt.output === "string"
              ? evt.output
              : JSON.stringify(evt.output, null, 2);
          useCanvasStore.getState().updateNode(nodeId, { content: content.slice(0, 500) });
        }
        break;
      }

      case "tool_execution_result": {
        // Fix #6: Removed early `break` on is_error — error tool nodes must
        // still be updated to "error" status instead of staying "running".
        const isError = evt.is_error === true;
        const toolId = evt.tool_use_id || "";
        const raw = isError ? null : extractMedia(evt) as { type: MediaType; url: string } | null;
        // Skip empty URLs and duplicate URLs (same image served from different events).
        const media = raw && raw.url && !this.seenMediaUrls.has(raw.url)
          ? raw : null;
        if (media) {
          this.seenMediaUrls.add(media.url);
          // Fix #13: Evict oldest entries when set grows too large.
          if (this.seenMediaUrls.size > MAX_SEEN_MEDIA) {
            const first = this.seenMediaUrls.values().next().value;
            if (first) this.seenMediaUrls.delete(first);
          }
        }

        const nodeId = this.toolNodeMap.get(toolId);

        if (nodeId) {
          if (media && !isError) {
            // Upgrade tool node to a full media node so users get
            // playback, editing, frame capture, etc.
            const mediaNodeType = (media.type === "video" ? "video"
              : media.type === "audio" ? "audio" : "image") as CanvasNodeType;
            const currentNodes = useCanvasStore.getState().nodes;
            const upgraded = currentNodes.map((n) =>
              n.id === nodeId
                ? {
                    ...n,
                    type: mediaNodeType as string,
                    data: {
                      ...n.data,
                      nodeType: mediaNodeType,
                      mediaType: media.type,
                      mediaUrl: media.url,
                      status: "done" as const,
                      endTime: Date.now(),
                    },
                  }
                : n
            );
            useCanvasStore.getState().setNodes(upgraded as typeof currentNodes);
            void this.stabilizeMediaNode(nodeId, media.type, media.url);
          } else {
            useCanvasStore.getState().updateNode(nodeId, {
              status: isError ? "error" : "done",
              endTime: Date.now(),
            });
          }
        } else if (media) {
          // Tool not in ALWAYS_SHOW_TOOLS but produced media — create a media node.
          const store = useCanvasStore.getState();
          const nodeType = media.type === "video" ? "video" : media.type === "audio" ? "audio" : "image";
          const mediaLabel = media.type === "video" ? "Video" : media.type === "audio" ? "Audio" : "Image";
          const parentId = this.agentNodeId || this.lastNodeId;
          const pos = this.nextPosition(parentId);
          const id = store.addNode({
            type: nodeType,
            position: pos,
            data: {
              nodeType: nodeType as CanvasNodeType,
              label: mediaLabel,
              status: "done",
              mediaType: media.type,
              mediaUrl: media.url,
              startTime: Date.now(),
              endTime: Date.now(),
              turnId: this.currentTurnId || undefined,
            },
          });
          if (parentId) {
            this.addEdge(parentId, id);
          }
          this.lastNodeId = id;
          this.turnNodeIds.push(id);
          void this.stabilizeMediaNode(id, media.type, media.url);
          this.scheduleLayout();
        }

        break;
      }
    }
  }

  /** Schedule a debounced fitView during streaming (no full re-layout).
   *  Full layout is deferred to finalize() to prevent nodes from jumping. */
  private scheduleLayout() {
    if (this.layoutTimer) clearTimeout(this.layoutTimer);
    this.layoutTimer = setTimeout(() => {
      this.layoutTimer = null;
      const store = useCanvasStore.getState();
      if (store.nodes.length === 0 || store.isUserInteracting) return;
      store.triggerFitView();
    }, SessionCanvasBridge.LAYOUT_DEBOUNCE_MS);
  }

  /** Restore lastNodeId and edgeCounter from existing canvas nodes. */
  restoreLastNode() {
    const { nodes, edges } = useCanvasStore.getState();
    if (nodes.length === 0) return;

    // Sync edge counter to avoid ID collisions with loaded edges.
    for (const e of edges) {
      const m = /^edge_(\d+)$/.exec(e.id);
      if (m) this.edgeCounter = Math.max(this.edgeCounter, Number(m[1]) + 1);
    }

    // Fix #11: Use stable tiebreaker (node ID) when timestamps match.
    let best = nodes[0];
    let bestTime = 0;
    for (const n of nodes) {
      const d = n.data as CanvasNodeData;
      const t = d.endTime || d.startTime || 0;
      if (t > bestTime || (t === bestTime && n.id > best.id)) {
        bestTime = t;
        best = n;
      }
    }
    this.lastNodeId = best.id;
  }

  /** Apply auto-layout to all nodes (final pass) and fit view. */
  finalize() {
    if (this.layoutTimer) {
      clearTimeout(this.layoutTimer);
      this.layoutTimer = null;
    }
    this.turnActive = false;

    // Fix #5: Mark agent node as done only when the turn truly ends.
    if (this.agentNodeId) {
      useCanvasStore.getState().updateNode(this.agentNodeId, {
        status: "done",
        endTime: Date.now(),
      });
    }

    // Auto-group turn nodes if there are 2+ nodes in this turn.
    if (this.turnNodeIds.length >= 2) {
      const store = useCanvasStore.getState();
      // Extract a short label from the first prompt node in this turn.
      const firstNode = store.nodes.find((n) => n.id === this.turnNodeIds[0]);
      const promptContent = (firstNode?.data as CanvasNodeData)?.content || "";
      const turnLabel = promptContent.length > 0
        ? SessionCanvasBridge.truncateLabel(promptContent, 30)
        : `Turn (${this.turnNodeIds.length})`;
      const newGroupId = store.groupNodes(this.turnNodeIds, turnLabel);

      // Connect consecutive groups: if any child node in this turn has an
      // edge from a node belonging to a different group, create a group-level
      // edge so the two groups are visually linked.
      if (newGroupId) {
        this.linkParentGroups(newGroupId);
      }
    }

    const store = useCanvasStore.getState();
    if (store.nodes.length === 0) return;
    const laidOut = autoLayoutGraph(store.nodes, store.edges, store.layoutMode);
    store.setNodes(laidOut);
    store.commitHistory();
    // Trigger fitView so all nodes are visible after layout.
    deferredFitView();
  }

  /** Calculate the next node position relative to a parent. */
  private nextPosition(parentId: string | null): { x: number; y: number } {
    if (!parentId) return { x: 0, y: 0 };

    // Use Map for O(1) lookup instead of O(n) array.find on every call.
    const nodes = useCanvasStore.getState().nodes;
    if (!this.nodeMap || this.nodeMapGeneration !== nodes.length) {
      this.nodeMap = new Map(nodes.map((n) => [n.id, n]));
      this.nodeMapGeneration = nodes.length;
    }
    const parent = this.nodeMap.get(parentId);
    if (!parent) return { x: 0, y: 0 };

    const childIndex = this.childCountAtParent.get(parentId) || 0;
    this.childCountAtParent.set(parentId, childIndex + 1);

    return {
      x: parent.position.x + H_GAP,
      y: parent.position.y + childIndex * V_GAP,
    };
  }

  /** Truncate text at a natural boundary (punctuation > space > hard cut). */
  private static truncateLabel(text: string, max: number): string {
    if (text.length <= max) return text;
    // Try to break at sentence-ending punctuation.
    const punctRe = /[。.!?！？\n]/g;
    let lastPunct = -1;
    let m: RegExpExecArray | null;
    while ((m = punctRe.exec(text)) !== null) {
      if (m.index >= max) break;
      lastPunct = m.index;
    }
    if (lastPunct > max * 0.4) return text.slice(0, lastPunct + 1);
    // Fall back to last space.
    const lastSpace = text.lastIndexOf(" ", max);
    if (lastSpace > max * 0.4) return text.slice(0, lastSpace) + "...";
    return text.slice(0, max) + "...";
  }

  /** Generate a short label from tool input (mirrors thread auto-title logic). */
  private static generateNodeLabel(toolName: string, input?: Record<string, unknown>): string {
    const raw = (input?.prompt ?? input?.text) as string | undefined;
    if (!raw) return toolName;
    const first = raw.split(/[。.!?！？\n]/)[0].trim();
    if (first.length > 0 && first.length <= 30) return first;
    if (raw.length <= 30) return raw;
    const truncated = raw.slice(0, 30);
    const lastSpace = truncated.lastIndexOf(" ");
    return (lastSpace > 10 ? truncated.slice(0, lastSpace) : truncated) + "...";
  }

  private createToolNode(toolId: string, name: string): string {
    const store = useCanvasStore.getState();
    // Prefer agent node as parent; fall back to lastNodeId when tool
    // execution starts before the agent's content_block_start fires.
    const parentId = this.agentNodeId || this.lastNodeId;
    const pos = this.nextPosition(parentId);

    const id = store.addNode({
      type: "tool",
      position: pos,
      data: {
        nodeType: "tool" as CanvasNodeType,
        label: SessionCanvasBridge.generateNodeLabel(name, this.pendingToolInput),
        toolName: name,
        toolParams: this.pendingToolInput,
        status: "running",
        startTime: Date.now(),
        turnId: this.currentTurnId || undefined,
      },
    });
    this.pendingToolInput = undefined;
    this.toolNodeMap.set(toolId, id);
    this.turnNodeIds.push(id);

    if (parentId) {
      this.addEdge(parentId, id);
    }
    this.lastNodeId = id;
    return id;
  }

  private addEdge(source: string, target: string, edgeType: "flow" | "reference" | "context" = "flow") {
    useCanvasStore.getState().addEdge({
      id: `edge_${this.edgeCounter++}`,
      source,
      target,
      type: edgeType,
    });
  }

  /**
   * Find canvas media nodes whose URL matches a tool's input references
   * and create edges from those nodes to the tool node.
   */
  private linkMediaReferences(toolNodeId: string, input?: Record<string, unknown>) {
    if (!input) return;

    const refUrls: string[] = [];
    for (const key of MEDIA_REF_KEYS) {
      const val = input[key];
      if (typeof val === "string" && val) refUrls.push(val);
    }
    // reference_images is an array
    const refImages = input["reference_images"];
    if (Array.isArray(refImages)) {
      for (const u of refImages) {
        if (typeof u === "string" && u) refUrls.push(u);
      }
    }

    if (refUrls.length === 0) return;

    const nodes = useCanvasStore.getState().nodes;
    const refSet = new Set(refUrls);

    for (const node of nodes) {
      const d = node.data as CanvasNodeData;
      if (!d.mediaUrl) continue;
      // Match against both current mediaUrl and original sourceUrl
      // (cacheCanvasMedia stabilizes URLs, storing the original in sourceUrl).
      if (refSet.has(d.mediaUrl) || (d.sourceUrl && refSet.has(d.sourceUrl))) {
        // Use "context" edge for cross-turn references, "reference" for same-turn.
        const isCrossTurn = d.turnId && this.currentTurnId && d.turnId !== this.currentTurnId;
        this.addEdge(node.id, toolNodeId, isCrossTurn ? "context" : "reference");
      }
    }
  }

  /**
   * After grouping, find cross-group edges between child nodes and create
   * a group-level flow edge so consecutive groups are visually connected.
   */
  private linkParentGroups(newGroupId: string) {
    const { nodes, edges } = useCanvasStore.getState();
    const childIds = new Set(
      nodes.filter((n) => n.parentId === newGroupId).map((n) => n.id)
    );
    const nodeMap = new Map(nodes.map((n) => [n.id, n]));

    // Find groups that are sources of edges targeting children of this group.
    const linkedGroupIds = new Set<string>();
    for (const edge of edges) {
      if (!childIds.has(edge.target)) continue;
      const sourceNode = nodeMap.get(edge.source);
      if (sourceNode?.parentId && sourceNode.parentId !== newGroupId) {
        linkedGroupIds.add(sourceNode.parentId);
      }
    }

    for (const prevGroupId of linkedGroupIds) {
      // Avoid duplicate group-level edges.
      const exists = edges.some(
        (e) => e.source === prevGroupId && e.target === newGroupId
      );
      if (!exists) {
        this.addEdge(prevGroupId, newGroupId);
      }
    }
  }

  private async stabilizeMediaNode(nodeId: string, mediaType: MediaType, rawUrl: string) {
    const stabilized = await cacheCanvasMedia(rawUrl, mediaType);
    if (!stabilized.mediaUrl || stabilized.mediaUrl === rawUrl) {
      return;
    }

    useCanvasStore.getState().updateNode(nodeId, {
      mediaUrl: stabilized.mediaUrl,
      mediaPath: stabilized.mediaPath,
      sourceUrl: stabilized.sourceUrl,
    });
  }
}
