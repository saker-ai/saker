"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import {
  ReactFlow,
  ReactFlowProvider,
  MiniMap,
  Controls,
  Background,
  BackgroundVariant,
  useReactFlow,
  type NodeTypes,
  type EdgeTypes,
  type Connection,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { FolderHeart, Plus, Clock, Download, LayoutGrid, Bookmark } from "lucide-react";
import { useCanvasStore, loadFromLocalStorage, deferredFitView, INFINITE_CANVAS_KEY } from "./store";
import { autoLayoutGraph } from "./layout";
import { PromptNode } from "./nodes/PromptNode";
import { AgentNode } from "./nodes/AgentNode";
import { ToolNode } from "./nodes/ToolNode";
import { ImageNode } from "./nodes/ImageNode";
import { VideoNode } from "./nodes/VideoNode";
import { AudioNode } from "./nodes/AudioNode";
import { TextNode } from "./nodes/TextNode";
import { AITypoNode } from "./nodes/AITypoNode";
import { TextGenNode } from "./nodes/TextGenNode";
import { SkillNode } from "./nodes/SkillNode";
import { CompositionNode } from "./nodes/CompositionNode";
import { ImageGenNode } from "./nodes/ImageGenNode";
import { VoiceGenNode } from "./nodes/VoiceGenNode";
import { VideoGenNode } from "./nodes/VideoGenNode";
import { GroupNode } from "./nodes/GroupNode";
import { SketchNode } from "./nodes/SketchNode";
import { LLMNode } from "./nodes/LLMNode";
import { MaskNode } from "./nodes/MaskNode";
import { ReferenceNode } from "./nodes/ReferenceNode";
import { ExportNode } from "./nodes/ExportNode";
import { TableNode } from "./nodes/TableNode";
import { AppInputNode } from "./nodes/AppInputNode";
import { AppOutputNode } from "./nodes/AppOutputNode";
import { FlowEdge } from "./edges/FlowEdge";
import { ReferenceEdge } from "./edges/ReferenceEdge";
import { ContextEdge } from "./edges/ContextEdge";
import { MediaPreview } from "./nodes/MediaPreview";
import { NodeContextMenu } from "./nodes/NodeContextMenu";
import { AssetLibrary } from "./panels/AssetLibrary";
import { HistoryPanel } from "./panels/HistoryPanel";
import { TemplatePanel } from "./panels/TemplatePanel";
import { CanvasToast } from "./panels/CanvasToast";
import { BulkToolbar } from "./panels/BulkToolbar";
import { NodeSearch } from "./panels/NodeSearch";
import { QuickAddMenu, type QuickAddType } from "./panels/QuickAddMenu";
import { useT } from "@/features/i18n";
import { usePermissions } from "@/features/project/usePermissions";
import type { CanvasNodeData, CanvasNodeType } from "./types";
import type { LayoutMode } from "./layout";
import { cacheCanvasMedia } from "./mediaCache";
import { showCanvasToast } from "./panels/CanvasToast";

const nodeTypes: NodeTypes = {
  prompt: PromptNode,
  agent: AgentNode,
  tool: ToolNode,
  skill: SkillNode,
  image: ImageNode,
  video: VideoNode,
  audio: AudioNode,
  text: TextNode,
  aiTypo: AITypoNode,
  textGen: TextGenNode,
  composition: CompositionNode,
  imageGen: ImageGenNode,
  voiceGen: VoiceGenNode,
  videoGen: VideoGenNode,
  sketch: SketchNode,
  group: GroupNode,
  llm: LLMNode,
  mask: MaskNode,
  reference: ReferenceNode,
  export: ExportNode,
  table: TableNode,
  appInput: AppInputNode,
  appOutput: AppOutputNode,
};

const edgeTypes: EdgeTypes = {
  flow: FlowEdge,
  reference: ReferenceEdge,
  context: ContextEdge,
};

const SNAP_GRID: [number, number] = [20, 20];

interface ConnectMenuState {
  sourceNodeId: string;
  sourceHandleId?: string | null;
  screenX: number;
  screenY: number;
}

interface PaneMenuState {
  screenX: number;
  screenY: number;
}

function CanvasInner() {
  const { t } = useT();
  const perms = usePermissions();
  const canEdit = perms.canEdit;
  const nodes = useCanvasStore((s) => s.nodes);
  const edges = useCanvasStore((s) => s.edges);
  const viewport = useCanvasStore((s) => s.viewport);
  const setNodes = useCanvasStore((s) => s.setNodes);
  const setViewport = useCanvasStore((s) => s.setViewport);
  const fitViewTrigger = useCanvasStore((s) => s.fitViewTrigger);
  const addNode = useCanvasStore((s) => s.addNode);
  const addEdge = useCanvasStore((s) => s.addEdge);
  const removeNodes = useCanvasStore((s) => s.removeNodes);
  const undo = useCanvasStore((s) => s.undo);
  const redo = useCanvasStore((s) => s.redo);
  const { fitView, screenToFlowPosition, getViewport } = useReactFlow();
  const prevTrigger = useRef(fitViewTrigger);

  const setUserInteracting = useCanvasStore((s) => s.setUserInteracting);
  const [isMoving, setIsMoving] = useState(false);
  const currentZoom = getViewport()?.zoom || 1;
  const isLowZoom = currentZoom < 0.45;

  const [assetOpen, setAssetOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [templateOpen, setTemplateOpen] = useState(false);
  const [addMenuOpen, setAddMenuOpen] = useState(false);
  const [layoutMenuOpen, setLayoutMenuOpen] = useState(false);
  const [connectMenu, setConnectMenu] = useState<ConnectMenuState | null>(null);
  const [paneMenu, setPaneMenu] = useState<PaneMenuState | null>(null);
  const connectSourceRef = useRef<string | null>(null);
  const connectSourceHandleRef = useRef<string | null>(null);
  const layoutMode = useCanvasStore((s) => s.layoutMode);
  const setLayoutMode = useCanvasStore((s) => s.setLayoutMode);
  const setHighlightedTurnId = useCanvasStore((s) => s.setHighlightedTurnId);

  const closeAllPanels = () => { setAssetOpen(false); setHistoryOpen(false); setTemplateOpen(false); setAddMenuOpen(false); setLayoutMenuOpen(false); setConnectMenu(null); setPaneMenu(null); };

  const handleEdgesChange = useCallback((changes: Parameters<NonNullable<React.ComponentProps<typeof ReactFlow>["onEdgesChange"]>>[0]) => {
    const removeIds = new Set(
      changes.filter((c) => c.type === "remove" && "id" in c).map((c) => (c as { id: string }).id)
    );
    if (removeIds.size === 0) return;
    const currentEdges = useCanvasStore.getState().edges;
    useCanvasStore.getState().setEdges(currentEdges.filter((e) => !removeIds.has(e.id)));
  }, []);

  const handleNodesChange = useCallback((changes: Parameters<NonNullable<React.ComponentProps<typeof ReactFlow>["onNodesChange"]>>[0]) => {
    const removeIds = new Set<string>();
    const changeMap = new Map<string, (typeof changes)[number]>();
    
    for (const c of changes) {
      if (c.type === "remove") {
        removeIds.add(c.id);
      } else if ("id" in c) {
        changeMap.set(c.id, c);
      }
    }
    
    if (changeMap.size === 0 && removeIds.size === 0) return;
    
    let currentNodes = useCanvasStore.getState().nodes;
    let hasChanges = false;
    
    if (removeIds.size > 0) {
      currentNodes = currentNodes.filter((n) => !removeIds.has(n.id));
      hasChanges = true;
    }
    
    const updated = currentNodes.map((node) => {
      const change = changeMap.get(node.id);
      if (!change) return node;
      
      if (change.type === "position" && change.position) {
        // Filter out tiny jitters
        if (Math.abs(node.position.x - change.position.x) < 0.1 &&
            Math.abs(node.position.y - change.position.y) < 0.1) return node;
        hasChanges = true;
        // Mark as pinned when user finishes dragging so auto-layout preserves position.
        const shouldPin = change.dragging === false && !node.data.pinned;
        return {
          ...node,
          position: change.position,
          ...(shouldPin ? { data: { ...node.data, pinned: true } } : {}),
        };
      }
      if (change.type === "select") {
        if (node.selected === change.selected) return node;
        hasChanges = true;
        return { ...node, selected: change.selected };
      }
      if (change.type === "dimensions" && "dimensions" in change && change.dimensions) {
        if (node.measured?.width === change.dimensions.width && 
            node.measured?.height === change.dimensions.height) return node;
        hasChanges = true;
        return { ...node, measured: { width: change.dimensions.width, height: change.dimensions.height } };
      }
      return node;
    });
    
    if (hasChanges) setNodes(updated);
  }, [setNodes]);

  // Keyboard shortcuts (single unified handler)
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement;
      const inField = target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT" || target.isContentEditable;

      const isMac = /Mac|iPod|iPhone|iPad/.test(navigator.platform);
      const modifier = isMac ? e.metaKey : e.ctrlKey;

      // Cmd/Ctrl+Enter — trigger generation on any selected gen node, even from within its own textarea
      if (modifier && e.key === "Enter") {
        const selectedGen = useCanvasStore.getState().nodes.find(n => {
          const nt = (n.data as CanvasNodeData).nodeType;
          return n.selected && (nt === "imageGen" || nt === "videoGen");
        });
        if (selectedGen) {
          e.preventDefault();
          window.dispatchEvent(new CustomEvent("canvas-retry-node", { detail: { nodeId: selectedGen.id } }));
          return;
        }
      }

      // Esc — close any open menu/panel, even from inside input fields
      if (e.key === "Escape") {
        setAssetOpen(false);
        setHistoryOpen(false);
        setTemplateOpen(false);
        setAddMenuOpen(false);
        setLayoutMenuOpen(false);
        setConnectMenu(null);
        setPaneMenu(null);
        // Don't preventDefault — allow inputs to blur naturally
        return;
      }

      if (inField) return;

      // All shortcuts below mutate the canvas; viewers can't trigger them.
      if (!canEdit) return;

      // Delete / Backspace — remove selected nodes and edges (skip locked nodes)
      if (e.key === "Delete" || e.key === "Backspace") {
        const selectedNodes = useCanvasStore.getState().nodes.filter(n => n.selected);
        const selectedEdges = useCanvasStore.getState().edges.filter(ed => ed.selected);
        const deletableNodes = selectedNodes.filter(n => !(n.data as CanvasNodeData | undefined)?.locked);
        const lockedCount = selectedNodes.length - deletableNodes.length;
        if (deletableNodes.length > 0 || selectedEdges.length > 0) {
          e.preventDefault();
          useCanvasStore.getState().commitHistory();
          if (deletableNodes.length > 0) removeNodes(deletableNodes.map(n => n.id));
          if (selectedEdges.length > 0) useCanvasStore.getState().removeEdges(selectedEdges.map(ed => ed.id));
        }
        if (lockedCount > 0) {
          showCanvasToast("error", t("canvas.nodesLocked" as any) || "Locked nodes skipped");
        } else if (selectedNodes.length > 0 && deletableNodes.length === 0 && selectedEdges.length === 0) {
          showCanvasToast("error", t("canvas.nodesLocked" as any) || "All selected nodes are locked");
        }
        return;
      }

      // Auto Layout: Ctrl+L
      if (modifier && e.key === "l") {
        e.preventDefault();
        const { nodes, edges, layoutMode, setNodes } = useCanvasStore.getState();
        useCanvasStore.getState().commitHistory();
        setNodes(autoLayoutGraph(nodes, edges, layoutMode));
        deferredFitView();
        return;
      }

      // Group: Ctrl+G
      if (modifier && e.key === "g") {
        e.preventDefault();
        const selectedIds = useCanvasStore.getState().nodes.filter(n => n.selected).map(n => n.id);
        if (selectedIds.length >= 2) {
          useCanvasStore.getState().commitHistory();
          useCanvasStore.getState().groupNodes(selectedIds);
        }
        return;
      }

      // Undo: Ctrl+Z
      if (modifier && !e.shiftKey && e.key === "z") {
        e.preventDefault();
        undo();
        return;
      }

      // Redo: Ctrl+Shift+Z or Ctrl+Y
      if ((modifier && e.shiftKey && e.key.toLowerCase() === "z") || (modifier && e.key === "y")) {
        e.preventDefault();
        redo();
        return;
      }

      // Select All: Ctrl+A
      if (modifier && e.key === "a") {
        e.preventDefault();
        const { nodes, setNodes } = useCanvasStore.getState();
        setNodes(nodes.map(n => ({ ...n, selected: true })));
        return;
      }

      // Duplicate: Ctrl+D
      if (modifier && e.key === "d") {
        const selected = useCanvasStore.getState().nodes.filter(n => n.selected);
        if (selected.length > 0) {
          e.preventDefault();
          for (const node of selected) {
            addNode({
              type: node.type,
              position: { x: node.position.x + 40, y: node.position.y + 40 },
              data: { ...node.data },
            });
          }
        }
        return;
      }
    };

    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [removeNodes, undo, redo, addNode, canEdit, t]);

  // Derived threadId from URL hash for localStorage scoping.
  // Falls back to INFINITE_CANVAS_KEY so thread-less sessions still persist.
  const getThreadId = useCallback(() => {
    const hash = window.location.hash;
    const m = /[#/](?:canvas|chats)\/([a-f0-9-]+)/i.exec(hash);
    return m ? m[1] : INFINITE_CANVAS_KEY;
  }, []);

  // localStorage persistence is handled by useCanvasBridge (saveToLocalStorage).
  // Restore canvas from localStorage on mount (only if empty and no bridge loaded data).
  useEffect(() => {
    const state = useCanvasStore.getState();
    if (state.nodes.length > 0) return;
    const loaded = loadFromLocalStorage(getThreadId());
    if (loaded) {
      deferredFitView();
    }
  }, [getThreadId]);

  // Global paste — create image node from clipboard (file or URL text)
  useEffect(() => {
    const IMAGE_URL_RE = /^https?:\/\/.+\.(png|jpe?g|gif|webp|svg|bmp|ico)(\?.*)?$/i;
    const MAX_PASTE_SIZE = 50 * 1024 * 1024;

    const createImageNodeFromBlob = (blobUrl: string, label: string) => {
      const cx = window.innerWidth / 2;
      const cy = window.innerHeight / 2;
      const pos = screenToFlowPosition({ x: cx, y: cy });

      addNode({
        type: "image",
        position: pos,
        data: {
          nodeType: "image" as CanvasNodeType,
          label,
          mediaUrl: blobUrl,
          mediaType: "image",
          status: "done" as const,
          pinned: true,
        },
      });

      cacheCanvasMedia(blobUrl, "image").then((stabilized) => {
        if (stabilized.mediaUrl && stabilized.mediaUrl !== blobUrl) {
          const currentNodes = useCanvasStore.getState().nodes;
          const n = currentNodes.find((n) => (n.data as CanvasNodeData).mediaUrl === blobUrl);
          if (n) {
            useCanvasStore.getState().updateNode(n.id, {
              mediaUrl: stabilized.mediaUrl,
              mediaPath: stabilized.mediaPath,
              sourceUrl: stabilized.sourceUrl,
            });
          }
        }
        URL.revokeObjectURL(blobUrl);
      });

      showCanvasToast("success", t("canvas.imagePasted"));
    };

    const handler = (e: ClipboardEvent) => {
      const target = e.target as HTMLElement;
      if (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT" || target.isContentEditable) return;
      if (!canEdit) return;

      const items = e.clipboardData?.items;
      if (!items) return;

      // Check for image file in clipboard
      for (const item of items) {
        if (item.type.startsWith("image/")) {
          const file = item.getAsFile();
          if (!file) continue;

          if (file.size > MAX_PASTE_SIZE) {
            e.preventDefault();
            showCanvasToast("error", t("canvas.fileTooLarge"));
            return;
          }

          e.preventDefault();
          createImageNodeFromBlob(URL.createObjectURL(file), file.name || "Pasted Image");
          return;
        }
      }

      // Check for image URL text in clipboard
      const text = e.clipboardData?.getData("text/plain")?.trim();
      if (text && IMAGE_URL_RE.test(text)) {
        e.preventDefault();
        const cx = window.innerWidth / 2;
        const cy = window.innerHeight / 2;
        const pos = screenToFlowPosition({ x: cx, y: cy });

        addNode({
          type: "image",
          position: pos,
          data: {
            nodeType: "image" as CanvasNodeType,
            label: text.split("/").pop()?.split("?")[0]?.slice(0, 30) || "Image",
            mediaUrl: text,
            mediaType: "image",
            status: "done" as const,
            pinned: true,
          },
        });

        cacheCanvasMedia(text, "image").then((stabilized) => {
          if (stabilized.mediaUrl && stabilized.mediaUrl !== text) {
            const currentNodes = useCanvasStore.getState().nodes;
            const n = currentNodes.find((n) => (n.data as CanvasNodeData).mediaUrl === text);
            if (n) {
              useCanvasStore.getState().updateNode(n.id, {
                mediaUrl: stabilized.mediaUrl,
                mediaPath: stabilized.mediaPath,
                sourceUrl: stabilized.sourceUrl,
              });
            }
          }
        });

        showCanvasToast("success", t("canvas.imagePasted"));
      }
    };

    window.addEventListener("paste", handler);
    return () => window.removeEventListener("paste", handler);
  }, [addNode, screenToFlowPosition, t, canEdit]);

  useEffect(() => {
    if (fitViewTrigger !== prevTrigger.current) {
      prevTrigger.current = fitViewTrigger;
      fitView({ padding: 0.15, duration: 300 });
    }
  }, [fitViewTrigger, fitView]);

  const onConnect = useCallback(
    (connection: Connection) => {
      if (connection.source && connection.target) {
        const currentNodes = useCanvasStore.getState().nodes;
        const src = currentNodes.find((n) => n.id === connection.source);
        const tgt = currentNodes.find((n) => n.id === connection.target);
        const srcMediaType = src?.data?.mediaType;
        const tgtIsGenerator = tgt?.data?.nodeType === "imageGen" || tgt?.data?.nodeType === "videoGen";
        const edgeType: "flow" | "reference" =
          tgtIsGenerator && (srcMediaType === "image" || srcMediaType === "video") ? "reference" : "flow";
        addEdge({
          id: `edge_${connection.source}_${connection.sourceHandle ?? "out"}_${connection.target}_${connection.targetHandle ?? "in"}`,
          source: connection.source,
          sourceHandle: connection.sourceHandle,
          target: connection.target,
          targetHandle: connection.targetHandle,
          type: edgeType,
        });
      }
    },
    [addEdge]
  );

  const onConnectStart = useCallback(
    (_: unknown, params: { nodeId?: string | null; handleId?: string | null; handleType?: string | null }) => {
      if (params.handleType === "source" && params.nodeId) {
        connectSourceRef.current = params.nodeId;
        connectSourceHandleRef.current = params.handleId ?? null;
      }
    },
    []
  );

  const onConnectEnd = useCallback(
    (event: MouseEvent | TouchEvent) => {
      if (!connectSourceRef.current) return;
      const target = event.target as HTMLElement;
      if (target.closest(".react-flow__node")) {
        connectSourceRef.current = null;
        connectSourceHandleRef.current = null;
        return;
      }
      const touch = "changedTouches" in event ? event.changedTouches[0] : undefined;
      const clientX = touch ? touch.clientX : (event as MouseEvent).clientX;
      const clientY = touch ? touch.clientY : (event as MouseEvent).clientY;
      if (clientX == null || clientY == null) { connectSourceRef.current = null; return; }
      setConnectMenu({
        sourceNodeId: connectSourceRef.current,
        sourceHandleId: connectSourceHandleRef.current,
        screenX: clientX,
        screenY: clientY,
      });
      connectSourceRef.current = null;
      connectSourceHandleRef.current = null;
    },
    []
  );

  const createConnectedNode = useCallback(
    (type: CanvasNodeType | "imageEdit" | "videoEdit") => {
      if (!connectMenu) return;
      const pos = screenToFlowPosition({ x: connectMenu.screenX, y: connectMenu.screenY });

      let newNodeId: string;
      if (type === "imageEdit") {
        newNodeId = addNode({
          type: "image",
          position: pos,
          data: {
            nodeType: "image" as CanvasNodeType,
            label: t("canvas.imageEditNode"),
            status: "pending",
            editOnCreate: true,
          },
        });
      } else if (type === "videoEdit") {
        newNodeId = addNode({
          type: "video",
          position: pos,
          data: {
            nodeType: "video" as CanvasNodeType,
            label: t("canvas.videoEditNode"),
            status: "pending",
            editOnCreate: true,
          },
        });
      } else {
        const labelMap: Record<string, string> = {
          imageGen: t("canvas.imageGen"),
          voiceGen: t("canvas.audioGen"),
          videoGen: t("canvas.videoGen"),
          textGen: t("canvas.textGen"),
          aiTypo: t("canvas.aiTypo"),
          sketch: t("canvas.sketch" as any),
        };
        newNodeId = addNode({
          type,
          position: pos,
          data: {
            nodeType: type,
            label: labelMap[type] || type,
            status: "pending",
            startTime: Date.now(),
          },
        });
      }
      const currentNodes = useCanvasStore.getState().nodes;
      const src = currentNodes.find((n) => n.id === connectMenu.sourceNodeId);
      const srcMediaType = src?.data?.mediaType;
      const tgtIsGenerator = type === "imageGen" || type === "videoGen";
      const edgeType: "flow" | "reference" =
        tgtIsGenerator && (srcMediaType === "image" || srcMediaType === "video") ? "reference" : "flow";
      addEdge({
        id: `edge_${connectMenu.sourceNodeId}_${connectMenu.sourceHandleId ?? "out"}_${newNodeId}`,
        source: connectMenu.sourceNodeId,
        sourceHandle: connectMenu.sourceHandleId ?? undefined,
        target: newNodeId,
        type: edgeType,
      });
      setConnectMenu(null);
    },
    [connectMenu, addNode, addEdge, screenToFlowPosition, t]
  );

  // Drag & drop files onto canvas
  const [dropActive, setDropActive] = useState(false);
  const onDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
    if (e.dataTransfer.types.includes("Files") && !dropActive) setDropActive(true);
  }, [dropActive]);
  const onDragLeave = useCallback((e: React.DragEvent) => {
    // Only clear when leaving the canvas root, not child nodes
    if (e.currentTarget === e.target) setDropActive(false);
  }, []);

  const onDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      setDropActive(false);
      if (!canEdit) return;
      const files = Array.from(e.dataTransfer.files);
      if (files.length === 0) return;

      const pos = screenToFlowPosition({ x: e.clientX, y: e.clientY });

      files.forEach((file, i) => {
        const mediaType = file.type.startsWith("image/")
          ? "image"
          : file.type.startsWith("video/")
            ? "video"
            : file.type.startsWith("audio/")
              ? "audio"
              : null;

        if (!mediaType) return;

        const blobUrl = URL.createObjectURL(file);
        const nodeType = mediaType as "image" | "video" | "audio";
        addNode({
          type: nodeType,
          position: { x: pos.x + i * 40, y: pos.y + i * 40 },
          data: {
            nodeType,
            label: file.name.slice(0, 30),
            mediaUrl: blobUrl,
            mediaType,
            status: "done" as const,
            pinned: true,
          },
        });
        // Stabilize the blob URL to a cached copy and revoke the blob
        cacheCanvasMedia(blobUrl, mediaType).then((stabilized) => {
          if (stabilized.mediaUrl && stabilized.mediaUrl !== blobUrl) {
            const currentNodes = useCanvasStore.getState().nodes;
            const target = currentNodes.find((n) => (n.data as CanvasNodeData).mediaUrl === blobUrl);
            if (target) {
              useCanvasStore.getState().updateNode(target.id, {
                mediaUrl: stabilized.mediaUrl,
                mediaPath: stabilized.mediaPath,
                sourceUrl: stabilized.sourceUrl,
              });
            }
          }
          URL.revokeObjectURL(blobUrl);
        });
      });
    },
    [addNode, screenToFlowPosition, canEdit]
  );

  const createGenNodeAt = useCallback(
    (type: QuickAddType, x: number, y: number) => {
      const pos = screenToFlowPosition({ x, y });
      if (type === "imageEdit") {
        addNode({
          type: "image",
          position: pos,
          data: {
            nodeType: "image" as CanvasNodeType,
            label: t("canvas.imageEditNode"),
            status: "pending",
            editOnCreate: true,
          },
        });
        return;
      }
      if (type === "videoEdit") {
        addNode({
          type: "video",
          position: pos,
          data: {
            nodeType: "video" as CanvasNodeType,
            label: t("canvas.videoEditNode"),
            status: "pending",
            editOnCreate: true,
          },
        });
        return;
      }
      const labelMap: Record<string, string> = {
        imageGen: t("canvas.imageGen"),
        voiceGen: t("canvas.audioGen"),
        videoGen: t("canvas.videoGen"),
        textGen: t("canvas.textGen"),
        aiTypo: t("canvas.aiTypo"),
        sketch: t("canvas.sketch" as any),
        llm: t("canvas.llm" as any),
        mask: t("canvas.mask" as any),
        reference: t("canvas.reference" as any),
        export: t("canvas.export" as any),
        table: t("canvas.table" as any),
        appInput: t("canvas.appInputLabel" as any),
        appOutput: t("canvas.appOutputLabel" as any),
      };
      if (type === "appInput") {
        addNode({
          type: "appInput",
          position: pos,
          data: {
            nodeType: "appInput" as CanvasNodeType,
            label: labelMap.appInput,
            status: "done",
            appVariable: "input",
            appFieldType: "text",
            appRequired: false,
          },
        });
        return;
      }
      if (type === "appOutput") {
        addNode({
          type: "appOutput",
          position: pos,
          data: {
            nodeType: "appOutput" as CanvasNodeType,
            label: labelMap.appOutput,
            status: "done",
            appOutputKind: "text",
          },
        });
        return;
      }
      addNode({
        type: type as CanvasNodeType,
        position: pos,
        data: {
          nodeType: type as CanvasNodeType,
          label: labelMap[type] || type,
          status: "pending",
          startTime: Date.now(),
        },
      });
    },
    [addNode, screenToFlowPosition, t]
  );

  const createGenNode = useCallback(
    (type: QuickAddType) => {
      createGenNodeAt(type, window.innerWidth / 2 - 140, window.innerHeight / 2 - 100);
      setAddMenuOpen(false);
    },
    [createGenNodeAt]
  );

  // Right-click on canvas pane
  const onPaneContextMenu = useCallback(
    (event: MouseEvent | React.MouseEvent) => {
      event.preventDefault();
      if (!canEdit) return;
      setPaneMenu({ screenX: event.clientX, screenY: event.clientY });
    },
    [canEdit]
  );

  const [snapToGrid] = useState(true);

  return (
    <>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        defaultViewport={viewport}
        onConnect={onConnect}
        onConnectStart={onConnectStart}
        onConnectEnd={onConnectEnd}
        onDragOver={onDragOver}
        onDragLeave={onDragLeave}
        onDrop={onDrop}
        onPaneContextMenu={onPaneContextMenu}
        onPaneClick={() => { setPaneMenu(null); setHighlightedTurnId(null); }}
        onNodeClick={(_event, node) => {
          const d = node.data as CanvasNodeData;
          if (d.turnId) setHighlightedTurnId(d.turnId);
        }}
        snapToGrid={snapToGrid}
        snapGrid={SNAP_GRID}
        onNodesChange={handleNodesChange}
        onEdgesChange={handleEdgesChange}
        onMoveStart={() => { setIsMoving(true); setUserInteracting(true); }}
        onMoveEnd={(_event, vp) => {
          setViewport(vp);
          setIsMoving(false);
          setUserInteracting(false);
        }}
        fitView
        fitViewOptions={{ padding: 0.15 }}
        proOptions={{ hideAttribution: true }}
        minZoom={0.05}
        maxZoom={2}
        onlyRenderVisibleElements
        nodeDragThreshold={5}
        nodesDraggable={canEdit}
        nodesConnectable={canEdit}
        edgesReconnectable={canEdit}
        elementsSelectable
        className={`${(isMoving || isLowZoom) ? "canvas-low-perf-mode" : ""}${!canEdit ? " canvas-readonly" : ""}`}
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1} color="var(--border)" />
        <MiniMap
          nodeStrokeColor="var(--border)"
          nodeColor={(node) => {
            const nt = (node.data as CanvasNodeData).nodeType;
            if (nt === "prompt") return "#6366f1";
            if (nt === "agent") return "#10b981";
            if (nt === "tool") return "#f59e0b";
            if (nt === "image" || nt === "video" || nt === "audio") return "#ec4899";
            if (nt === "sketch") return "#f472b6";
            if (nt === "table") return "#0ea5e9";
            if (nt === "group") return "rgba(99,102,241,0.15)";
            return "var(--bg-secondary)";
          }}
          maskColor="rgba(0,0,0,0.1)"
        />
        <Controls position="bottom-left" className="canvas-controls" />
      </ReactFlow>

      {/* Left-side canvas toolbar */}
      <div className="canvas-left-toolbar">
        {canEdit && (
          <div className="canvas-add-node-wrapper">
            <button
              className={`canvas-left-toolbar-btn ${addMenuOpen ? "active" : ""}`}
              onClick={() => { closeAllPanels(); setAddMenuOpen(!addMenuOpen); }}
              title={t("canvas.addNode")}
            >
              <Plus size={18} />
            </button>
            {addMenuOpen && (
              <QuickAddMenu
                className="canvas-add-menu"
                onSelect={(type) => createGenNode(type as QuickAddType)}
                onClose={() => setAddMenuOpen(false)}
              />
            )}
          </div>
        )}
        <button
          className={`canvas-left-toolbar-btn ${assetOpen ? "active" : ""}`}
          onClick={() => { closeAllPanels(); setAssetOpen(!assetOpen); }}
          title={t("canvas.assetLibrary")}
        >
          <FolderHeart size={18} />
        </button>
        <button
          className={`canvas-left-toolbar-btn ${historyOpen ? "active" : ""}`}
          onClick={() => { closeAllPanels(); setHistoryOpen(!historyOpen); }}
          title={t("canvas.history")}
        >
          <Clock size={18} />
        </button>
        <button
          className={`canvas-left-toolbar-btn ${templateOpen ? "active" : ""}`}
          onClick={() => { closeAllPanels(); setTemplateOpen(!templateOpen); }}
          title={t("canvas.templates" as any)}
        >
          <Bookmark size={18} />
        </button>
        <div className="canvas-layout-wrapper">
          <button
            className={`canvas-left-toolbar-btn ${layoutMenuOpen ? "active" : ""}`}
            onClick={() => { setLayoutMenuOpen(!layoutMenuOpen); }}
            title={t("canvas.layoutMode")}
          >
            <LayoutGrid size={18} />
          </button>
          {layoutMenuOpen && (
            <div className="canvas-layout-menu">
              {(["auto", "horizontal", "vertical", "grid", "tree"] as LayoutMode[]).map((mode) => {
                const labelKey = `canvas.layout${mode.charAt(0).toUpperCase()}${mode.slice(1)}` as Parameters<typeof t>[0];
                return (
                  <button
                    key={mode}
                    className={`canvas-layout-option ${layoutMode === mode ? "active" : ""}`}
                    onClick={() => { setLayoutMode(mode); setLayoutMenuOpen(false); }}
                  >
                    {t(labelKey)}
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </div>

      {nodes.length === 0 && (
        <div className="canvas-empty">
          <p>{t("canvas.emptyHint")}</p>
        </div>
      )}

      {/* Connect-end quick-add menu */}
      {connectMenu && (
        <QuickAddMenu
          className="canvas-connect-menu"
          style={{ left: connectMenu.screenX, top: connectMenu.screenY }}
          onSelect={(type) => createConnectedNode(type as CanvasNodeType | "imageEdit" | "videoEdit")}
          onClose={() => setConnectMenu(null)}
        />
      )}

      {/* Right-click pane context menu */}
      {paneMenu && (
        <QuickAddMenu
          className="canvas-pane-menu"
          style={{ left: paneMenu.screenX, top: paneMenu.screenY }}
          onSelect={(type) => { createGenNodeAt(type as "imageGen" | "videoGen" | "voiceGen" | "imageEdit" | "videoEdit", paneMenu.screenX, paneMenu.screenY); setPaneMenu(null); }}
          onClose={() => setPaneMenu(null)}
        />
      )}

      <BulkToolbar />
      <MediaPreview />
      <NodeContextMenu />
      <CanvasToast />
      <NodeSearch />
      {dropActive && (
        <div className="canvas-drop-overlay">
          <div className="canvas-drop-overlay-inner">
            <Download size={20} />
            <span>{t("canvas.dropHere" as any)}</span>
          </div>
        </div>
      )}
      <AssetLibrary open={assetOpen} onClose={() => setAssetOpen(false)} />
      <HistoryPanel open={historyOpen} onClose={() => setHistoryOpen(false)} />
      <TemplatePanel open={templateOpen} onClose={() => setTemplateOpen(false)} />
    </>
  );
}

export function CanvasView() {
  return (
    <div className="canvas-container">
      <ReactFlowProvider>
        <CanvasInner />
      </ReactFlowProvider>
    </div>
  );
}
