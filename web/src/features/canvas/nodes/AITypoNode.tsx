import { memo, useMemo, useCallback, useDeferredValue, useState } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Sparkles, ChevronUp, ChevronDown, Maximize2, Edit3, Table as TableIcon } from "lucide-react";
import { motion, AnimatePresence } from "framer-motion";
import { measureNaturalWidth, prepareWithSegments, layoutWithLines, type LayoutLine } from "@chenglou/pretext";
import type { CanvasNodeData } from "../types";
import { useCanvasStore } from "../store";
import { NodeToolbar, getTextActions } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useT } from "@/features/i18n";
import { normalizeManuscriptData } from "../manuscript";
import { ManuscriptEditorOverlay } from "./ManuscriptEditorOverlay";
import { extractManuscriptToTable } from "../extractToTable";
import { renderMarkdown } from "@/features/chat/markdown";

const NODE_WIDTH = 280;
const BODY_HORIZONTAL_PADDING = 12;
const BODY_VERTICAL_PADDING = 12;
const CONTENT_WIDTH = NODE_WIDTH - BODY_HORIZONTAL_PADDING * 2;
const LINE_HEIGHT = 24;
const HEADER_HEIGHT = 44;
const FOOTER_HEIGHT = 34;
const BASE_HEIGHT = HEADER_HEIGHT + BODY_VERTICAL_PADDING * 2;
const FONT_FAMILY = '"Inter", system-ui, sans-serif';
const FONT_SIZE = 14;
const FONT_STYLE = `${FONT_SIZE}px ${FONT_FAMILY}`;
const EMPTY_PLACEHOLDER = "Generating thoughts...";

type EntityHandle = {
  id: string;
  label: string;
  token: string;
  lineIndex: number;
  startIndex: number;
  x: number;
  y: number;
  width: number;
};

function buildEntityHandles(lines: LayoutLine[], nodeId: string) {
  const handles: EntityHandle[] = [];
  const widthCache = new Map<string, number>();
  const measure = (text: string) => {
    if (!text) return 0;
    const cached = widthCache.get(text);
    if (cached != null) return cached;
    const width = measureNaturalWidth(prepareWithSegments(text, FONT_STYLE));
    widthCache.set(text, width);
    return width;
  };

  for (let lineIdx = 0; lineIdx < lines.length; lineIdx++) {
    const line = lines[lineIdx];
    const regex = /\[(.+?)\]/g;
    let match: RegExpExecArray | null;

    while ((match = regex.exec(line.text)) !== null) {
      const token = match[0];
      const label = match[1];
      const prefix = line.text.slice(0, match.index);
      const prefixWidth = measure(prefix);
      const tokenWidth = measure(token);

      handles.push({
        id: `word-handle-${nodeId}-${label}-${lineIdx}-${match.index}`,
        label,
        token,
        lineIndex: lineIdx,
        startIndex: match.index,
        x: prefixWidth + tokenWidth / 2,
        y: lineIdx * LINE_HEIGHT + LINE_HEIGHT / 2,
        width: tokenWidth,
      });
    }
  }

  return handles;
}

function estimateMarkdownHeight(text: string, pretextHeight: number) {
  const lineCount = Math.max(1, text.split(/\n/).length);
  const blockBonus = (text.match(/^#{1,3}\s/gm)?.length ?? 0) * 10
    + (text.match(/^[-*+]\s/gm)?.length ?? 0) * 4
    + (text.match(/^>\s/gm)?.length ?? 0) * 8
    + (text.match(/```/g)?.length ?? 0) * 18;
  return Math.max(pretextHeight, lineCount * LINE_HEIGHT + blockBonus);
}

export const AITypoNode = memo(function AITypoNode({ id, data, selected }: NodeProps) {
  const rawData = data as CanvasNodeData;
  const d = useMemo(() => normalizeManuscriptData(rawData), [rawData]);
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const content = d.content || "";
  const displayContent = content || EMPTY_PLACEHOLDER;
  const deferredContent = useDeferredValue(displayContent);
  const isRunning = d.status === "running";
  const isHighlighted = useCanvasStore((s) => s.highlightedTurnId != null && s.highlightedTurnId === d.turnId);
  const edges = useCanvasStore((s) => s.edges);
  const [activeEntityHandleId, setActiveEntityHandleId] = useState<string | null>(null);
  const [editorOpen, setEditorOpen] = useState(d.fullscreen === true);
  const actions = useMemo(() => [
    ...getTextActions(content),
    {
      icon: <Edit3 size={13} />,
      label: "Edit manuscript",
      onClick: () => setEditorOpen(true),
    },
    {
      icon: <Maximize2 size={13} />,
      label: "Fullscreen manuscript",
      onClick: () => setEditorOpen(true),
    },
    {
      icon: <TableIcon size={13} />,
      label: "拆成多维表格",
      onClick: () => {
        extractManuscriptToTable({
          nodeId: id,
          manuscriptTitle: d.manuscriptTitle,
          fullContent: content,
        });
      },
    },
  ], [content, id, d.manuscriptTitle]);

  const prepared = useMemo(() => {
    return prepareWithSegments(deferredContent, FONT_STYLE);
  }, [deferredContent]);

  const layoutResult = useMemo(() => {
    return layoutWithLines(prepared, CONTENT_WIDTH, LINE_HEIGHT);
  }, [prepared]);

  const bodyHeight = estimateMarkdownHeight(deferredContent, layoutResult.height || LINE_HEIGHT);
  const measuredHeight = d.collapsed
    ? HEADER_HEIGHT
    : BASE_HEIGHT + bodyHeight + (isRunning ? FOOTER_HEIGHT : 0);

  const entityHandles = useMemo(() => {
    if (d.collapsed) return [];
    return buildEntityHandles(layoutResult.lines, id);
  }, [layoutResult, id, d.collapsed]);

  const activeEdgeHandleIds = useMemo(
    () => new Set(edges.filter((edge) => edge.source === id && edge.selected && edge.sourceHandle).map((edge) => edge.sourceHandle as string)),
    [edges, id]
  );
  const activeHandleIds = useMemo(() => {
    const ids = new Set(activeEdgeHandleIds);
    if (activeEntityHandleId) ids.add(activeEntityHandleId);
    return ids;
  }, [activeEdgeHandleIds, activeEntityHandleId]);
  const markdownHtml = useMemo(() => renderMarkdown(deferredContent), [deferredContent]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, content: d.content, label: d.label },
      })
    );
  }, [id, d.content, d.label]);

  const handleEntityClick = useCallback((handle: EntityHandle) => {
    const state = useCanvasStore.getState();
    const relatedEdges = state.edges.filter((edge) => edge.source === id && edge.sourceHandle === handle.id);
    const relatedTargetIds = new Set(relatedEdges.map((edge) => edge.target));
    setActiveEntityHandleId(handle.id);
    state.setNodes(
      state.nodes.map((node) => ({
        ...node,
        selected: node.id === id || relatedTargetIds.has(node.id),
      }))
    );
    state.setEdges(
      state.edges.map((edge) => ({
        ...edge,
        selected: edge.source === id && edge.sourceHandle === handle.id,
      }))
    );
  }, [id]);

  return (
    <div
      className={`canvas-node canvas-node-agent ${selected ? "selected" : ""} ${isHighlighted ? "canvas-node-highlighted" : ""} ${isRunning ? "running" : ""}`}
      onContextMenu={handleContextMenu}
      style={{ width: NODE_WIDTH, height: measuredHeight }}
    >
      <NodeToolbar nodeId={id} selected={selected} actions={actions} />
      <div 
        className="canvas-node-header" 
        onClick={() => updateNode(id, { collapsed: !d.collapsed } as Partial<CanvasNodeData>)}
        style={{ cursor: "pointer" }}
      >
        <div className="canvas-node-icon-wrapper ai-active">
          <Sparkles size={14} className={isRunning ? "animate-pulse text-accent" : "text-accent"} />
        </div>
        <span className="canvas-node-label">{d.manuscriptTitle || d.label || t("canvas.aiTypo")}</span>
        <button
          type="button"
          className="gen-collapse-toggle manuscript-fullscreen-trigger"
          title="全屏编辑"
          onClick={(event) => {
            event.stopPropagation();
            setEditorOpen(true);
          }}
        >
          <Maximize2 size={13} />
        </button>
        <span className="gen-collapse-toggle">
          {d.collapsed ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
        </span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>

      {!d.collapsed && (
        <div className="canvas-node-body relative nowheel">
          <motion.div 
            className="ai-typo-container"
            layout
            transition={{ type: "spring", stiffness: 300, damping: 30 }}
            style={{
              fontFamily: FONT_FAMILY,
              fontSize: FONT_SIZE,
              lineHeight: `${LINE_HEIGHT}px`,
              minHeight: bodyHeight,
            }}
          >
            <AnimatePresence mode="popLayout">
              <motion.div
                key={deferredContent}
                className="ai-typo-markdown message-content"
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                transition={{ duration: 0.2 }}
                dangerouslySetInnerHTML={{ __html: markdownHtml }}
              />
            </AnimatePresence>
            {entityHandles.map((h) => (
              <div
                key={h.id}
                className="absolute"
                style={{
                  left: h.x - h.width / 2,
                  top: h.y - LINE_HEIGHT / 2,
                  width: h.width,
                  height: LINE_HEIGHT,
                }}
                onClick={(event) => {
                  event.stopPropagation();
                  handleEntityClick(h);
                }}
              >
                <Handle
                  type="source"
                  position={Position.Right}
                  id={h.id}
                  className={`word-level-handle word-level-handle-token ${activeHandleIds.has(h.id) ? "active" : ""}`}
                  title={`Connect from ${h.label}`}
                  style={{
                    background: "transparent",
                    width: h.width,
                    height: LINE_HEIGHT,
                    border: "none",
                    borderRadius: 999,
                    left: 0,
                    right: "auto",
                    top: 0,
                    transform: "none",
                    opacity: 0,
                  }}
                />
                <div className={`word-highlight animate-pulse ${activeHandleIds.has(h.id) ? "active" : ""}`} />
              </div>
            ))}
          </motion.div>
        </div>
      )}

      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
      
      {!d.collapsed && isRunning && (
        <div className="ai-typo-footer">
          <div className="typing-indicator">
            <span></span><span></span><span></span>
          </div>
        </div>
      )}
      {editorOpen && (
        <ManuscriptEditorOverlay
          nodeId={id}
          data={d}
          onClose={() => {
            setEditorOpen(false);
            updateNode(id, { fullscreen: false } as Partial<CanvasNodeData>);
          }}
        />
      )}
    </div>
  );
});
