import type { StreamEvent } from "@/features/rpc/types";

/**
 * Extract media info from a tool_execution_result stream event.
 *
 * Three detection paths (in priority order):
 * 1. Structured metadata (aigo tools) — media_type + media_url
 * 2. Data metadata (ImageRead etc.) — MIME type + file path
 * 3. Regex on output text — HTTP URLs and local file paths
 */
export function extractMedia(
  evt: StreamEvent
): { type: string; url: string } | null {
  const output = evt.output as Record<string, unknown> | undefined;
  if (!output || typeof output !== "object") return null;

  // Path 1: structured metadata (aigo tools)
  const metadata = output.metadata as Record<string, unknown> | undefined;
  const structured = metadata?.structured as
    | Record<string, unknown>
    | undefined;
  const mediaType = (structured?.media_type ?? output.media_type) as
    | string
    | undefined;
  const mediaUrl =
    ((structured?.media_url ?? output.media_url) as string) || "";

  if (mediaType && mediaUrl) return { type: mediaType, url: mediaUrl };

  // Path 2: tool Data metadata (ImageRead etc.)
  const data = metadata?.data as Record<string, unknown> | undefined;
  if (data?.media_type && (data?.absolute_path || data?.path)) {
    const mime = data.media_type as string;
    const filePath =
      (data.absolute_path as string) || (data.path as string);
    const t = mime.startsWith("video/")
      ? "video"
      : mime.startsWith("audio/")
        ? "audio"
        : "image";
    const url = filePath.startsWith("/")
      ? `/api/files${filePath}`
      : `/api/files/${filePath}`;
    return { type: t, url };
  }

  // Path 3: detect media URLs/paths in output text
  const outText = (output.output as string) || "";
  if (outText) {
    // HTTP(S) URLs with optional query string (e.g. signed DashScope URLs)
    const urlMatch = outText.match(
      /https?:\/\/[^\s"']+\.(png|jpe?g|gif|webp|svg|mp4|webm|mp3|wav|ogg)(\?[^\s"']*)?/i
    );
    if (urlMatch) {
      const ext = urlMatch[1].toLowerCase();
      const t = /^(mp4|webm)$/.test(ext)
        ? "video"
        : /^(mp3|wav|ogg)$/.test(ext)
          ? "audio"
          : "image";
      return { type: t, url: urlMatch[0] };
    }
    // Absolute local file paths
    const pathMatch = outText.match(
      /\/[\w/._-]+\.(png|jpe?g|gif|webp|mp4|webm|mp3|wav|ogg)\b/i
    );
    if (pathMatch) {
      const ext = pathMatch[1].toLowerCase();
      const t = /^(mp4|webm)$/.test(ext)
        ? "video"
        : /^(mp3|wav|ogg)$/.test(ext)
          ? "audio"
          : "image";
      return { type: t, url: `/api/files${pathMatch[0]}` };
    }
    // Relative file paths with directory (e.g. output/images/cat.png).
    // Must contain at least one / to exclude bare filenames from ls output.
    const relMatch = outText.match(
      /(?:^|[\s"'=])([\w.][\w./_-]*\/[\w./_-]*\.(png|jpe?g|gif|webp|mp4|webm|mp3|wav|ogg))(?:\s|$|["'])/i
    );
    if (relMatch) {
      const ext = relMatch[2].toLowerCase();
      const t = /^(mp4|webm)$/.test(ext)
        ? "video"
        : /^(mp3|wav|ogg)$/.test(ext)
          ? "audio"
          : "image";
      return { type: t, url: `/api/files/${relMatch[1]}` };
    }
  }

  return null;
}
