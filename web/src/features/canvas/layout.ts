import type { Edge } from "@xyflow/react";
import type { CanvasNode } from "./types";

type Node = CanvasNode;

export type LayoutMode = "auto" | "horizontal" | "vertical" | "grid" | "tree";

const COMPONENT_GAP = 240;
const ISOLATED_GAP = 240;
const MIN_LAYER_PADDING = 160;
const MAX_LAYER_PADDING = 260;
const ROW_PADDING = 40;

function getNodeSize(node: Node) {
  const estimated = (() => {
    switch (node.type) {
      case "image":
        return { width: 280, height: 280 };
      case "video":
        return { width: 300, height: 300 };
      case "audio":
        return { width: 280, height: 140 };
      case "prompt":
        return { width: 260, height: 120 };
      case "agent":
        return { width: 280, height: 180 };
      case "aiTypo":
        return { width: 280, height: 220 };
      case "tool":
        return { width: 280, height: 200 };
      case "group":
        return { width: 300, height: 200 };
      case "composition":
        return { width: 280, height: 220 };
      case "text":
      default:
        return { width: 260, height: 160 };
    }
  })();

  const measuredW = node.measured?.width ?? node.width;
  const measuredH = node.measured?.height ?? node.height;

  return {
    width: Math.max(estimated.width, measuredW ?? 0),
    height: Math.max(estimated.height, measuredH ?? 0),
  };
}

function getLayerPadding(layerWidth: number) {
  return Math.min(MAX_LAYER_PADDING, Math.max(MIN_LAYER_PADDING, Math.round(layerWidth * 0.32)));
}

function getNodeCenterY(node: Node) {
  return node.position.y + getNodeSize(node).height / 2;
}

function getValidEdges(nodes: Node[], edges: Edge[]) {
  const ids = new Set(nodes.map((n) => n.id));
  return edges.filter((e) => ids.has(e.source) && ids.has(e.target));
}

function getBounds(nodes: Node[]) {
  let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
  for (const n of nodes) {
    if (n.position.x < minX) minX = n.position.x;
    if (n.position.x > maxX) maxX = n.position.x;
    if (n.position.y < minY) minY = n.position.y;
    if (n.position.y > maxY) maxY = n.position.y;
  }
  return { minX, maxX, minY, maxY };
}

function sortByPosition(a: Node, b: Node) {
  return a.position.x - b.position.x || a.position.y - b.position.y || a.id.localeCompare(b.id);
}

function partitionGraph(nodes: Node[], edges: Edge[]) {
  const validEdges = getValidEdges(nodes, edges);
  const adj = new Map<string, Set<string>>();
  for (const n of nodes) adj.set(n.id, new Set());
  for (const e of validEdges) {
    adj.get(e.source)?.add(e.target);
    adj.get(e.target)?.add(e.source);
  }

  const connectedIds = nodes.filter((n) => (adj.get(n.id)?.size ?? 0) > 0).map((n) => n.id);
  const connectedSet = new Set(connectedIds);
  const nodeMap = new Map(nodes.map((n) => [n.id, n]));
  const visited = new Set<string>();
  const components: Node[][] = [];

  for (const nid of connectedIds) {
    if (visited.has(nid)) continue;
    const stack = [nid];
    const comp: Node[] = [];
    visited.add(nid);
    while (stack.length > 0) {
      const cur = stack.pop()!;
      const node = nodeMap.get(cur);
      if (node) comp.push(node);
      for (const nb of adj.get(cur) ?? []) {
        if (!connectedSet.has(nb) || visited.has(nb)) continue;
        visited.add(nb);
        stack.push(nb);
      }
    }
    components.push(comp.sort(sortByPosition));
  }

  const isolated = nodes.filter((n) => !connectedSet.has(n.id)).sort(sortByPosition);
  components.sort((a, b) => {
    const la = getBounds(a), lb = getBounds(b);
    const cx = (la.minX + la.maxX) / 2 - (lb.minX + lb.maxX) / 2;
    return cx || (la.minY + la.maxY) / 2 - (lb.minY + lb.maxY) / 2;
  });

  return { components, isolated };
}

function layoutComponent(component: Node[], edges: Edge[], startX: number) {
  const ids = new Set(component.map((n) => n.id));
  const nodeById = new Map(component.map((n) => [n.id, n]));
  const compEdges = edges.filter((e) => ids.has(e.source) && ids.has(e.target));
  const outgoing = new Map<string, string[]>();
  const incoming = new Map<string, string[]>();
  const indegree = new Map<string, number>();
  const layers = new Map<string, number>();
  
  for (const n of component) {
    outgoing.set(n.id, []);
    incoming.set(n.id, []);
    indegree.set(n.id, 0);
  }
  for (const e of compEdges) {
    outgoing.get(e.source)?.push(e.target);
    incoming.get(e.target)?.push(e.source);
    indegree.set(e.target, (indegree.get(e.target) ?? 0) + 1);
  }

  // Phase 1: Assign layers (Longest Path layering for simplicity, can be improved to simplex)
  const queue = component.filter((n) => (indegree.get(n.id) ?? 0) === 0).sort(sortByPosition);
  while (queue.length > 0) {
    const cur = queue.shift()!;
    const layer = layers.get(cur.id) ?? 0;
    for (const tid of outgoing.get(cur.id) ?? []) {
      layers.set(tid, Math.max(layers.get(tid) ?? 0, layer + 1));
      indegree.set(tid, (indegree.get(tid) ?? 0) - 1);
      if ((indegree.get(tid) ?? 0) === 0) {
        const next = nodeById.get(tid);
        if (next) queue.push(next);
        queue.sort(sortByPosition);
      }
    }
  }
  
  // Handle cycles or unassigned
  const maxL = Math.max(...layers.values(), -1);
  component.filter(n => !layers.has(n.id)).forEach((n, i) => layers.set(n.id, maxL + 1 + i));

  const layerGroups = new Map<number, string[]>();
  for (const n of component) {
    const l = layers.get(n.id) ?? 0;
    if (!layerGroups.has(l)) layerGroups.set(l, []);
    layerGroups.get(l)!.push(n.id);
  }
  const orderedLayerIds = [...layerGroups.keys()].sort((a, b) => a - b);
  const layerNodeIds = orderedLayerIds.map(l => layerGroups.get(l)!);

  // Phase 2: Crossing Reduction using Iterative Barycenter
  // Initialize positions based on initial sort
  const nodePositions = new Map<string, number>(); // Layer index (0..N)
  layerNodeIds.forEach(nodes => nodes.forEach((id, idx) => nodePositions.set(id, idx)));

  const ITERATIONS = 4;
  for (let it = 0; it < ITERATIONS; it++) {
    // Forward pass (Downstream)
    for (let i = 1; i < layerNodeIds.length; i++) {
      const currentLayer = layerNodeIds[i];
      const prevLayerPos = nodePositions;
      
      const barycenters = currentLayer.map(id => {
        const preds = incoming.get(id) || [];
        if (preds.length === 0) return nodePositions.get(id) || 0;
        return preds.reduce((sum, pid) => sum + (prevLayerPos.get(pid) || 0), 0) / preds.length;
      });
      
      const zipped = currentLayer.map((id, idx) => ({ id, bary: barycenters[idx] }));
      zipped.sort((a, b) => a.bary - b.bary || a.id.localeCompare(b.id));
      layerNodeIds[i] = zipped.map(z => z.id);
      layerNodeIds[i].forEach((id, idx) => nodePositions.set(id, idx));
    }
    
    // Backward pass (Upstream)
    for (let i = layerNodeIds.length - 2; i >= 0; i--) {
      const currentLayer = layerNodeIds[i];
      const nextLayerPos = nodePositions;
      
      const barycenters = currentLayer.map(id => {
        const succs = outgoing.get(id) || [];
        if (succs.length === 0) return nodePositions.get(id) || 0;
        return succs.reduce((sum, sid) => sum + (nextLayerPos.get(sid) || 0), 0) / succs.length;
      });
      
      const zipped = currentLayer.map((id, idx) => ({ id, bary: barycenters[idx] }));
      zipped.sort((a, b) => a.bary - b.bary || a.id.localeCompare(b.id));
      layerNodeIds[i] = zipped.map(z => z.id);
      layerNodeIds[i].forEach((id, idx) => nodePositions.set(id, idx));
    }
  }

  // Phase 3: Coordinate Assignment
  const result: Node[] = [];
  let curX = startX;
  const placedCenters = new Map<string, number>();

  for (let i = 0; i < layerNodeIds.length; i++) {
    const currentLayerIds = layerNodeIds[i];
    const nodes = currentLayerIds.map(id => nodeById.get(id)!);
    
    const widths = nodes.map((n) => getNodeSize(n).width);
    const lw = widths.length > 0 ? Math.max(...widths) : 0;
    const lp = getLayerPadding(lw);

    // Calculate vertical center based on average of parents (if any)
    const targetCenter = nodes.reduce((sum, node) => {
      const parentIds = incoming.get(node.id) || [];
      if (parentIds.length === 0) return sum + getNodeCenterY(node);
      const anchor = parentIds.reduce((pSum, pid) => pSum + (placedCenters.get(pid) ?? getNodeCenterY(nodeById.get(pid)!)), 0) / parentIds.length;
      return sum + anchor;
    }, 0) / nodes.length;

    const totalHeight = nodes.reduce((sum, n) => sum + getNodeSize(n).height, 0) + ROW_PADDING * Math.max(nodes.length - 1, 0);
    let curY = Math.round(targetCenter - totalHeight / 2);

    for (const node of nodes) {
      const positionedNode = { ...node, position: { x: curX, y: curY } };
      result.push(positionedNode);
      placedCenters.set(node.id, getNodeCenterY(positionedNode));
      curY += getNodeSize(node).height + ROW_PADDING;
    }
    curX += lw + lp;
  }

  const lastLayerIds = layerNodeIds.at(-1);
  const lastW = lastLayerIds ? Math.max(...lastLayerIds.map(id => getNodeSize(nodeById.get(id)!).width)) : 0;
  return { nodes: result, width: Math.max(curX - startX - getLayerPadding(lastW), 0) };
}

/** Separate group children from root nodes; layout only root nodes. */
function partitionGrouped(nodes: Node[]) {
  const roots: Node[] = [];
  const children = new Map<string, Node[]>(); // groupId → children
  for (const n of nodes) {
    if (n.parentId) {
      const list = children.get(n.parentId) || [];
      list.push(n);
      children.set(n.parentId, list);
    } else {
      roots.push(n);
    }
  }
  return { roots, children };
}

/** After laying out root nodes, reattach group children with relative positions preserved. */
function reattachGroupChildren(laidOutRoots: Node[], children: Map<string, Node[]>): Node[] {
  if (children.size === 0) return laidOutRoots;
  const result = [...laidOutRoots];
  for (const [groupId, kids] of children) {
    // Children keep their relative positions (they're relative to group parent).
    // Just ensure they're in the output.
    for (const kid of kids) {
      result.push(kid);
    }
  }
  return result;
}

/** Grid layout: arrange nodes in rows and columns (good for media browsing). */
function gridLayout(nodes: Node[]): Node[] {
  if (nodes.length === 0) return [];
  const cols = Math.max(2, Math.ceil(Math.sqrt(nodes.length)));
  const cellW = 320;
  const cellH = 320;
  const startX = nodes.reduce((m, n) => Math.min(m, n.position.x), Infinity);
  const startY = nodes.reduce((m, n) => Math.min(m, n.position.y), Infinity);
  return nodes.map((n, i) => ({
    ...n,
    position: {
      x: startX + (i % cols) * cellW,
      y: startY + Math.floor(i / cols) * cellH,
    },
  }));
}

/** Vertical (top-to-bottom) layout: run Sugiyama then swap axes with adjusted spacing. */
function verticalLayout(nodes: Node[], edges: Edge[]): Node[] {
  const horizontal = horizontalLayout(nodes, edges);
  // Swap x/y and apply a scale factor to compensate for
  // different node width vs height (nodes are wider than tall).
  const scaleX = 0.7; // compress horizontal spread
  const scaleY = 1.3; // expand vertical spread
  const bounds = getBounds(horizontal);
  const cx = (bounds.minX + bounds.maxX) / 2;
  const cy = (bounds.minY + bounds.maxY) / 2;
  return horizontal.map((n) => ({
    ...n,
    position: {
      x: cy + (n.position.y - cy) * scaleX,
      y: cx + (n.position.x - cx) * scaleY,
    },
  }));
}

/** Standard horizontal (left-to-right) Sugiyama layout. */
function horizontalLayout(nodes: Node[], edges: Edge[]): Node[] {
  const validEdges = getValidEdges(nodes, edges);
  const { components, isolated } = partitionGraph(nodes, validEdges);
  if (components.length === 0) return [...nodes];

  const result: Node[] = [];
  let cursorX = components.reduce((m, c) => Math.min(m, getBounds(c).minX), Infinity);

  for (const comp of components) {
    const layout = layoutComponent(comp, validEdges, cursorX);
    result.push(...layout.nodes);
    cursorX += layout.width + COMPONENT_GAP;
  }

  if (isolated.length > 0) {
    const startX = cursorX + ISOLATED_GAP;
    const topY = nodes.reduce((m, node) => Math.min(m, node.position.y), Infinity);
    let curY = topY;
    for (const node of isolated) {
      result.push({ ...node, position: { x: startX, y: curY } });
      curY += getNodeSize(node).height + ROW_PADDING;
    }
  }

  return result;
}

/** Auto-detect the best layout mode based on graph structure (ignores group children). */
function detectLayoutMode(nodes: Node[], edges: Edge[]): LayoutMode {
  // Only consider root-level nodes for mode detection.
  const roots = nodes.filter((n) => !n.parentId);
  const validEdges = getValidEdges(roots, edges);
  const connectedCount = new Set([
    ...validEdges.map((e) => e.source),
    ...validEdges.map((e) => e.target),
  ]).size;
  const isolatedCount = roots.length - connectedCount;

  // Mostly isolated nodes (e.g. media gallery) → grid
  if (isolatedCount > connectedCount && isolatedCount >= 4) return "grid";

  // Check max branching factor
  const outDegree = new Map<string, number>();
  for (const e of validEdges) {
    outDegree.set(e.source, (outDegree.get(e.source) || 0) + 1);
  }
  const maxOut = Math.max(0, ...outDegree.values());

  // Linear chain → horizontal
  if (maxOut <= 1) return "horizontal";

  // Has branches → tree (same as horizontal Sugiyama, which handles DAGs)
  return "tree";
}

export function autoLayoutGraph(nodes: Node[], edges: Edge[], mode: LayoutMode = "auto"): Node[] {
  if (nodes.length === 0) return [];
  if (nodes.length === 1) return [...nodes];

  // 0. Separate pinned / locked nodes — they keep their positions and are excluded from layout.
  const pinned: Node[] = [];
  const unpinned: Node[] = [];
  for (const n of nodes) {
    const d = n.data as Record<string, unknown> | undefined;
    if ((d?.pinned || d?.locked) && !n.parentId) {
      pinned.push(n);
    } else {
      unpinned.push(n);
    }
  }

  // If all root nodes are pinned, nothing to layout.
  if (unpinned.filter((n) => !n.parentId).length === 0) return [...nodes];

  // Layout only unpinned nodes, then merge pinned back at their original positions.
  const laidOut = _autoLayoutGraphInner(unpinned, edges, mode);

  // Merge: pinned nodes retain original position.
  return [...laidOut, ...pinned];
}

function _autoLayoutGraphInner(nodes: Node[], edges: Edge[], mode: LayoutMode): Node[] {
  if (nodes.length === 0) return [];
  if (nodes.length === 1) return [...nodes];

  // 1. Recursive Pass: Layout children of each group first
  const groupIds = new Set(nodes.filter(n => n.type === "group").map(n => n.id));
  let processedNodes = [...nodes];

  for (const groupId of groupIds) {
    const children = processedNodes.filter(n => n.parentId === groupId);
    if (children.length > 0) {
      // Recursively layout children (assuming children don't have further nested groups for now)
      // If nesting is deep, this should be a truly recursive DFS approach.
      const laidOutChildren = autoLayoutGraph(children, edges, "auto");
      
      // Update processedNodes with new relative positions
      const childIds = new Set(children.map(c => c.id));
      processedNodes = processedNodes.map(n => childIds.has(n.id) ? laidOutChildren.find(lc => lc.id === n.id)! : n);
      
      // Calculate bounding box of laid out children (including node sizes) to resize the group.
      const padding = 20;
      const headerH = 40;
      let gMinX = Infinity, gMinY = Infinity, gMaxX = -Infinity, gMaxY = -Infinity;
      for (const child of laidOutChildren) {
        const size = getNodeSize(child);
        gMinX = Math.min(gMinX, child.position.x);
        gMinY = Math.min(gMinY, child.position.y);
        gMaxX = Math.max(gMaxX, child.position.x + size.width);
        gMaxY = Math.max(gMaxY, child.position.y + size.height);
      }
      const groupWidth = gMaxX - gMinX + padding * 2;
      const groupHeight = gMaxY - gMinY + headerH + padding;
      
      // Shift children to be relative to the group's new (0,0) which is at (-padding, -headerH)
      processedNodes = processedNodes.map(n => {
        if (n.parentId === groupId) {
          return {
            ...n,
            position: { x: n.position.x - gMinX + padding, y: n.position.y - gMinY + headerH }
          };
        }
        if (n.id === groupId) {
          return {
            ...n,
            measured: { width: groupWidth, height: groupHeight },
            width: groupWidth,
            height: groupHeight,
            style: { ...n.style, width: groupWidth, height: groupHeight }
          };
        }
        return n;
      });
    }
  }

  // 2. Main Pass: Layout root-level nodes (including the resized groups)
  const { roots, children: allChildrenMap } = partitionGrouped(processedNodes);
  
  const effectiveMode = mode === "auto" ? detectLayoutMode(roots, edges) : mode;

  let laidOutRoots: Node[];
  switch (effectiveMode) {
    case "grid":
      laidOutRoots = gridLayout(roots);
      break;
    case "vertical":
      laidOutRoots = verticalLayout(roots, edges);
      break;
    case "horizontal":
    case "tree":
    default:
      laidOutRoots = horizontalLayout(roots, edges);
      break;
  }

  // 3. Reattach Pass: Bring back the children (their positions are already relative)
  return reattachGroupChildren(laidOutRoots, allChildrenMap);
}
