import { useRpcStore } from "@/features/rpc/rpcStore";
import type { CanvasNodeData, MediaType } from "./types";

interface CachedMediaResult {
  path?: string;
  url?: string;
  media_type?: string;
  source_url?: string;
}

interface MediaDataUrlResult {
  data_url?: string;
}

export async function cacheCanvasMedia(
  rawUrl: string,
  mediaType: MediaType,
): Promise<Pick<CanvasNodeData, "mediaUrl" | "mediaPath" | "sourceUrl">> {
  const trimmed = rawUrl.trim();
  if (!trimmed) {
    return { mediaUrl: trimmed };
  }

  const rpc = useRpcStore.getState().rpc;
  if (!rpc) {
    return { mediaUrl: trimmed, sourceUrl: isRemoteMediaUrl(trimmed) ? trimmed : undefined };
  }

  try {
    const res = await rpc.request<CachedMediaResult>("media/cache", { url: trimmed, mediaType });
    return {
      mediaUrl: typeof res.url === "string" && res.url.trim() !== "" ? res.url : trimmed,
      mediaPath: typeof res.path === "string" && res.path.trim() !== "" ? res.path : undefined,
      sourceUrl: typeof res.source_url === "string" && res.source_url.trim() !== "" ? res.source_url : isRemoteMediaUrl(trimmed) ? trimmed : undefined,
    };
  } catch {
    return { mediaUrl: trimmed, sourceUrl: isRemoteMediaUrl(trimmed) ? trimmed : undefined };
  }
}

export async function resolveCanvasReferenceUrl(
  data: CanvasNodeData,
  mediaType: MediaType,
): Promise<string> {
  const rpc = useRpcStore.getState().rpc;

  if (typeof data.mediaPath === "string" && data.mediaPath.trim() !== "" && rpc) {
    try {
      const res = await rpc.request<MediaDataUrlResult>("media/data_url", { path: data.mediaPath, mediaType });
      if (typeof res.data_url === "string" && res.data_url.trim() !== "") {
        return res.data_url.trim();
      }
    } catch {
      // Fall back to other known references below.
    }
  }

  if (typeof data.sourceUrl === "string" && data.sourceUrl.trim() !== "") {
    return data.sourceUrl.trim();
  }

  if (typeof data.mediaUrl === "string" && data.mediaUrl.trim() !== "") {
    return data.mediaUrl.trim();
  }

  return "";
}

function isRemoteMediaUrl(value: string) {
  return /^https?:\/\//i.test(value) || /^data:/i.test(value);
}
