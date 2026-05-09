import type { CanvasNodeData, RefType } from "./types";

export interface ReferenceBundle {
  nodeId: string;
  refType: RefType;
  strength: number;
  mediaUrl?: string;
  mediaType?: "image" | "video" | "audio";
}

type MediaNode = {
  id: string;
  data?: {
    mediaType?: unknown;
    mediaUrl?: unknown;
  };
};

type MediaEdge = {
  source: string;
  target: string;
};

function pushUnique(urls: string[], seen: Set<string>, value: unknown) {
  if (typeof value !== "string") {
    return;
  }

  const url = value.trim();
  if (!url || seen.has(url)) {
    return;
  }

  seen.add(url);
  urls.push(url);
}

export function collectVideoReferences(
  targetNodeId: string,
  edges: MediaEdge[],
  nodes: MediaNode[],
) {
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const imageUrls: string[] = [];
  const videoUrls: string[] = [];
  const seenImages = new Set<string>();
  const seenVideos = new Set<string>();

  for (const edge of edges) {
    if (edge.target !== targetNodeId) {
      continue;
    }

    const sourceNode = nodeById.get(edge.source);
    const mediaType = sourceNode?.data?.mediaType;

    if (mediaType === "image") {
      pushUnique(imageUrls, seenImages, sourceNode?.data?.mediaUrl);
      continue;
    }

    if (mediaType === "video") {
      pushUnique(videoUrls, seenVideos, sourceNode?.data?.mediaUrl);
    }
  }

  return { imageUrls, videoUrls };
}

export function collectLinkedImageNodes(
  targetNodeId: string,
  edges: Array<{ source: string; target: string }>,
  nodes: Array<{ id: string; data?: CanvasNodeData }>,
): CanvasNodeData[] {
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const refs: CanvasNodeData[] = [];
  const seen = new Set<string>();

  for (const edge of edges) {
    if (edge.target !== targetNodeId || seen.has(edge.source)) {
      continue;
    }
    const sourceNode = nodeById.get(edge.source);
    if (sourceNode?.data?.mediaType !== "image") {
      continue;
    }
    seen.add(edge.source);
    refs.push(sourceNode.data);
  }

  return refs;
}

/** Walk upstream reference nodes and return their refType/strength bundles.
 *  If the reference node has no media of its own, walks one hop upstream to
 *  find the attached media node. */
export function collectReferenceNodes(
  targetNodeId: string,
  edges: Array<{ source: string; target: string }>,
  nodes: Array<{ id: string; data?: CanvasNodeData }>,
): ReferenceBundle[] {
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const bundles: ReferenceBundle[] = [];

  for (const edge of edges) {
    if (edge.target !== targetNodeId) continue;
    const sourceNode = nodeById.get(edge.source);
    const d = sourceNode?.data;
    if (!d || d.nodeType !== "reference") continue;

    let mediaUrl = typeof d.mediaUrl === "string" ? d.mediaUrl : undefined;
    let mediaType = (d.mediaType as "image" | "video" | "audio" | undefined) || "image";
    if (!mediaUrl) {
      for (const e2 of edges) {
        if (e2.target !== edge.source) continue;
        const upstream = nodeById.get(e2.source);
        const ud = upstream?.data;
        if (ud && typeof ud.mediaUrl === "string" && ud.mediaUrl) {
          mediaUrl = ud.mediaUrl;
          if (ud.mediaType) mediaType = ud.mediaType as "image" | "video" | "audio";
          break;
        }
      }
    }

    bundles.push({
      nodeId: edge.source,
      refType: (d.refType as RefType) || "style",
      strength: typeof d.refStrength === "number" ? d.refStrength : 1,
      mediaUrl,
      mediaType,
    });
  }

  return bundles;
}
