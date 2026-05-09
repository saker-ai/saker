type LoadedNode = {
  id: string;
  type?: string;
  data?: Record<string, unknown>;
};

type LoadedEdge = {
  source: string;
  target: string;
};

const MEDIA_TYPES = new Set(["image", "video", "audio"]);

function hasMediaUrl(node: LoadedNode) {
  if (!MEDIA_TYPES.has(node.type || "")) {
    return true;
  }

  const mediaUrl = node.data?.mediaUrl;
  return typeof mediaUrl === "string" && mediaUrl.trim() !== "";
}

export function sanitizeLoadedCanvas<TNode extends LoadedNode, TEdge extends LoadedEdge>(
  rawNodes: TNode[],
  rawEdges: TEdge[],
) {
  const removedIds = new Set(
    rawNodes
      .filter((node) => !node.id || !node.type || !hasMediaUrl(node))
      .map((node) => node.id),
  );

  // Fix stale "running" status — persisted canvas cannot have active tasks.
  for (const node of rawNodes) {
    if (node.data?.status === "running") {
      node.data.status = "done";
      if (!node.data.endTime) node.data.endTime = node.data.startTime;
    }
  }

  if (removedIds.size === 0) {
    return { nodes: rawNodes, edges: rawEdges };
  }

  return {
    nodes: rawNodes.filter((node) => !removedIds.has(node.id)),
    edges: rawEdges.filter((edge) => !removedIds.has(edge.source) && !removedIds.has(edge.target)),
  };
}
