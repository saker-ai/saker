// The editor sub-app is mounted at /editor. Its root page preserves any
// ?import=... payload while redirecting to the actual editor route.
export const EDITOR_BASE_PATH = "/editor/";
export const EDITOR_EXPORT_MESSAGE_TYPE = "saker:editor:export";
export const EDITOR_EXPORT_BROADCAST_CHANNEL = "saker:editor:export";

const STORAGE_KEY_PREFIX = "saker:editor:import:";
const INLINE_LIMIT_CHARS = 6000;
const STORAGE_TTL_MS = 5 * 60 * 1000;

export interface EditorAsset {
  url: string;
  type: "video" | "audio" | "image";
  label?: string;
  durationMs?: number;
}

export interface EditorImportPayload {
  assets: EditorAsset[];
  /**
   * Canvas node that opened the editor. Echoed back in the export message
   * so the bridge can update that node's mediaUrl in place rather than
   * creating a new library asset.
   */
  originNodeId?: string;
}

export interface EditorExportMessage {
  type: typeof EDITOR_EXPORT_MESSAGE_TYPE;
  filename: string;
  size?: number;
  mimeType?: string;
  blob?: Blob;
  dataUrl?: string;
  /**
   * If set, the canvas-side bridge will write the export back into this
   * node instead of pushing it as a new library asset.
   */
  originNodeId?: string;
  /**
   * Idempotency key — the canvas-side bridge dedupes against this when the
   * same message arrives via both window.opener.postMessage and the
   * BroadcastChannel fallback.
   */
  messageId?: string;
}

interface StoredImport {
  ts: number;
  payload: EditorImportPayload;
}

function utf8ToBase64Url(str: string): string {
  const utf8 = new TextEncoder().encode(str);
  let binary = "";
  for (let i = 0; i < utf8.length; i++) binary += String.fromCharCode(utf8[i]);
  const b64 = (typeof btoa !== "undefined" ? btoa : (window.btoa as typeof btoa))(binary);
  return b64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function base64UrlToUtf8(b64url: string): string {
  const padded = b64url.replace(/-/g, "+").replace(/_/g, "/");
  const pad = padded.length % 4 === 0 ? "" : "=".repeat(4 - (padded.length % 4));
  const dec = (typeof atob !== "undefined" ? atob : (window.atob as typeof atob));
  const binary = dec(padded + pad);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return new TextDecoder().decode(bytes);
}

export function encodeImportPayload(payload: EditorImportPayload): string {
  return utf8ToBase64Url(JSON.stringify(payload));
}

export function decodeImportPayload(encoded: string): EditorImportPayload | null {
  try {
    const parsed = JSON.parse(base64UrlToUtf8(encoded));
    if (!parsed || !Array.isArray(parsed.assets)) return null;
    return parsed as EditorImportPayload;
  } catch {
    return null;
  }
}

function tryWriteStorage(payload: EditorImportPayload): string | null {
  if (typeof window === "undefined" || !window.localStorage) return null;
  try {
    const key = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
    const record: StoredImport = { ts: Date.now(), payload };
    window.localStorage.setItem(STORAGE_KEY_PREFIX + key, JSON.stringify(record));
    pruneExpiredStorage();
    return key;
  } catch {
    return null;
  }
}

function pruneExpiredStorage(): void {
  if (typeof window === "undefined" || !window.localStorage) return;
  const cutoff = Date.now() - STORAGE_TTL_MS;
  const drop: string[] = [];
  for (let i = 0; i < window.localStorage.length; i++) {
    const k = window.localStorage.key(i);
    if (!k || !k.startsWith(STORAGE_KEY_PREFIX)) continue;
    try {
      const v = window.localStorage.getItem(k);
      if (!v) continue;
      const r = JSON.parse(v) as StoredImport;
      if (!r.ts || r.ts < cutoff) drop.push(k);
    } catch {
      drop.push(k);
    }
  }
  for (const k of drop) {
    try {
      window.localStorage.removeItem(k);
    } catch {
      /* ignore */
    }
  }
}

export function readStoredImport(key: string): EditorImportPayload | null {
  if (typeof window === "undefined" || !window.localStorage) return null;
  const fullKey = STORAGE_KEY_PREFIX + key;
  try {
    const v = window.localStorage.getItem(fullKey);
    if (!v) return null;
    window.localStorage.removeItem(fullKey);
    const r = JSON.parse(v) as StoredImport;
    if (!r.ts || Date.now() - r.ts > STORAGE_TTL_MS) return null;
    return r.payload;
  } catch {
    return null;
  }
}

export function buildEditorImportUrl(
  input: EditorAsset[] | EditorImportPayload,
): string {
  const payload: EditorImportPayload = Array.isArray(input)
    ? { assets: input }
    : input;
  if (payload.assets.length === 0) return EDITOR_BASE_PATH;
  const inline = encodeImportPayload(payload);
  if (inline.length <= INLINE_LIMIT_CHARS) {
    return `${EDITOR_BASE_PATH}?import=inline:${inline}`;
  }
  const storageKey = tryWriteStorage(payload);
  if (storageKey) return `${EDITOR_BASE_PATH}?import=ls:${storageKey}`;
  // Last resort — try inline anyway, may exceed URL limits but better than failing.
  return `${EDITOR_BASE_PATH}?import=inline:${inline}`;
}

export function isEditorExportMessage(value: unknown): value is EditorExportMessage {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  if (v.type !== EDITOR_EXPORT_MESSAGE_TYPE) return false;
  if (typeof v.filename !== "string") return false;
  const hasBlob = typeof Blob !== "undefined" && v.blob instanceof Blob;
  const hasDataUrl = typeof v.dataUrl === "string" && v.dataUrl.length > 0;
  return hasBlob || hasDataUrl;
}
