// Receiver-side protocol for the editor bridge.
// Shared types, constants, and codec are in @saker/editor-protocol.

import {
	EDITOR_EXPORT_BROADCAST_CHANNEL,
	encodeImportPayload,
	decodeImportPayload,
	readStoredImport,
	newExportMessageId,
} from "@saker/editor-protocol";
import type {
	EditorAsset,
	EditorImportPayload,
	EditorExportMessage,
} from "@saker/editor-protocol";

// Re-export shared items for local consumers.
export {
	EDITOR_EXPORT_MESSAGE_TYPE,
	EDITOR_EXPORT_BROADCAST_CHANNEL,
	encodeImportPayload,
	decodeImportPayload,
	readStoredImport,
	newExportMessageId,
} from "@saker/editor-protocol";
export type {
	EditorAsset,
	EditorImportPayload,
	EditorExportMessage,
} from "@saker/editor-protocol";

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