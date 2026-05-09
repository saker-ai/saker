// isValidMediaUrl is the frontend mirror of pkg/canvas.looksLikeMediaURL.
// It guards against the canvas writing opaque task-id UUIDs (e.g.
// "c424ec41-...") into <video src=>, which would otherwise show up as a
// silent black frame. Anything with a recognized scheme or a leading slash
// is allowed through; bare identifiers are rejected so the UI can render
// an explicit error placeholder instead of a broken media element.
export function isValidMediaUrl(u?: string | null): boolean {
  if (!u) return false;
  if (u.startsWith("/")) return true;
  return (
    u.startsWith("http://") ||
    u.startsWith("https://") ||
    u.startsWith("data:") ||
    u.startsWith("blob:") ||
    u.startsWith("file://")
  );
}
