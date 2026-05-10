/** Shared encoding/decoding functions for the editor-bridge postMessage contract. */

import type { EditorImportPayload, StoredImport } from "./types";
import { STORAGE_KEY_PREFIX, STORAGE_TTL_MS } from "./constants";

function utf8ToBase64Url(str: string): string {
	const utf8 = new TextEncoder().encode(str);
	let binary = "";
	for (let i = 0; i < utf8.length; i++) binary += String.fromCharCode(utf8[i]);
	const b64 =
		typeof btoa !== "undefined" ? btoa(binary) : (window.btoa as typeof btoa)(binary);
	return b64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function base64UrlToUtf8(b64url: string): string {
	if (!b64url) return "";
	const padded = b64url.replace(/-/g, "+").replace(/_/g, "/");
	const pad = padded.length % 4 === 0 ? "" : "=".repeat(4 - (padded.length % 4));
	const dec = typeof atob !== "undefined" ? atob : (window.atob as typeof atob);
	const binary = dec(padded + pad);
	const bytes = new Uint8Array(binary.length);
	for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
	return new TextDecoder().decode(bytes);
}

/** Encode an import payload to a compact base64url string for URL transfer. */
export function encodeImportPayload(payload: EditorImportPayload): string {
	return utf8ToBase64Url(JSON.stringify(payload));
}

/** Decode a base64url-encoded import payload. Returns null on parse failure. */
export function decodeImportPayload(encoded: string): EditorImportPayload | null {
	try {
		const parsed = JSON.parse(base64UrlToUtf8(encoded));
		if (!parsed || !Array.isArray(parsed.assets)) return null;
		return parsed as EditorImportPayload;
	} catch {
		return null;
	}
}

/** Read and consume a localStorage-stored import payload by key suffix. */
export function readStoredImport(key: string): EditorImportPayload | null {
	if (typeof window === "undefined" || !window.localStorage) return null;
	const fullKey = STORAGE_KEY_PREFIX + key;
	try {
		const v = window.localStorage.getItem(fullKey);
		if (!v) return null;
		window.localStorage.removeItem(fullKey);
		const r = JSON.parse(v) as StoredImport;
		if (!r.ts || Date.now() - r.ts > STORAGE_TTL_MS) return null;
		if (!r.payload || !Array.isArray(r.payload.assets)) return null;
		return r.payload;
	} catch {
		return null;
	}
}

/** Generate a UUID-based idempotency key for export messages. */
export function newExportMessageId(): string {
	const rand =
		typeof crypto !== "undefined" && "randomUUID" in crypto
			? crypto.randomUUID()
			: Math.random().toString(36).slice(2);
	return `${Date.now().toString(36)}-${rand}`;
}

export { utf8ToBase64Url, base64UrlToUtf8 };