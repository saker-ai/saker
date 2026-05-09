// Mirrors saker/web/src/features/editor-bridge/protocol.ts.
// Keep these definitions in sync — the postMessage contract crosses both apps.

export const EDITOR_EXPORT_MESSAGE_TYPE = "saker:editor:export";
export const EDITOR_EXPORT_BROADCAST_CHANNEL = "saker:editor:export";

const STORAGE_KEY_PREFIX = "saker:editor:import:";
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
	 * so the bridge can update that node in place rather than creating a
	 * new library asset.
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
	originNodeId?: string;
	/**
	 * Idempotency key — the canvas-side bridge dedupes against this when the
	 * same message arrives via both window.opener.postMessage and the
	 * BroadcastChannel fallback.
	 */
	messageId?: string;
}

export function newExportMessageId(): string {
	const rand =
		typeof crypto !== "undefined" && "randomUUID" in crypto
			? crypto.randomUUID()
			: Math.random().toString(36).slice(2);
	return `${Date.now().toString(36)}-${rand}`;
}

interface StoredImport {
	ts: number;
	payload: EditorImportPayload;
}

function utf8ToBase64Url(str: string): string {
	const utf8 = new TextEncoder().encode(str);
	let binary = "";
	for (let i = 0; i < utf8.length; i++) binary += String.fromCharCode(utf8[i]);
	const b64 =
		typeof btoa !== "undefined" ? btoa(binary) : window.btoa(binary);
	return b64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function base64UrlToUtf8(b64url: string): string {
	const padded = b64url.replace(/-/g, "+").replace(/_/g, "/");
	const pad =
		padded.length % 4 === 0 ? "" : "=".repeat(4 - (padded.length % 4));
	const dec = typeof atob !== "undefined" ? atob : window.atob;
	const binary = dec(padded + pad);
	const bytes = new Uint8Array(binary.length);
	for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
	return new TextDecoder().decode(bytes);
}

export function encodeImportPayload(payload: EditorImportPayload): string {
	return utf8ToBase64Url(JSON.stringify(payload));
}

export function decodeImportPayload(
	encoded: string,
): EditorImportPayload | null {
	try {
		const parsed = JSON.parse(base64UrlToUtf8(encoded));
		if (!parsed || !Array.isArray(parsed.assets)) return null;
		return parsed as EditorImportPayload;
	} catch {
		return null;
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

/**
 * Read `?import=inline:<b64>` or `?import=ls:<key>` from the current URL and
 * return the decoded payload, or `null` if there is no pending import. Should
 * be called once on editor mount.
 */
export function readImportFromQuery(
	search: string,
): EditorImportPayload | null {
	if (!search) return null;
	const params = new URLSearchParams(search);
	const raw = params.get("import");
	if (!raw) return null;
	if (raw.startsWith("inline:")) {
		return decodeImportPayload(raw.slice("inline:".length));
	}
	if (raw.startsWith("ls:")) {
		return readStoredImport(raw.slice("ls:".length));
	}
	return null;
}

/**
 * Send the export back to the canvas page. Tries window.opener first
 * (works when the editor was opened via window.open from the canvas);
 * also broadcasts on a same-origin BroadcastChannel so the canvas can
 * receive the export even when opener is gone (closed/navigated/refreshed).
 *
 * Both routes carry the same messageId so the canvas-side bridge can
 * dedupe.
 */
export function postExportToOpener(message: EditorExportMessage): void {
	if (typeof window === "undefined") return;
	const enriched: EditorExportMessage = {
		...message,
		messageId: message.messageId ?? newExportMessageId(),
	};
	const opener = window.opener as Window | null;
	if (opener) {
		try {
			opener.postMessage(enriched, window.location.origin);
		} catch {
			/* ignore — opener may have navigated away */
		}
	}
	if (typeof BroadcastChannel !== "undefined") {
		try {
			const channel = new BroadcastChannel(EDITOR_EXPORT_BROADCAST_CHANNEL);
			channel.postMessage(enriched);
			channel.close();
		} catch {
			/* ignore — BroadcastChannel may be unavailable */
		}
	}
}
