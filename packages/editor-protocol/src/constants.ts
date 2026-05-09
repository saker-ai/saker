/** Shared constants for the editor-bridge postMessage contract. */

export const EDITOR_EXPORT_MESSAGE_TYPE = "saker:editor:export";
export const EDITOR_EXPORT_BROADCAST_CHANNEL = "saker:editor:export";

/** localStorage key prefix for import payloads that exceed inline URL limits. */
export const STORAGE_KEY_PREFIX = "saker:editor:import:";

/** TTL for localStorage import payloads (5 minutes). */
export const STORAGE_TTL_MS = 5 * 60 * 1000;