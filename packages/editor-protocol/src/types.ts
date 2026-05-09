/** Shared types for the editor-bridge postMessage contract. */

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
	type: "saker:editor:export";
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

/** Internal storage record for localStorage-based payload transfer. */
export interface StoredImport {
	ts: number;
	payload: EditorImportPayload;
}