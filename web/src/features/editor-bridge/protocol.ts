// Sender-side protocol for the editor bridge.
// Shared types, constants, and codec are in @saker/editor-protocol.

import {
	EDITOR_EXPORT_MESSAGE_TYPE,
	EDITOR_EXPORT_BROADCAST_CHANNEL,
	STORAGE_KEY_PREFIX,
	STORAGE_TTL_MS,
	encodeImportPayload,
	readStoredImport,
} from "@saker/editor-protocol";
import type {
	EditorAsset,
	EditorImportPayload,
	EditorExportMessage,
	StoredImport,
} from "@saker/editor-protocol";

// Re-export shared items for local consumers.
export {
	EDITOR_EXPORT_MESSAGE_TYPE,
	EDITOR_EXPORT_BROADCAST_CHANNEL,
	encodeImportPayload,
	decodeImportPayload,
	readStoredImport,
} from "@saker/editor-protocol";
export type {
	EditorAsset,
	EditorImportPayload,
	EditorExportMessage,
} from "@saker/editor-protocol";

export const EDITOR_BASE_PATH = "/editor/";
const INLINE_LIMIT_CHARS = 6000;

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